// Package server exposes the read-only aha tool surface over HTTP and serves
// a minimal loopback dashboard. The HTTP routes share their implementations
// with the MCP server: both go through the same underlying corpus/search
// functions, so output shapes stay in lockstep.
//
// Security posture:
//   - Loopback bind by default; non-loopback requires --allow-remote.
//   - Every request's Host header is validated against an allowlist to
//     blunt DNS-rebinding from a malicious public site that resolves to
//     127.0.0.1. The allowlist defaults to localhost/127.0.0.1/[::1]; for
//     remote binds the configured host:port is added.
//   - POST routes require Content-Type: application/json so cross-origin
//     "simple" form posts (text/plain) cannot reach the JSON parser.
//   - The served HTML carries a strict Content-Security-Policy.
//   - All routes are read-only.
package server

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"

	"github.com/adewale/aha/internal/mcp"
	"github.com/adewale/aha/internal/model"
)

// Options configures the HTTP server.
type Options struct {
	Addr         string   // host:port; default "127.0.0.1:18428"
	AllowRemote  bool     // allow non-loopback bind
	AllowedHosts []string // additional Host header values to accept
	// Token, when non-empty, requires every request to carry an
	// `Authorization: Bearer <token>` header. Borrowed from the
	// cloudflare-mcp pattern of per-request bearer verification (which
	// they do against the upstream API; for a single-user local tool a
	// shared-secret is sufficient). Constant-time compared on every
	// request.
	//
	// REQUIRED when AllowRemote is true. Optional (but allowed) on
	// loopback binds — useful when the loopback dashboard is shared with
	// trusted local processes that want to authenticate.
	Token string
}

// Server is the embedded HTTP handler. Tests can drive its ServeHTTP directly
// or stand it up via Listen.
type Server struct {
	backend      mcp.Backend
	mux          *http.ServeMux
	allowedHosts map[string]struct{}
	staticHand   http.Handler
	token        []byte // empty = auth disabled; non-empty = bearer required
}

// defaultAllowedHosts are the Host header values the dashboard accepts on
// every request, regardless of binding. They match all standard loopback
// presentations a browser might send.
func defaultAllowedHosts() []string {
	return []string{"localhost", "127.0.0.1", "[::1]", "::1"}
}

// New wires up routes against the given read-only backend.
// Equivalent to NewWithOptions(backend, Options{}).
func New(backend mcp.Backend) *Server {
	return NewWithOptions(backend, Options{})
}

// NewWithOptions builds a Server using the given Options. Tests that want
// non-default host allowlists or bind behaviour pass them through here.
func NewWithOptions(backend mcp.Backend, opts Options) *Server {
	hosts := map[string]struct{}{}
	for _, h := range defaultAllowedHosts() {
		hosts[strings.ToLower(h)] = struct{}{}
	}
	for _, h := range opts.AllowedHosts {
		if h != "" {
			hosts[strings.ToLower(h)] = struct{}{}
		}
	}
	if opts.Addr != "" {
		hosts[strings.ToLower(opts.Addr)] = struct{}{}
		if h, _, err := net.SplitHostPort(opts.Addr); err == nil && h != "" {
			hosts[strings.ToLower(h)] = struct{}{}
		}
	}
	s := &Server{
		backend:      backend,
		mux:          http.NewServeMux(),
		allowedHosts: hosts,
		staticHand:   http.StripPrefix("/static/", http.FileServer(http.FS(staticFS()))),
		token:        []byte(opts.Token),
	}
	s.routes()
	return s
}

// ServeHTTP implements http.Handler. Every request first passes through the
// Host validator and (when configured) the bearer-token check; failing
// requests get 421 Misdirected Request or 401 Unauthorized respectively
// rather than silently serving the corpus to an unauthenticated or
// attacker-controlled origin.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !s.hostAllowed(r.Host) {
		writeError(w, http.StatusMisdirectedRequest, "host not permitted")
		return
	}
	if !s.tokenAccepted(r) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="aha"`)
		writeError(w, http.StatusUnauthorized, "missing or invalid bearer token")
		return
	}
	s.mux.ServeHTTP(w, r)
}

// tokenAccepted returns true when the configured token (if any) matches the
// presented Authorization header. Compared in constant time to avoid
// timing leaks. When no token is configured, every request is accepted.
func (s *Server) tokenAccepted(r *http.Request) bool {
	if len(s.token) == 0 {
		return true
	}
	auth := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(auth, prefix) {
		return false
	}
	presented := []byte(auth[len(prefix):])
	return subtle.ConstantTimeCompare(s.token, presented) == 1
}

func (s *Server) hostAllowed(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return false
	}
	if _, ok := s.allowedHosts[host]; ok {
		return true
	}
	if h, p, err := net.SplitHostPort(host); err == nil {
		// SplitHostPort is lenient about the port string — anything
		// after the last colon counts. Require a numeric port so that
		// `127.0.0.1:abc` (and similar malformed shapes) fall through
		// to the rejection branch rather than being granted access on
		// the host-part alone.
		if !portLooksNumeric(p) {
			return false
		}
		if _, ok := s.allowedHosts[strings.ToLower(h)]; ok {
			return true
		}
	}
	return false
}

func portLooksNumeric(p string) bool {
	if p == "" {
		return false
	}
	for _, r := range p {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// Listen binds to opts.Addr, refusing non-loopback unless opts.AllowRemote,
// and refusing remote binds without an authentication token. The latter
// is the dashboard analogue of cloudflare-mcp's per-request bearer check:
// once you opt off loopback, you must authenticate.
//
// Returns the net.Listener so tests can grab the chosen port via :0.
func Listen(opts Options) (net.Listener, error) {
	addr := opts.Addr
	if addr == "" {
		addr = "127.0.0.1:18428"
	}
	if !opts.AllowRemote {
		if err := requireLoopback(addr); err != nil {
			return nil, err
		}
	} else if opts.Token == "" {
		return nil, fmt.Errorf("refusing remote bind %q without --token; non-loopback dashboards must require authentication", addr)
	}
	return net.Listen("tcp", addr)
}

func requireLoopback(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("parse addr %q: %w", addr, err)
	}
	if host == "" {
		return fmt.Errorf("refusing wildcard bind %q without --allow-remote; use 127.0.0.1:PORT for loopback", addr)
	}
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return nil
	}
	ip := net.ParseIP(host)
	if ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("refusing non-loopback bind %q; pass --allow-remote to override", addr)
}

func (s *Server) routes() {
	s.mux.HandleFunc("/", s.handleIndex)
	s.mux.Handle("/static/", s.staticHand)
	s.mux.HandleFunc("/api/status", s.jsonGet("status"))
	s.mux.HandleFunc("/api/verify", s.jsonGet("verify"))
	s.mux.HandleFunc("/api/conflicts", s.jsonGet("conflicts"))
	s.mux.HandleFunc("/api/corpus_size", s.jsonGet("corpus_size"))
	s.mux.HandleFunc("/api/doctor", s.jsonGet("doctor"))
	s.mux.HandleFunc("/api/search", s.handleSearch)
	s.mux.HandleFunc("/api/read", s.handleRead)
	s.mux.HandleFunc("/api/clusters", s.handleClusters)
	s.mux.HandleFunc("/api/skill_candidates", s.handleSkillCandidates)
	s.mux.HandleFunc("/api/tools", s.handleToolsList)
	s.mux.HandleFunc("/api/version", s.handleVersion)
}

// indexCSP is intentionally strict: only self-hosted scripts/styles, no
// inline content, no remote loads. The dashboard's app.js and app.css are
// served from /static/ on the same origin so this works as-is.
const indexCSP = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	body, err := indexHTML()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", indexCSP)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Write(body)
}

func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	writeJSON(w, map[string]any{"version": model.Version})
}

func (s *Server) handleToolsList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	writeJSON(w, map[string]any{
		"tools": append([]string(nil), mcp.ToolNames...),
	})
}

// jsonGet builds a handler for the no-argument tools that accept GET.
func (s *Server) jsonGet(toolName string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		out, err := mcp.CallTool(s.backend, toolName, nil)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, out)
	}
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	s.handleJSONPost(w, r, "search")
}

func (s *Server) handleRead(w http.ResponseWriter, r *http.Request) {
	s.handleJSONPost(w, r, "read")
}

func (s *Server) handleClusters(w http.ResponseWriter, r *http.Request) {
	s.handleJSONPost(w, r, "clusters")
}

func (s *Server) handleSkillCandidates(w http.ResponseWriter, r *http.Request) {
	s.handleJSONPost(w, r, "skill_candidates")
}

func (s *Server) handleJSONPost(w http.ResponseWriter, r *http.Request, tool string) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if !isJSONContentType(r.Header.Get("Content-Type")) {
		writeError(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
		return
	}
	args, err := readArgs(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	out, err := mcp.CallTool(s.backend, tool, args)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, out)
}

func isJSONContentType(ct string) bool {
	if ct == "" {
		return false
	}
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	return strings.EqualFold(strings.TrimSpace(ct), "application/json")
}

const maxJSONArgsBytes = 1 << 20

func readArgs(r io.Reader) (json.RawMessage, error) {
	body, err := io.ReadAll(io.LimitReader(r, maxJSONArgsBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxJSONArgsBytes {
		return nil, fmt.Errorf("JSON request body exceeds %d bytes", maxJSONArgsBytes)
	}
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return json.RawMessage("{}"), nil
	}
	return json.RawMessage(body), nil
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		// Headers already written; nothing useful we can do.
		_ = err
	}
}

// errorEnvelope is the shape every HTTP error response uses. It is pinned
// by an HTTP JSON contract test so changing the shape requires updating the
// dashboard's app.js and any external consumers.
type errorEnvelope struct {
	Error errorPayload `json:"error"`
}

type errorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(errorEnvelope{Error: errorPayload{Code: errorCodeForStatus(status), Message: message}})
}

func errorCodeForStatus(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "bad_request"
	case http.StatusUnauthorized:
		return "unauthorized"
	case http.StatusUnsupportedMediaType:
		return "unsupported_media_type"
	case http.StatusMethodNotAllowed:
		return "method_not_allowed"
	case http.StatusMisdirectedRequest:
		return "host_not_permitted"
	case http.StatusRequestTimeout:
		return "timeout"
	}
	return "error"
}

func methodNotAllowed(w http.ResponseWriter, allowed string) {
	w.Header().Set("Allow", allowed)
	writeError(w, http.StatusMethodNotAllowed, "method not allowed")
}

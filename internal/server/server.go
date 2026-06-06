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
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"

	"github.com/adewale/aha/internal/mcp"
	"github.com/adewale/aha/internal/model"
	"github.com/adewale/aha/internal/search"
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
	s.mux.HandleFunc("/api/search_traces", s.handleSearchTraces)
	s.mux.HandleFunc("/api/read", s.handleRead)
	s.mux.HandleFunc("/api/incidents", s.handleIncidents)
	s.mux.HandleFunc("/api/incident_trajectory", s.handleIncidentTrajectory)
	s.mux.HandleFunc("/api/overview", s.jsonGet("overview"))
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

func (s *Server) handleSearchTraces(w http.ResponseWriter, r *http.Request) {
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
	out, err := mcp.CallTool(s.backend, "search", args)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	hits, ok := out.([]search.Result)
	if !ok {
		writeError(w, http.StatusBadRequest, "search returned unexpected result shape")
		return
	}
	traces, err := buildSearchTraces(s.backend.DB(), hits)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, traces)
}

type searchTraceCard struct {
	TraceID           string             `json:"trace_id"`
	SessionKey        string             `json:"session_key,omitempty"`
	Title             string             `json:"title"`
	Subtitle          string             `json:"subtitle"`
	Project           string             `json:"project,omitempty"`
	Source            string             `json:"source,omitempty"`
	Machine           string             `json:"machine,omitempty"`
	Timestamp         string             `json:"timestamp,omitempty"`
	RefText           string             `json:"ref_text"`
	EntryID           string             `json:"entry_id,omitempty"`
	Status            string             `json:"status"`
	StatusClass       string             `json:"status_class"`
	MatchedEventCount int                `json:"matched_event_count"`
	Messages          int                `json:"messages"`
	UserPrompts       int                `json:"user_prompts"`
	ToolCalls         int                `json:"tool_calls"`
	Failures          int                `json:"failures"`
	Commands          []string           `json:"commands"`
	Files             []string           `json:"files"`
	Timeline          []traceTimeline    `json:"timeline"`
	MatchedEvents     []searchTraceEvent `json:"matched_events"`
}

type searchTraceEvent struct {
	Role      string `json:"role"`
	Label     string `json:"label"`
	Timestamp string `json:"timestamp,omitempty"`
	Snippet   string `json:"snippet"`
	RefText   string `json:"ref_text"`
	EntryID   string `json:"entry_id,omitempty"`
}

type traceTimeline struct {
	Role  string `json:"role"`
	Label string `json:"label"`
}

func buildSearchTraces(db *sql.DB, hits []search.Result) ([]searchTraceCard, error) {
	type group struct {
		key  string
		hits []search.Result
	}
	var groups []group
	byKey := map[string]int{}
	for _, h := range hits {
		key := h.SessionKey
		if key == "" {
			key = h.RefText
		}
		if key == "" {
			key = fmt.Sprintf("hit-%d", len(groups))
		}
		idx, ok := byKey[key]
		if !ok {
			idx = len(groups)
			byKey[key] = idx
			groups = append(groups, group{key: key})
		}
		groups[idx].hits = append(groups[idx].hits, h)
	}
	out := make([]searchTraceCard, 0, len(groups))
	for _, g := range groups {
		if len(g.hits) == 0 {
			continue
		}
		card, err := buildSearchTrace(db, g.key, g.hits)
		if err != nil {
			return nil, err
		}
		out = append(out, card)
	}
	if out == nil {
		out = []searchTraceCard{}
	}
	return out, nil
}

func buildSearchTrace(db *sql.DB, key string, hits []search.Result) (searchTraceCard, error) {
	first := hits[0]
	card := searchTraceCard{
		TraceID:           key,
		SessionKey:        first.SessionKey,
		Project:           first.Project,
		Source:            first.Source,
		Machine:           first.Machine,
		Timestamp:         first.Timestamp,
		RefText:           first.RefText,
		EntryID:           first.EntryID,
		MatchedEventCount: len(hits),
		MatchedEvents:     searchTraceEvents(hits, 5),
	}
	if first.SessionKey == "" {
		card.Title = titleFromHits(hits)
		card.Subtitle = compactJoin(" · ", first.Source, first.Machine, dateOnly(first.Timestamp))
		card.Status = statusForTrace(card.Failures, card.ToolCalls, card.Files, hits)
		card.StatusClass = statusClass(card.Status)
		return card, nil
	}
	if err := enrichSessionTrace(db, &card, hits); err != nil {
		return searchTraceCard{}, err
	}
	return card, nil
}

func enrichSessionTrace(db *sql.DB, card *searchTraceCard, hits []search.Result) error {
	var rawCWD, source, machine, started string
	err := db.QueryRow(`select coalesce(raw_cwd,''),source_name,machine_id,coalesce(started_at,'') from sessions where session_key=?`, card.SessionKey).Scan(&rawCWD, &source, &machine, &started)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if card.Project == "" {
		card.Project = rawCWD
	}
	if card.Source == "" {
		card.Source = source
	}
	if card.Machine == "" {
		card.Machine = machine
	}
	if card.Timestamp == "" {
		card.Timestamp = started
	}
	if err := db.QueryRow(`select count(*),coalesce(sum(case when role='user' then 1 else 0 end),0) from messages where session_key=?`, card.SessionKey).Scan(&card.Messages, &card.UserPrompts); err != nil {
		return err
	}
	if err := db.QueryRow(`select count(*),coalesce(sum(case when is_error<>0 then 1 else 0 end),0) from tool_invocations where session_key=?`, card.SessionKey).Scan(&card.ToolCalls, &card.Failures); err != nil {
		return err
	}
	card.Commands = queryStrings(db, `select distinct coalesce(nullif(command_family,''),nullif(tool_name,''),'') from tool_invocations where session_key=? and coalesce(nullif(command_family,''),nullif(tool_name,''),'')<>'' order by 1 limit 6`, card.SessionKey)
	card.Files = queryStrings(db, `select distinct coalesce(nullif(relative_path,''),nullif(raw_path,''),'') from artifacts where parent_session_key=? and coalesce(nullif(relative_path,''),nullif(raw_path,''),'')<>'' order by 1 limit 6`, card.SessionKey)
	card.Timeline = queryTimeline(db, card.SessionKey)
	card.Title = titleForSession(db, card.SessionKey, hits)
	card.Subtitle = compactJoin(" · ", card.Project, card.Source, card.Machine, dateOnly(card.Timestamp))
	card.Status = statusForTrace(card.Failures, card.ToolCalls, card.Files, hits)
	card.StatusClass = statusClass(card.Status)
	return nil
}

func searchTraceEvents(hits []search.Result, limit int) []searchTraceEvent {
	if len(hits) < limit {
		limit = len(hits)
	}
	events := make([]searchTraceEvent, 0, limit)
	for _, h := range hits[:limit] {
		events = append(events, searchTraceEvent{Role: h.Role, Label: labelForRole(h.Role), Timestamp: h.Timestamp, Snippet: cleanSnippet(h.Snippet), RefText: h.RefText, EntryID: h.EntryID})
	}
	return events
}

func queryTimeline(db *sql.DB, sessionKey string) []traceTimeline {
	rows, err := db.Query(`select role from messages m join entries e on e.session_key=m.session_key and e.entry_id=m.entry_id where m.session_key=? order by e.line_no,e.entry_id limit 18`, sessionKey)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []traceTimeline
	for rows.Next() {
		var role string
		if err := rows.Scan(&role); err == nil {
			out = append(out, traceTimeline{Role: role, Label: labelForRole(role)})
		}
	}
	return out
}

func queryStrings(db *sql.DB, sqlText, arg string) []string {
	rows, err := db.Query(sqlText, arg)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err == nil && strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	return out
}

func titleForSession(db *sql.DB, sessionKey string, hits []search.Result) string {
	if t := titleFromHits(hits); t != "" {
		return t
	}
	var text string
	_ = db.QueryRow(`select coalesce(m.text,'') from messages m join entries e on e.session_key=m.session_key and e.entry_id=m.entry_id where m.session_key=? and m.role='user' and trim(coalesce(m.text,''))<>'' order by e.line_no,e.entry_id limit 1`, sessionKey).Scan(&text)
	if text = cleanSnippet(text); text != "" {
		return truncate(text, 140)
	}
	return "Untitled trace"
}

func titleFromHits(hits []search.Result) string {
	for _, h := range hits {
		if h.Role == "user" {
			if t := cleanSnippet(h.Snippet); t != "" {
				return truncate(t, 140)
			}
		}
	}
	for _, h := range hits {
		if t := cleanSnippet(h.Snippet); t != "" {
			return truncate(t, 140)
		}
	}
	return ""
}

func statusForTrace(failures, toolCalls int, files []string, hits []search.Result) string {
	if failures > 0 || hitsContainFailure(hits) {
		return "failed tool"
	}
	if toolCalls > 0 {
		return "tool work"
	}
	if len(files) > 0 || hitsContainRole(hits, "artifact") {
		return "file match"
	}
	return "conversation"
}

func statusClass(status string) string {
	switch status {
	case "failed tool":
		return "status-failure"
	case "tool work":
		return "status-tool"
	case "file match":
		return "status-file"
	default:
		return "status-conversation"
	}
}

func hitsContainFailure(hits []search.Result) bool {
	for _, h := range hits {
		text := strings.ToLower(h.Snippet)
		if strings.Contains(text, "error") || strings.Contains(text, "failed") || strings.Contains(text, "failure") || strings.Contains(text, "panic") || strings.Contains(text, "exception") || strings.Contains(text, "denied") {
			return true
		}
	}
	return false
}

func hitsContainRole(hits []search.Result, role string) bool {
	for _, h := range hits {
		if h.Role == role {
			return true
		}
	}
	return false
}

func labelForRole(role string) string {
	switch role {
	case "user":
		return "Prompt"
	case "assistant":
		return "Assistant"
	case "toolResult":
		return "Tool output"
	case "artifact":
		return "File artifact"
	default:
		if role == "" {
			return "Event"
		}
		return role
	}
}

func cleanSnippet(s string) string {
	s = strings.ReplaceAll(s, "[", "")
	s = strings.ReplaceAll(s, "]", "")
	return strings.Join(strings.Fields(s), " ")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.TrimSpace(s[:n]) + "…"
}

func dateOnly(ts string) string {
	if len(ts) >= 10 {
		return ts[:10]
	}
	return ts
}

func compactJoin(sep string, parts ...string) string {
	var out []string
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, sep)
}

func (s *Server) handleRead(w http.ResponseWriter, r *http.Request) {
	s.handleJSONPost(w, r, "read")
}

func (s *Server) handleIncidents(w http.ResponseWriter, r *http.Request) {
	s.handleJSONPost(w, r, "incidents")
}

func (s *Server) handleIncidentTrajectory(w http.ResponseWriter, r *http.Request) {
	s.handleJSONPost(w, r, "incident_trajectory")
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

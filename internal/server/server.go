// Package server exposes the read-only aha tool surface over HTTP and serves
// a minimal loopback dashboard. The HTTP routes share their implementations
// with the MCP server: both go through the same underlying corpus/search
// functions, so output shapes stay in lockstep.
//
// Security posture mirrors tracebase's: 127.0.0.1 binding by default; remote
// binds require an explicit opt-in (--allow-remote or AHA_ALLOW_REMOTE=1);
// every route is read-only and CORS-free.
package server

import (
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
	Addr        string // host:port; default "127.0.0.1:18428"
	AllowRemote bool   // allow non-loopback bind
}

// Server is the embedded HTTP handler. Tests can drive its ServeHTTP directly
// or stand it up via Listen.
type Server struct {
	backend mcp.Backend
	mux     *http.ServeMux
}

// New wires up routes against the given read-only backend.
func New(backend mcp.Backend) *Server {
	s := &Server{backend: backend, mux: http.NewServeMux()}
	s.routes()
	return s
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// Listen binds to opts.Addr, refusing non-loopback unless opts.AllowRemote.
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
	}
	return net.Listen("tcp", addr)
}

func requireLoopback(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("parse addr %q: %w", addr, err)
	}
	if host == "" || host == "localhost" || host == "127.0.0.1" || host == "::1" {
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
	s.mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS()))))
	s.mux.HandleFunc("/api/status", s.jsonGet("status"))
	s.mux.HandleFunc("/api/verify", s.jsonGet("verify"))
	s.mux.HandleFunc("/api/conflicts", s.jsonGet("conflicts"))
	s.mux.HandleFunc("/api/corpus_size", s.jsonGet("corpus_size"))
	s.mux.HandleFunc("/api/doctor", s.jsonGet("doctor"))
	s.mux.HandleFunc("/api/search", s.handleSearch)
	s.mux.HandleFunc("/api/read", s.handleRead)
	s.mux.HandleFunc("/api/tools", s.handleToolsList)
	s.mux.HandleFunc("/api/version", s.handleVersion)
}

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
	w.Write(body)
}

func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"version": model.Version})
}

func (s *Server) handleToolsList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{
		"tools": []string{"search", "read", "status", "verify", "conflicts", "corpus_size", "doctor"},
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
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
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
	writeJSON(w, out)
}

func (s *Server) handleRead(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	args, err := readArgs(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	out, err := mcp.CallTool(s.backend, "read", args)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, out)
}

func readArgs(r io.Reader) (json.RawMessage, error) {
	body, err := io.ReadAll(io.LimitReader(r, 1<<20))
	if err != nil {
		return nil, err
	}
	body = []byte(strings.TrimSpace(string(body)))
	if len(body) == 0 {
		return json.RawMessage("{}"), nil
	}
	return json.RawMessage(body), nil
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		// Headers already written; nothing useful we can do.
		_ = err
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"message": message}})
}

func methodNotAllowed(w http.ResponseWriter, allowed string) {
	w.Header().Set("Allow", allowed)
	writeError(w, http.StatusMethodNotAllowed, "method not allowed")
}

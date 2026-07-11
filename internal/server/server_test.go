package server_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/adewale/aha/internal/cli"
	"github.com/adewale/aha/internal/corpus"
	"github.com/adewale/aha/internal/mcp"
	"github.com/adewale/aha/internal/model"
	"github.com/adewale/aha/internal/server"
	"github.com/adewale/aha/internal/testutil"
)

func buildCorpus(t *testing.T) (*corpus.Store, model.Config) {
	t.Helper()
	root := t.TempDir()
	fx := testutil.WriteAgentFixtures(t, root)
	outDir := filepath.Join(root, "bundles")
	corpusDir := filepath.Join(root, "corpus")
	configPath := filepath.Join(root, "config.jsonc")
	cfg := `{
		"machine_id":"server-test",
		"sources":[
			{"type":"pi","root":"` + filepath.ToSlash(fx.PiRoot) + `","enabled":true},
			{"type":"claude-code","root":"` + filepath.ToSlash(fx.ClaudeRoot) + `","enabled":true}
		],
		"corpus_dir":"` + filepath.ToSlash(corpusDir) + `",
		"depot":{"type":"local","location":"` + filepath.ToSlash(outDir) + `"},
		"accept_secrets_warning":true
	}`
	if err := os.WriteFile(configPath, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := cli.Run([]string{"refresh", "--config", configPath, "--captured-at", "2026-01-03T00:00:00Z"}, io.Discard, io.Discard); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	store, err := corpus.OpenExisting(corpusDir)
	if err != nil {
		t.Fatalf("OpenExisting: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store, model.Config{CorpusDir: corpusDir, MachineID: "server-test"}
}

func newTestServer(t *testing.T) *server.Server {
	t.Helper()
	store, cfg := buildCorpus(t)
	return server.New(mcp.NewCorpusBackend(store, cfg))
}

// loopback rewrites a httptest.NewRequest's default Host (example.com) to a
// loopback variant so the server's Host allowlist permits the call. Tests
// that want to exercise the allowlist itself bypass this and set req.Host
// directly.
func loopback(req *http.Request) *http.Request {
	req.Host = "127.0.0.1"
	return req
}

func TestIndexServesHTML(t *testing.T) {
	srv := newTestServer(t)
	w := httptest.NewRecorder()
	req := loopback(httptest.NewRequest(http.MethodGet, "/", nil))
	srv.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("index status=%d", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{"<title>aha</title>", "/static/app.js"} {
		if !strings.Contains(body, want) {
			t.Fatalf("index missing %q: %s", want, body)
		}
	}
}

func TestDashboardIsSearchFirstTraceBrowser(t *testing.T) {
	srv := newTestServer(t)
	w := httptest.NewRecorder()
	req := loopback(httptest.NewRequest(http.MethodGet, "/", nil))
	srv.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("index status=%d", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{"Search", "Prompts first", "Failures", "Most frequent", "Sources", "Data &amp; trust", "Search agent history", "schema migration sqlite failure", "Conversation", "Trace", "Evidence", "Traces", "Search in", "Prompts", "Tool output", "Advanced filters", "Clear scope", "Copy ref", "Widen context", "aria-live", "aria-pressed", "Sources &amp; scope", "Trust checks"} {
		if !strings.Contains(body, want) {
			t.Fatalf("dashboard copy missing search-first trace label %q:\n%s", want, body)
		}
	}
	for _, stale := range []string{"Start with a task", "Find prompts you typed", "Browse work history", "select a result or cluster", "<h2>corpus</h2>", "<h2>read</h2>", "<h2>conflicts</h2>"} {
		if strings.Contains(body, stale) {
			t.Fatalf("dashboard copy still exposes stale/confused label %q:\n%s", stale, body)
		}
	}

	w = httptest.NewRecorder()
	req = loopback(httptest.NewRequest(http.MethodGet, "/static/app.js", nil))
	srv.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("app.js status=%d", w.Code)
	}
	js := w.Body.String()
	for _, want := range []string{"/api/search_traces", "search matching history", "copy fix notes", "Fix notes", "renderTraceCards", "trace-card", "trace-timeline", "trace-fact", "renderReadEntry", "renderFailureSummary", "Most frequent", "updateReaderContext", "widen-context", "example-search", "sources-tab-status", "setActiveTab", "incident-summary-status", "overview-summary-status", "conflicts-summary-status", "setSearchFeedback", "updateScopeSummary", "runSearchIfQuery", `role: $("role").value.trim()`} {
		if !strings.Contains(js, want) {
			t.Fatalf("dashboard behavior copy missing %q:\n%s", want, js)
		}
	}
	for _, stale := range []string{"copy skill draft", "applyJourney", "journey", "# ", "cluster"} {
		if strings.Contains(js, stale) {
			t.Fatalf("dashboard behavior copy still contains stale term %q", stale)
		}
	}

	w = httptest.NewRecorder()
	req = loopback(httptest.NewRequest(http.MethodGet, "/static/app.css", nil))
	srv.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("app.css status=%d", w.Code)
	}
	css := w.Body.String()
	for _, want := range []string{"contain: paint", "overflow: hidden", "overflow-wrap: anywhere"} {
		if !strings.Contains(css, want) {
			t.Fatalf("dashboard CSS missing card containment rule %q", want)
		}
	}
	for _, stale := range []string{"border-left", "border-right", "#fff", "#000", "counter-reset", "counter-increment", "decimal-leading-zero", ".tab::before", ".trace-card::after"} {
		if strings.Contains(css, stale) {
			t.Fatalf("dashboard CSS still contains slop-prone token %q", stale)
		}
	}
}

func TestStaticAssetsAreEmbedded(t *testing.T) {
	srv := newTestServer(t)
	for _, path := range []string{"/static/app.js", "/static/app.css"} {
		w := httptest.NewRecorder()
		req := loopback(httptest.NewRequest(http.MethodGet, path, nil))
		srv.ServeHTTP(w, req)
		if w.Code != 200 {
			t.Fatalf("%s status=%d", path, w.Code)
		}
		if w.Body.Len() == 0 {
			t.Fatalf("%s returned empty body", path)
		}
	}
}

func TestStatusEndpointReturnsCorpusShape(t *testing.T) {
	srv := newTestServer(t)
	w := httptest.NewRecorder()
	req := loopback(httptest.NewRequest(http.MethodGet, "/api/status", nil))
	srv.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v\n%s", err, w.Body.String())
	}
	for _, key := range []string{"corpus_dir", "sessions", "entries", "fts_messages"} {
		if _, ok := got[key]; !ok {
			t.Fatalf("status missing %q: %v", key, got)
		}
	}
}

func TestSearchEndpointAcceptsPOSTAndChainsToRead(t *testing.T) {
	srv := newTestServer(t)
	w := httptest.NewRecorder()
	req := loopback(httptest.NewRequest(http.MethodPost, "/api/search",
		strings.NewReader(`{"query":"needle","limit":5}`)))
	req.Header.Set("Content-Type", "application/json")
	srv.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("search status=%d body=%s", w.Code, w.Body.String())
	}
	var hits []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &hits); err != nil {
		t.Fatalf("decode hits: %v", err)
	}
	if len(hits) == 0 {
		t.Fatalf("search returned no hits: %s", w.Body.String())
	}
	refText, _ := hits[0]["ref_text"].(string)
	if refText == "" {
		t.Fatalf("first hit missing ref_text: %v", hits[0])
	}
	readBody, _ := json.Marshal(map[string]any{"ref": refText, "before": 1, "after": 3})
	w = httptest.NewRecorder()
	req = loopback(httptest.NewRequest(http.MethodPost, "/api/read", bytes.NewReader(readBody)))
	req.Header.Set("Content-Type", "application/json")
	srv.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("read status=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"entry_id"`) {
		t.Fatalf("read response missing entry_id: %s", w.Body.String())
	}
}

func TestSearchTracesEndpointReturnsRecognizableCards(t *testing.T) {
	srv := newTestServer(t)
	w := httptest.NewRecorder()
	req := loopback(httptest.NewRequest(http.MethodPost, "/api/search_traces",
		strings.NewReader(`{"query":"needle","limit":20}`)))
	req.Header.Set("Content-Type", "application/json")
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("search_traces status=%d body=%s", w.Code, w.Body.String())
	}
	var traces []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &traces); err != nil {
		t.Fatalf("decode traces: %v\n%s", err, w.Body.String())
	}
	if len(traces) == 0 {
		t.Fatalf("search_traces returned no cards: %s", w.Body.String())
	}
	first := traces[0]
	for _, key := range []string{"title", "subtitle", "status", "ref_text", "matched_events", "timeline"} {
		if _, ok := first[key]; !ok {
			t.Fatalf("first trace missing %q: %#v", key, first)
		}
	}
	if events, ok := first["matched_events"].([]any); !ok || len(events) == 0 {
		t.Fatalf("first trace missing matched events: %#v", first)
	}
	var sawToolFailure bool
	for _, tr := range traces {
		tools, _ := tr["tool_calls"].(float64)
		failures, _ := tr["failures"].(float64)
		commands, _ := tr["commands"].([]any)
		title, _ := tr["title"].(string)
		if tools > 0 && failures > 0 && len(commands) > 0 && strings.Contains(title, "claude needle") {
			sawToolFailure = true
		}
	}
	if !sawToolFailure {
		t.Fatalf("search_traces should enrich cards with tool/failure/command evidence: %s", w.Body.String())
	}
}

func TestSearchEndpointRejectsBadArgs(t *testing.T) {
	srv := newTestServer(t)
	w := httptest.NewRecorder()
	req := loopback(httptest.NewRequest(http.MethodPost, "/api/search",
		strings.NewReader(`{"query":"x","bogus":1}`)))
	req.Header.Set("Content-Type", "application/json")
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestJSONPostRejectsTrailingJSON(t *testing.T) {
	srv := newTestServer(t)
	w := httptest.NewRecorder()
	req := loopback(httptest.NewRequest(http.MethodPost, "/api/search",
		strings.NewReader(`{"query":"needle"}{"bogus":1}`)))
	req.Header.Set("Content-Type", "application/json")
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for trailing JSON, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestJSONPostRejectsOversizedBody(t *testing.T) {
	srv := newTestServer(t)
	w := httptest.NewRecorder()
	body := `{"query":"needle"}` + strings.Repeat(" ", 1<<20)
	req := loopback(httptest.NewRequest(http.MethodPost, "/api/search", strings.NewReader(body)))
	req.Header.Set("Content-Type", "application/json")
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for oversized body, got %d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "exceeds") {
		t.Fatalf("oversized response should explain the limit: %s", w.Body.String())
	}
}

func TestIncidentsEndpointAcceptsPOST(t *testing.T) {
	srv := newTestServer(t)
	w := httptest.NewRecorder()
	req := loopback(httptest.NewRequest(http.MethodPost, "/api/incidents", strings.NewReader(`{"limit":5,"state":"unresolved"}`)))
	req.Header.Set("Content-Type", "application/json")
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("incidents status=%d body=%s", w.Code, w.Body.String())
	}
	var incidents []corpus.Incident
	if err := json.Unmarshal(w.Body.Bytes(), &incidents); err != nil {
		t.Fatalf("decode incidents: %v\n%s", err, w.Body.String())
	}
	if incidents == nil {
		t.Fatalf("incidents response must be [] not null: %s", w.Body.String())
	}
}

func TestOverviewEndpointReturnsComposition(t *testing.T) {
	srv := newTestServer(t)
	w := httptest.NewRecorder()
	req := loopback(httptest.NewRequest(http.MethodGet, "/api/overview", nil))
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("overview status=%d body=%s", w.Code, w.Body.String())
	}
	var o corpus.Overview
	if err := json.Unmarshal(w.Body.Bytes(), &o); err != nil {
		t.Fatalf("decode overview: %v\n%s", err, w.Body.String())
	}
	if o.Sessions <= 0 {
		t.Fatalf("overview should report sessions from the fixture corpus: %+v", o)
	}
}

func TestIncidentTrajectoryEndpointValidatesRef(t *testing.T) {
	srv := newTestServer(t)
	w := httptest.NewRecorder()
	req := loopback(httptest.NewRequest(http.MethodPost, "/api/incident_trajectory", strings.NewReader(`{"ref":"session:v1:abc"}`)))
	req.Header.Set("Content-Type", "application/json")
	srv.ServeHTTP(w, req)
	// A non-message ref is a clean error, not a crash.
	if w.Code == http.StatusInternalServerError {
		t.Fatalf("trajectory should reject a non-message ref gracefully, got 500: %s", w.Body.String())
	}
}

func TestSearchEndpointRejectsGET(t *testing.T) {
	srv := newTestServer(t)
	w := httptest.NewRecorder()
	req := loopback(httptest.NewRequest(http.MethodGet, "/api/search", nil))
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

func TestSearchEndpointRequiresJSONContentType(t *testing.T) {
	srv := newTestServer(t)
	for _, ct := range []string{"", "text/plain", "application/x-www-form-urlencoded", "multipart/form-data; boundary=x"} {
		w := httptest.NewRecorder()
		req := loopback(httptest.NewRequest(http.MethodPost, "/api/search",
			strings.NewReader(`{"query":"needle"}`)))
		if ct != "" {
			req.Header.Set("Content-Type", ct)
		}
		srv.ServeHTTP(w, req)
		if w.Code != http.StatusUnsupportedMediaType {
			t.Fatalf("Content-Type=%q expected 415, got %d body=%s", ct, w.Code, w.Body.String())
		}
		assertErrorEnvelope(t, w.Body.Bytes(), "unsupported_media_type")
	}
}

func TestSearchEndpointAcceptsJSONWithCharsetParam(t *testing.T) {
	srv := newTestServer(t)
	w := httptest.NewRecorder()
	req := loopback(httptest.NewRequest(http.MethodPost, "/api/search",
		strings.NewReader(`{"query":"needle","limit":5}`)))
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	srv.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestHostHeaderAllowlistRejectsForeignHosts(t *testing.T) {
	srv := newTestServer(t)
	// Borrowed from @hono/mcp's DNS-rebind protection test matrix: real
	// rebinding attacks present hostnames that match no entry in our
	// allowlist (public DNS that resolves to 127.0.0.1). Each must yield
	// 421 with the pinned error code so downstream consumers can react.
	cases := []string{
		"evil.example.com",
		"evil.example.com:1234",
		"10.0.0.1",
		"10.0.0.1:18428",
		"192.168.1.50",
		"169.254.169.254", // IMDS — common SSRF target
		"169.254.169.254:80",
		"aha.attacker.example", // CNAME-rebound name
		"",                     // missing/empty Host header
		"  ",                   // whitespace-only
	}
	for _, host := range cases {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
		req.Host = host
		srv.ServeHTTP(w, req)
		if w.Code != http.StatusMisdirectedRequest {
			t.Fatalf("Host=%q expected 421, got %d body=%s", host, w.Code, w.Body.String())
		}
		assertErrorEnvelope(t, w.Body.Bytes(), "host_not_permitted")
	}
}

// IDN homograph hostnames (Cyrillic 'а' that *looks* like ASCII 'a', etc.)
// are real-world DNS-rebind vectors. Go's `strings.ToLower` does not
// fold them to ASCII, so the allowlist comparison correctly rejects
// them — but the test documents that behaviour rather than leaving it
// implicit. If a future refactor switches to `cases.Fold`/Unicode-aware
// normalization, that change must update this test too.
func TestHostHeaderAllowlistRejectsIDNHomographs(t *testing.T) {
	srv := newTestServer(t)
	// "lоcalhost" with a Cyrillic 'о' (U+043E). Mistaken for ASCII at a
	// glance but a distinct codepoint; must not match "localhost".
	for _, host := range []string{
		"lоcalhost",        // Cyrillic 'о'
		"localhost​",       // trailing zero-width space
		"localhоst",        // mid-name Cyrillic 'о'
		"xn--lcalhost-tdh", // IDN punycode that doesn't match
	} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
		req.Host = host
		srv.ServeHTTP(w, req)
		if w.Code != http.StatusMisdirectedRequest {
			t.Fatalf("Host=%q (homograph) expected 421, got %d", host, w.Code)
		}
	}
}

// Malformed Host header shapes that look superficially loopback-ish must
// still be rejected. Tests the rejection path of `net.SplitHostPort` on
// invalid IPv6 brackets and missing brackets.
func TestHostHeaderAllowlistRejectsMalformedBrackets(t *testing.T) {
	srv := newTestServer(t)
	for _, host := range []string{
		"[::1:8080",       // missing closing bracket
		"::1]:8080",       // missing opening bracket
		"[::1]extra:8080", // garbage after the IPv6 literal
		"127.0.0.1:abc",   // non-numeric port
		":18428",          // leading colon, missing host
	} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
		req.Host = host
		srv.ServeHTTP(w, req)
		if w.Code != http.StatusMisdirectedRequest {
			t.Fatalf("Host=%q (malformed) expected 421, got %d", host, w.Code)
		}
	}
}

func TestHostHeaderAllowlistAcceptsLoopbackVariants(t *testing.T) {
	srv := newTestServer(t)
	for _, host := range []string{"localhost", "localhost:18428", "127.0.0.1", "127.0.0.1:18428", "[::1]:18428"} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
		req.Host = host
		srv.ServeHTTP(w, req)
		if w.Code != 200 {
			t.Fatalf("Host=%q expected 200, got %d body=%s", host, w.Code, w.Body.String())
		}
	}
}

func TestHostHeaderAllowlistExtendedByOption(t *testing.T) {
	store, cfg := buildCorpus(t)
	srv := server.NewWithOptions(mcp.NewCorpusBackend(store, cfg), server.Options{
		Addr:         "10.0.0.5:18428",
		AllowRemote:  true,
		AllowedHosts: []string{"aha.lan"},
	})
	for _, host := range []string{"aha.lan", "aha.lan:18428", "10.0.0.5", "10.0.0.5:18428"} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
		req.Host = host
		srv.ServeHTTP(w, req)
		if w.Code != 200 {
			t.Fatalf("Host=%q expected 200, got %d body=%s", host, w.Code, w.Body.String())
		}
	}
}

func TestIndexCarriesContentSecurityPolicy(t *testing.T) {
	srv := newTestServer(t)
	w := httptest.NewRecorder()
	req := loopback(httptest.NewRequest(http.MethodGet, "/", nil))
	srv.ServeHTTP(w, req)
	csp := w.Header().Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'none'", "script-src 'self'", "style-src 'self'", "frame-ancestors 'none'"} {
		if !strings.Contains(csp, want) {
			t.Fatalf("CSP missing %q: %s", want, csp)
		}
	}
	if w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("missing X-Content-Type-Options nosniff")
	}
}

func TestHTTPErrorEnvelopeIsPinned(t *testing.T) {
	srv := newTestServer(t)
	// Drive every error path and assert the envelope shape is identical.
	cases := []struct {
		name string
		req  func() *http.Request
		want string // expected error code
	}{
		{
			"unknown route uses the shared envelope",
			func() *http.Request { return loopback(httptest.NewRequest(http.MethodGet, "/api/no-such", nil)) },
			"not_found",
		},
		{
			"wrong method on GET endpoint",
			func() *http.Request {
				return loopback(httptest.NewRequest(http.MethodPost, "/api/status", strings.NewReader("{}")))
			},
			"method_not_allowed",
		},
		{
			"missing required arg",
			func() *http.Request {
				r := loopback(httptest.NewRequest(http.MethodPost, "/api/search", strings.NewReader(`{}`)))
				r.Header.Set("Content-Type", "application/json")
				return r
			},
			"bad_request",
		},
		{
			"bad content type",
			func() *http.Request {
				return loopback(httptest.NewRequest(http.MethodPost, "/api/search", strings.NewReader(`{"query":"x"}`)))
			},
			"unsupported_media_type",
		},
		{
			"foreign host header",
			func() *http.Request {
				r := httptest.NewRequest(http.MethodGet, "/api/status", nil)
				r.Host = "evil.example.com"
				return r
			},
			"host_not_permitted",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			srv.ServeHTTP(w, c.req())
			if c.want == "" {
				return
			}
			assertErrorEnvelope(t, w.Body.Bytes(), c.want)
		})
	}
}

func assertErrorEnvelope(t *testing.T, body []byte, wantCode string) {
	t.Helper()
	var got struct {
		Error struct {
			Code       string   `json:"code"`
			Message    string   `json:"message"`
			Next       []string `json:"next"`
			NextAction struct {
				Command string   `json:"command"`
				Args    []string `json:"args"`
			} `json:"next_action"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("body is not the error envelope: %v\n%s", err, body)
	}
	if got.Error.Code != wantCode {
		t.Fatalf("error.code = %q, want %q (body=%s)", got.Error.Code, wantCode, body)
	}
	if got.Error.Message == "" {
		t.Fatalf("error.message empty: %s", body)
	}
	if len(got.Error.Next) != 1 || got.Error.NextAction.Command == "" {
		t.Fatalf("error must contain exactly one structured action: %s", body)
	}
}

func TestToolsEndpointAdvertisesReadOnlySet(t *testing.T) {
	srv := newTestServer(t)
	w := httptest.NewRecorder()
	req := loopback(httptest.NewRequest(http.MethodGet, "/api/tools", nil))
	srv.ServeHTTP(w, req)
	var got struct {
		Tools []string `json:"tools"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Tools, mcp.ToolNames) {
		t.Fatalf("tools mismatch:\n  got:  %v\n  want: %v", got.Tools, mcp.ToolNames)
	}
}

func TestListenRefusesNonLoopbackByDefault(t *testing.T) {
	_, err := server.Listen(server.Options{Addr: "0.0.0.0:0"})
	if err == nil {
		t.Fatalf("expected refuse non-loopback bind, got nil")
	}
	if !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("expected loopback error, got %v", err)
	}
}

func TestListenRefusesWildcardByDefault(t *testing.T) {
	_, err := server.Listen(server.Options{Addr: ":0"})
	if err == nil {
		t.Fatalf("expected refuse wildcard bind, got nil")
	}
	if !strings.Contains(err.Error(), "wildcard") && !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("expected wildcard/loopback error, got %v", err)
	}
}

func TestListenAllowsLoopbackByDefault(t *testing.T) {
	l, err := server.Listen(server.Options{Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatalf("loopback listen: %v", err)
	}
	defer l.Close()
}

func TestListenAllowsRemoteWithFlag(t *testing.T) {
	l, err := server.Listen(server.Options{Addr: "0.0.0.0:0", AllowRemote: true, Token: "deadbeef"})
	if err != nil {
		t.Fatalf("listen with allow-remote: %v", err)
	}
	defer l.Close()
}

func TestListenRefusesRemoteWithoutToken(t *testing.T) {
	_, err := server.Listen(server.Options{Addr: "0.0.0.0:0", AllowRemote: true})
	if err == nil {
		t.Fatal("expected error binding remote without token")
	}
	if !strings.Contains(err.Error(), "without --token") {
		t.Fatalf("expected token-required error, got %v", err)
	}
}

func TestTokenAuthAcceptsValidBearer(t *testing.T) {
	store, cfg := buildCorpus(t)
	srv := server.NewWithOptions(mcp.NewCorpusBackend(store, cfg), server.Options{Token: "s3cret"})
	w := httptest.NewRecorder()
	req := loopback(httptest.NewRequest(http.MethodGet, "/api/status", nil))
	req.Header.Set("Authorization", "Bearer s3cret")
	srv.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200 with valid token, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestTokenAuthRejectsMissingHeader(t *testing.T) {
	store, cfg := buildCorpus(t)
	srv := server.NewWithOptions(mcp.NewCorpusBackend(store, cfg), server.Options{Token: "s3cret"})
	w := httptest.NewRecorder()
	req := loopback(httptest.NewRequest(http.MethodGet, "/api/status", nil))
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Header().Get("WWW-Authenticate"), "Bearer") {
		t.Fatalf("missing WWW-Authenticate: %q", w.Header().Get("WWW-Authenticate"))
	}
	assertErrorEnvelope(t, w.Body.Bytes(), "unauthorized")
}

func TestTokenAuthRejectsWrongToken(t *testing.T) {
	store, cfg := buildCorpus(t)
	srv := server.NewWithOptions(mcp.NewCorpusBackend(store, cfg), server.Options{Token: "s3cret"})
	w := httptest.NewRecorder()
	req := loopback(httptest.NewRequest(http.MethodGet, "/api/status", nil))
	req.Header.Set("Authorization", "Bearer wrong")
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestTokenAuthRejectsBasicAuth(t *testing.T) {
	store, cfg := buildCorpus(t)
	srv := server.NewWithOptions(mcp.NewCorpusBackend(store, cfg), server.Options{Token: "s3cret"})
	w := httptest.NewRecorder()
	req := loopback(httptest.NewRequest(http.MethodGet, "/api/status", nil))
	req.Header.Set("Authorization", "Basic c2VjcmV0OnNlY3JldA==")
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for non-Bearer scheme, got %d", w.Code)
	}
}

// The Authorization scheme name is technically case-insensitive per RFC
// 7235, but every real MCP/coding-agent client emits "Bearer" with a
// capital B. We enforce the canonical form strictly so a typo
// (`bearer s3cret`) cannot accidentally authenticate.
func TestTokenAuthRejectsLowercaseScheme(t *testing.T) {
	store, cfg := buildCorpus(t)
	srv := server.NewWithOptions(mcp.NewCorpusBackend(store, cfg), server.Options{Token: "s3cret"})
	w := httptest.NewRecorder()
	req := loopback(httptest.NewRequest(http.MethodGet, "/api/status", nil))
	req.Header.Set("Authorization", "bearer s3cret")
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on lowercase scheme, got %d", w.Code)
	}
}

// http.Header.Get returns only the first value. A client sending two
// Authorization headers (rare, but seen with broken proxies) gets the
// first read; we just need to confirm fail-closed when the first one
// is wrong even if a later one happens to match.
func TestTokenAuthOnlyHonoursFirstAuthorizationHeader(t *testing.T) {
	store, cfg := buildCorpus(t)
	srv := server.NewWithOptions(mcp.NewCorpusBackend(store, cfg), server.Options{Token: "s3cret"})
	w := httptest.NewRecorder()
	req := loopback(httptest.NewRequest(http.MethodGet, "/api/status", nil))
	req.Header.Add("Authorization", "Bearer wrong")
	req.Header.Add("Authorization", "Bearer s3cret")
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 when first Authorization is wrong, got %d", w.Code)
	}
}

func TestTokenAuthDisabledWhenEmpty(t *testing.T) {
	store, cfg := buildCorpus(t)
	srv := server.NewWithOptions(mcp.NewCorpusBackend(store, cfg), server.Options{Token: ""})
	w := httptest.NewRecorder()
	req := loopback(httptest.NewRequest(http.MethodGet, "/api/status", nil))
	srv.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200 without token configured, got %d", w.Code)
	}
}

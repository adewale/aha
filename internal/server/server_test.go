package server_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	if err := cli.Run([]string{"refresh", "--config", configPath, "--captured-at", "2026-01-03T00:00:00Z", "--bundle-id", "server"}, io.Discard, io.Discard); err != nil {
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

func TestIndexServesHTML(t *testing.T) {
	srv := newTestServer(t)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
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

func TestStaticAssetsAreEmbedded(t *testing.T) {
	srv := newTestServer(t)
	for _, path := range []string{"/static/app.js", "/static/app.css"} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
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
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
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
	req := httptest.NewRequest(http.MethodPost, "/api/search",
		strings.NewReader(`{"query":"needle","limit":5}`))
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
	req = httptest.NewRequest(http.MethodPost, "/api/read", bytes.NewReader(readBody))
	req.Header.Set("Content-Type", "application/json")
	srv.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("read status=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"entry_id"`) {
		t.Fatalf("read response missing entry_id: %s", w.Body.String())
	}
}

func TestSearchEndpointRejectsBadArgs(t *testing.T) {
	srv := newTestServer(t)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/search",
		strings.NewReader(`{"query":"x","bogus":1}`))
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestSearchEndpointRejectsGET(t *testing.T) {
	srv := newTestServer(t)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/search", nil)
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

func TestToolsEndpointAdvertisesReadOnlySet(t *testing.T) {
	srv := newTestServer(t)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/tools", nil)
	srv.ServeHTTP(w, req)
	var got struct {
		Tools []string `json:"tools"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	wantSet := map[string]bool{}
	for _, n := range []string{"search", "read", "status", "verify", "conflicts", "corpus_size", "doctor"} {
		wantSet[n] = true
	}
	if len(got.Tools) != len(wantSet) {
		t.Fatalf("tools count=%d want=%d (%v)", len(got.Tools), len(wantSet), got.Tools)
	}
	for _, n := range got.Tools {
		if !wantSet[n] {
			t.Fatalf("unexpected tool in HTTP list: %s", n)
		}
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

func TestListenAllowsLoopbackByDefault(t *testing.T) {
	l, err := server.Listen(server.Options{Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatalf("loopback listen: %v", err)
	}
	defer l.Close()
}

func TestListenAllowsRemoteWithFlag(t *testing.T) {
	l, err := server.Listen(server.Options{Addr: "0.0.0.0:0", AllowRemote: true})
	if err != nil {
		t.Fatalf("listen with allow-remote: %v", err)
	}
	defer l.Close()
}

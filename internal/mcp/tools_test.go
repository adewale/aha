package mcp_test

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adewale/aha/internal/cli"
	"github.com/adewale/aha/internal/corpus"
	"github.com/adewale/aha/internal/mcp"
	"github.com/adewale/aha/internal/model"
	"github.com/adewale/aha/internal/testutil"
)

// buildCorpus runs an `aha refresh` against the standard test fixtures and
// returns an opened, populated corpus.Store plus the config it was built from.
func buildCorpus(t *testing.T) (*corpus.Store, model.Config) {
	t.Helper()
	root := t.TempDir()
	fx := testutil.WriteAgentFixtures(t, root)
	outDir := filepath.Join(root, "bundles")
	corpusDir := filepath.Join(root, "corpus")
	configPath := filepath.Join(root, "config.jsonc")
	cfg := `{
		"machine_id":"mcp-test",
		"sources":[
			{"type":"pi","root":"` + filepath.ToSlash(fx.PiRoot) + `","enabled":true},
			{"type":"claude-code","root":"` + filepath.ToSlash(fx.ClaudeRoot) + `","enabled":true}
		],
		"corpus_dir":"` + filepath.ToSlash(corpusDir) + `",
		"depot":{"type":"local","location":"` + filepath.ToSlash(outDir) + `"},
		"include_subagents":true,
		"include_images":true,
		"index_tool_output":false,
		"redaction":"none-v1",
		"accept_secrets_warning":true
	}`
	if err := os.WriteFile(configPath, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := cli.Run([]string{"refresh", "--config", configPath, "--captured-at", "2026-01-03T00:00:00Z", "--bundle-id", "mcp"}, io.Discard, io.Discard); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	store, err := corpus.OpenExisting(corpusDir)
	if err != nil {
		t.Fatalf("OpenExisting: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store, model.Config{CorpusDir: corpusDir, MachineID: "mcp-test"}
}

// wireResponse is the subset of JSON-RPC response fields the wire tests
// inspect. It mirrors the encoded shape, not the unexported internal type.
type wireResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// callOne pipes a single request through Serve and returns the decoded
// response. Notifications return nil.
func callOne(t *testing.T, b mcp.Backend, req mcp.Message) *wireResponse {
	t.Helper()
	frame, err := mcp.EncodeFrame(req)
	if err != nil {
		t.Fatal(err)
	}
	in := bytes.NewReader(frame)
	out := &bytes.Buffer{}
	if err := mcp.Serve(b, in, out, io.Discard); err != nil {
		t.Fatalf("serve: %v", err)
	}
	if out.Len() == 0 {
		return nil
	}
	resp := &wireResponse{}
	if err := json.Unmarshal(extractBodyBytes(t, out.Bytes()), resp); err != nil {
		t.Fatalf("decode wire response: %v\n%s", err, out.Bytes())
	}
	return resp
}

// extractBodyBytes returns the JSON body of the first frame in buf, ignoring
// the Content-Length header. Useful when we need the raw response to decode
// fields the protocol's Message struct doesn't carry.
func extractBodyBytes(t *testing.T, buf []byte) []byte {
	t.Helper()
	sep := []byte("\r\n\r\n")
	idx := bytes.Index(buf, sep)
	if idx < 0 {
		t.Fatalf("frame missing header separator: %q", buf)
	}
	return buf[idx+len(sep):]
}

func contentText(t *testing.T, resp *wireResponse) string {
	t.Helper()
	if resp.Result == nil {
		t.Fatalf("response has no result: %+v", resp)
	}
	var wrapper struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(resp.Result, &wrapper); err != nil {
		t.Fatalf("decode content wrapper: %v\n%s", err, resp.Result)
	}
	if len(wrapper.Content) == 0 {
		t.Fatalf("response has no content: %s", resp.Result)
	}
	return wrapper.Content[0].Text
}

func TestInitializeReturnsServerInfo(t *testing.T) {
	store, cfg := buildCorpus(t)
	b := mcp.NewCorpusBackend(store, cfg)
	resp := callOne(t, b, mcp.Message{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "initialize"})
	if resp.Error != nil {
		t.Fatalf("initialize errored: %+v", resp.Error)
	}
	for _, want := range []string{`"protocolVersion"`, `"aha"`, `"tools"`} {
		if !bytes.Contains(resp.Result, []byte(want)) {
			t.Fatalf("initialize result missing %q: %s", want, resp.Result)
		}
	}
}

func TestToolsListAdvertisesReadOnlySet(t *testing.T) {
	store, cfg := buildCorpus(t)
	b := mcp.NewCorpusBackend(store, cfg)
	resp := callOne(t, b, mcp.Message{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "tools/list"})
	if resp.Error != nil {
		t.Fatalf("tools/list errored: %+v", resp.Error)
	}
	for _, name := range []string{"search", "read", "status", "verify", "conflicts", "corpus_size", "doctor"} {
		if !bytes.Contains(resp.Result, []byte(`"name":"`+name+`"`)) {
			t.Fatalf("tools/list missing %q: %s", name, resp.Result)
		}
	}
	for _, banned := range []string{"refresh", "snapshot", "ingest"} {
		if bytes.Contains(resp.Result, []byte(`"name":"`+banned+`"`)) {
			t.Fatalf("tools/list exposed write tool %q: %s", banned, resp.Result)
		}
	}
}

func TestNotificationsInitializedHasNoResponse(t *testing.T) {
	store, cfg := buildCorpus(t)
	b := mcp.NewCorpusBackend(store, cfg)
	resp := callOne(t, b, mcp.Message{JSONRPC: "2.0", Method: "notifications/initialized"})
	if resp != nil {
		t.Fatalf("notifications/initialized must not produce a response, got %+v", resp)
	}
}

func TestUnknownMethodReturnsMethodNotFound(t *testing.T) {
	store, cfg := buildCorpus(t)
	b := mcp.NewCorpusBackend(store, cfg)
	resp := callOne(t, b, mcp.Message{JSONRPC: "2.0", ID: json.RawMessage(`9`), Method: "nope"})
	if resp.Error == nil || resp.Error.Code != -32601 {
		t.Fatalf("expected method-not-found, got %+v", resp.Error)
	}
}

func TestToolsCallSearchReturnsResultsAndChainsToRead(t *testing.T) {
	store, cfg := buildCorpus(t)
	b := mcp.NewCorpusBackend(store, cfg)
	resp := callOne(t, b, mcp.Message{
		JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "tools/call",
		Params: json.RawMessage(`{"name":"search","arguments":{"query":"needle","limit":5}}`),
	})
	if resp.Error != nil {
		t.Fatalf("search errored: %+v", resp.Error)
	}
	text := contentText(t, resp)
	if !strings.Contains(text, `"ref_text"`) {
		t.Fatalf("search result missing ref_text: %s", text)
	}
	var results []map[string]any
	if err := json.Unmarshal([]byte(text), &results); err != nil {
		t.Fatalf("unmarshal search results: %v\n%s", err, text)
	}
	if len(results) == 0 {
		t.Fatalf("search returned zero results: %s", text)
	}
	refText, _ := results[0]["ref_text"].(string)
	if refText == "" {
		t.Fatalf("first result lacks ref_text: %v", results[0])
	}
	args, _ := json.Marshal(map[string]any{"ref": refText, "before": 1, "after": 3})
	resp = callOne(t, b, mcp.Message{
		JSONRPC: "2.0", ID: json.RawMessage(`2`), Method: "tools/call",
		Params: json.RawMessage(`{"name":"read","arguments":` + string(args) + `}`),
	})
	if resp.Error != nil {
		t.Fatalf("read errored: %+v", resp.Error)
	}
	readText := contentText(t, resp)
	if !strings.Contains(readText, `"entry_id"`) {
		t.Fatalf("read result missing entry_id: %s", readText)
	}
}

func TestToolsCallStatusReturnsCorpusShape(t *testing.T) {
	store, cfg := buildCorpus(t)
	b := mcp.NewCorpusBackend(store, cfg)
	resp := callOne(t, b, mcp.Message{
		JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "tools/call",
		Params: json.RawMessage(`{"name":"status"}`),
	})
	if resp.Error != nil {
		t.Fatalf("status errored: %+v", resp.Error)
	}
	text := contentText(t, resp)
	for _, want := range []string{`"corpus_dir"`, `"sessions"`, `"entries"`, `"fts_messages"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("status missing %q: %s", want, text)
		}
	}
}

func TestToolsCallDoctorReturnsLocalDiagnostics(t *testing.T) {
	store, cfg := buildCorpus(t)
	b := mcp.NewCorpusBackend(store, cfg)
	resp := callOne(t, b, mcp.Message{
		JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "tools/call",
		Params: json.RawMessage(`{"name":"doctor"}`),
	})
	if resp.Error != nil {
		t.Fatalf("doctor errored: %+v", resp.Error)
	}
	text := contentText(t, resp)
	for _, want := range []string{`"version"`, `"adapters"`, `"sources"`, `"corpus"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("doctor missing %q: %s", want, text)
		}
	}
	if strings.Contains(text, `"depot"`) {
		t.Fatalf("phase-1 doctor must not include depot probe: %s", text)
	}
}

func TestToolsCallRejectsUnknownArg(t *testing.T) {
	store, cfg := buildCorpus(t)
	b := mcp.NewCorpusBackend(store, cfg)
	resp := callOne(t, b, mcp.Message{
		JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "tools/call",
		Params: json.RawMessage(`{"name":"search","arguments":{"query":"x","bogus":1}}`),
	})
	if resp.Error == nil || !strings.Contains(resp.Error.Message, "unexpected argument") {
		t.Fatalf("expected unexpected-argument error, got %+v", resp.Error)
	}
}

func TestToolsCallRejectsMissingRequiredArg(t *testing.T) {
	store, cfg := buildCorpus(t)
	b := mcp.NewCorpusBackend(store, cfg)
	resp := callOne(t, b, mcp.Message{
		JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "tools/call",
		Params: json.RawMessage(`{"name":"search","arguments":{}}`),
	})
	if resp.Error == nil || !strings.Contains(resp.Error.Message, "missing required") {
		t.Fatalf("expected missing-required error, got %+v", resp.Error)
	}
}

func TestToolsCallRejectsUnknownTool(t *testing.T) {
	store, cfg := buildCorpus(t)
	b := mcp.NewCorpusBackend(store, cfg)
	resp := callOne(t, b, mcp.Message{
		JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "tools/call",
		Params: json.RawMessage(`{"name":"refresh"}`),
	})
	if resp.Error == nil || !strings.Contains(resp.Error.Message, "unknown tool") {
		t.Fatalf("expected unknown-tool error, got %+v", resp.Error)
	}
}

func TestServeRoundTripOverStdio(t *testing.T) {
	store, cfg := buildCorpus(t)
	b := mcp.NewCorpusBackend(store, cfg)
	in := &bytes.Buffer{}
	out := &bytes.Buffer{}
	for _, msg := range []mcp.Message{
		{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "initialize"},
		{JSONRPC: "2.0", Method: "notifications/initialized"},
		{JSONRPC: "2.0", ID: json.RawMessage(`2`), Method: "tools/list"},
	} {
		frame, err := mcp.EncodeFrame(msg)
		if err != nil {
			t.Fatal(err)
		}
		in.Write(frame)
	}
	if err := mcp.Serve(b, in, out, io.Discard); err != nil {
		t.Fatalf("serve: %v", err)
	}
	resps, rest, err := mcp.ParseFrames(out.Bytes())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(rest) != 0 {
		t.Fatalf("trailing bytes: %q", rest)
	}
	if len(resps) != 2 {
		t.Fatalf("expected 2 responses (initialize + tools/list), got %d", len(resps))
	}
}

// In-process MCP tests using the SDK's NewInMemoryTransports pair. These
// run a full Client↔Server loop through the official SDK without spawning
// a subprocess, so they're fast and cover the wire-format-correct path
// the SDK enforces.
//
// Cross-language conformance (Python/TypeScript/Go SDKs driving aha mcp,
// and our typed TS client driving Python/TS/Go reference servers) lives
// in scripts/mcp-conformance/ and internal/mcp/conformance/. This file
// is the Go-only fast lane.
package mcp_test

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/adewale/aha/internal/cli"
	"github.com/adewale/aha/internal/corpus"
	"github.com/adewale/aha/internal/mcp"
	"github.com/adewale/aha/internal/model"
	"github.com/adewale/aha/internal/testutil"
)

// expectedTools is sourced from the package's exported canonical list,
// not duplicated. Tests that need to assert against the registered set
// reference this name; cross-language conformance scripts reference
// mcp.ToolNames via the drift test below.
var expectedTools = mcp.ToolNames

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

// connectPair sets up an in-process Client↔Server pair against a populated
// corpus and runs the initialize handshake. Returns the client session
// the test should drive.
func connectPair(t *testing.T) (*sdkmcp.ClientSession, context.Context, context.CancelFunc) {
	t.Helper()
	store, cfg := buildCorpus(t)
	backend := mcp.NewCorpusBackend(store, cfg)
	server := mcp.NewServer(backend)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)

	clientTransport, serverTransport := sdkmcp.NewInMemoryTransports()

	// Run the server in a goroutine; it lives until ctx is cancelled.
	go func() { _ = server.Run(ctx, serverTransport) }()

	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "aha-inproc", Version: "0.1.0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		cancel()
		t.Fatalf("client.Connect: %v", err)
	}
	t.Cleanup(func() {
		_ = session.Close()
		cancel()
	})
	return session, ctx, cancel
}

func TestInitializeReturnsServerInfo(t *testing.T) {
	session, _, _ := connectPair(t)
	info := session.InitializeResult()
	if info == nil {
		t.Fatal("nil InitializeResult after Connect")
	}
	if info.ServerInfo.Name != "aha" {
		t.Fatalf("serverInfo.name = %q, want aha", info.ServerInfo.Name)
	}
}

func TestToolsListAdvertisesReadOnlySet(t *testing.T) {
	session, ctx, _ := connectPair(t)
	listed, err := session.ListTools(ctx, &sdkmcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	got := make([]string, len(listed.Tools))
	for i, tool := range listed.Tools {
		got[i] = tool.Name
	}
	sort.Strings(got)
	if !reflect.DeepEqual(got, expectedTools) {
		t.Fatalf("tools/list mismatch:\n  got:  %v\n  want: %v", got, expectedTools)
	}
	for _, tool := range listed.Tools {
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Fatalf("%s: readOnlyHint not true: %+v", tool.Name, tool.Annotations)
		}
	}
}

func TestToolsCallStatusReturnsCorpusShape(t *testing.T) {
	session, ctx, _ := connectPair(t)
	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "status"})
	if err != nil {
		t.Fatalf("CallTool(status): %v", err)
	}
	if res.IsError {
		t.Fatalf("status returned isError: %s", contentText(t, res))
	}
	body := contentText(t, res)
	for _, key := range []string{`"corpus_dir"`, `"sessions"`, `"entries"`, `"fts_messages"`} {
		if !strings.Contains(body, key) {
			t.Fatalf("status missing %q: %s", key, body)
		}
	}
}

func TestToolsCallSearchReturnsResultsAndChainsToRead(t *testing.T) {
	session, ctx, _ := connectPair(t)
	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "search",
		Arguments: map[string]any{"query": "needle", "limit": 5},
	})
	if err != nil {
		t.Fatalf("CallTool(search): %v", err)
	}
	if res.IsError {
		t.Fatalf("search returned isError: %s", contentText(t, res))
	}
	var hits []map[string]any
	if err := json.Unmarshal([]byte(contentText(t, res)), &hits); err != nil {
		t.Fatalf("decode hits: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("search returned zero results")
	}
	refText, _ := hits[0]["ref_text"].(string)
	if refText == "" {
		t.Fatalf("first hit lacks ref_text: %v", hits[0])
	}
	readRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "read",
		Arguments: map[string]any{"ref": refText, "before": 1, "after": 3},
	})
	if err != nil {
		t.Fatalf("CallTool(read): %v", err)
	}
	if readRes.IsError {
		t.Fatalf("read returned isError: %s", contentText(t, readRes))
	}
	if !strings.Contains(contentText(t, readRes), `"entry_id"`) {
		t.Fatalf("read result missing entry_id: %s", contentText(t, readRes))
	}
}

func TestEmptySearchReturnsList(t *testing.T) {
	session, ctx, _ := connectPair(t)
	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "search",
		Arguments: map[string]any{"query": "definitelynotinthecorpus"},
	})
	if err != nil {
		t.Fatalf("CallTool(search empty): %v", err)
	}
	body := contentText(t, res)
	var hits []any
	if err := json.Unmarshal([]byte(body), &hits); err != nil {
		t.Fatalf("empty search payload not a list: %v\n%s", err, body)
	}
	if len(hits) != 0 {
		t.Fatalf("empty search payload not []: %v", hits)
	}
}

func TestToolsCallClustersReturnsList(t *testing.T) {
	session, ctx, _ := connectPair(t)
	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "clusters",
		Arguments: map[string]any{"limit": 1},
	})
	if err != nil {
		t.Fatalf("CallTool(clusters): %v", err)
	}
	if res.IsError {
		t.Fatalf("clusters returned isError: %s", contentText(t, res))
	}
	var clusters []corpus.Cluster
	if err := json.Unmarshal([]byte(contentText(t, res)), &clusters); err != nil {
		t.Fatalf("clusters payload not a list: %v\n%s", err, contentText(t, res))
	}
	if clusters == nil {
		t.Fatalf("clusters payload must be [] not null: %s", contentText(t, res))
	}
}

func TestToolsCallSkillCandidatesReturnsList(t *testing.T) {
	session, ctx, _ := connectPair(t)
	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "skill_candidates",
		Arguments: map[string]any{"limit": 1},
	})
	if err != nil {
		t.Fatalf("CallTool(skill_candidates): %v", err)
	}
	if res.IsError {
		t.Fatalf("skill_candidates returned isError: %s", contentText(t, res))
	}
	var candidates []corpus.SkillCandidate
	if err := json.Unmarshal([]byte(contentText(t, res)), &candidates); err != nil {
		t.Fatalf("skill_candidates payload not a list: %v\n%s", err, contentText(t, res))
	}
	if candidates == nil {
		t.Fatalf("skill_candidates payload must be [] not null: %s", contentText(t, res))
	}
}

func TestToolsCallDoctorReturnsLocalDiagnostics(t *testing.T) {
	session, ctx, _ := connectPair(t)
	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "doctor"})
	if err != nil {
		t.Fatalf("CallTool(doctor): %v", err)
	}
	body := contentText(t, res)
	for _, want := range []string{`"version"`, `"adapters"`, `"sources"`, `"corpus"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("doctor missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, `"depot"`) {
		t.Fatalf("MCP doctor must not include depot probe: %s", body)
	}
}

func TestToolsCallRejectsUnknownArg(t *testing.T) {
	session, ctx, _ := connectPair(t)
	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "search",
		Arguments: map[string]any{"query": "x", "bogus": 1},
	})
	if err == nil && (res == nil || !res.IsError) {
		t.Fatalf("expected error on unknown arg, got success: res=%+v", res)
	}
}

func TestToolsCallRejectsMissingRequiredArg(t *testing.T) {
	session, ctx, _ := connectPair(t)
	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "search",
		Arguments: map[string]any{},
	})
	if err == nil && (res == nil || !res.IsError) {
		t.Fatalf("expected error on missing required arg, got success: res=%+v", res)
	}
}

func TestToolsCallRejectsUnknownTool(t *testing.T) {
	session, ctx, _ := connectPair(t)
	_, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "refresh"})
	if err == nil {
		t.Fatal("expected error on unknown tool")
	}
	if !strings.Contains(err.Error(), "refresh") && !strings.Contains(err.Error(), "tool") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ---------- CallTool (HTTP-server-facing path) tests ----------

func TestCallToolDispatchesSearch(t *testing.T) {
	store, cfg := buildCorpus(t)
	backend := mcp.NewCorpusBackend(store, cfg)
	out, err := mcp.CallTool(backend, "search", json.RawMessage(`{"query":"needle","limit":3}`))
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	body, _ := json.Marshal(out)
	if !strings.Contains(string(body), `"ref_text"`) {
		t.Fatalf("CallTool search missing ref_text: %s", body)
	}
}

func TestCallToolRejectsUnknownArg(t *testing.T) {
	store, cfg := buildCorpus(t)
	backend := mcp.NewCorpusBackend(store, cfg)
	_, err := mcp.CallTool(backend, "search", json.RawMessage(`{"query":"x","bogus":1}`))
	if err == nil {
		t.Fatal("expected error on unknown arg")
	}
	if !strings.Contains(err.Error(), "unexpected argument") && !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("expected unexpected-argument error, got %v", err)
	}
}

func TestCallToolRejectsUnknownTool(t *testing.T) {
	store, cfg := buildCorpus(t)
	backend := mcp.NewCorpusBackend(store, cfg)
	_, err := mcp.CallTool(backend, "refresh", nil)
	if err == nil {
		t.Fatal("expected error on unknown tool")
	}
	if !strings.Contains(err.Error(), "unknown tool") {
		t.Fatalf("expected unknown-tool error, got %v", err)
	}
}

func TestCallToolEmptySearchReturnsList(t *testing.T) {
	store, cfg := buildCorpus(t)
	backend := mcp.NewCorpusBackend(store, cfg)
	out, err := mcp.CallTool(backend, "search", json.RawMessage(`{"query":"definitelynotinthecorpus"}`))
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	// The result is []search.Result; verify it's not a nil slice by JSON
	// marshalling and asserting we get "[]" rather than "null".
	body, _ := json.Marshal(out)
	if string(body) != "[]" {
		t.Fatalf("empty search payload not []: %s", body)
	}
}

// TestHTTPAndMCPPathsAreConsistent guards against divergence between the
// two CallTool entrypoints: the SDK-registered handlers used by `aha mcp`
// and the direct CallTool used by the HTTP dashboard. For every tool +
// args pair, the typed payload reachable through the MCP wire (decoded
// from content[].text) must equal the value `mcp.CallTool` returns
// directly (after a JSON marshal round-trip to normalize map vs struct
// representation).
//
// The audit flagged the absence of this guard as a LOW-severity gap; the
// two paths produce equivalent text today, but a future refactor on one
// side without the other would silently break consumers that mix MCP
// and HTTP. This test fails loudly on any such drift.
func TestHTTPAndMCPPathsAreConsistent(t *testing.T) {
	// Build a single corpus + backend and use it for BOTH paths so the
	// comparison is over production code, not over test fixture setup.
	store, cfg := buildCorpus(t)
	backend := mcp.NewCorpusBackend(store, cfg)

	// Spin up an in-process MCP server pair against this backend.
	server := mcp.NewServer(backend)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	clientTransport, serverTransport := sdkmcp.NewInMemoryTransports()
	go func() { _ = server.Run(ctx, serverTransport) }()
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "aha-consistency", Version: "0.1.0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	defer session.Close()

	cases := []struct {
		name string
		args map[string]any
	}{
		{"status", nil},
		{"verify", nil},
		{"conflicts", nil},
		{"corpus_size", nil},
		{"doctor", nil},
		{"search", map[string]any{"query": "needle", "limit": 5}},
		{"search", map[string]any{"query": "definitelynotinthecorpus"}},
		{"read", map[string]any{"session": "pi-session", "before": 1, "after": 1}},
		{"clusters", map[string]any{"limit": 1}},
		{"skill_candidates", map[string]any{"limit": 1}},
		{"incidents", map[string]any{"limit": 5}},
		{"overview", nil},
	}

	for _, tc := range cases {
		t.Run(tc.name+"/"+argHash(tc.args), func(t *testing.T) {
			// ---- Direct CallTool path (the HTTP server uses this) ----
			var raw json.RawMessage
			if tc.args != nil {
				raw, _ = json.Marshal(tc.args)
			}
			httpOut, err := mcp.CallTool(backend, tc.name, raw)
			if err != nil {
				t.Fatalf("direct CallTool errored: %v", err)
			}
			httpJSON, err := json.Marshal(httpOut)
			if err != nil {
				t.Fatalf("direct CallTool result not marshallable: %v", err)
			}

			// ---- SDK-handler path (the `aha mcp` server uses this) ----
			mcpRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
				Name:      tc.name,
				Arguments: tc.args,
			})
			if err != nil {
				t.Fatalf("MCP path errored: %v", err)
			}
			if mcpRes.IsError {
				t.Fatalf("MCP path returned isError: %s", contentText(t, mcpRes))
			}
			mcpJSON := []byte(contentText(t, mcpRes))

			// Normalize both through `any` so map key ordering and JSON
			// indentation don't pollute the comparison; we're checking the
			// semantic shape, not the byte-level output.
			var httpDec, mcpDec any
			if err := json.Unmarshal(httpJSON, &httpDec); err != nil {
				t.Fatalf("decode direct-path payload: %v\n%s", err, httpJSON)
			}
			if err := json.Unmarshal(mcpJSON, &mcpDec); err != nil {
				t.Fatalf("decode MCP-path payload: %v\n%s", err, mcpJSON)
			}
			if !reflect.DeepEqual(httpDec, mcpDec) {
				t.Fatalf("HTTP and MCP payloads diverge for %s\n  direct: %s\n     MCP: %s",
					tc.name, httpJSON, mcpJSON)
			}
		})
	}
}

// argHash is a stable-ish key for table-driven subtest names so a failing
// run identifies the args without dumping JSON in the name.
func argHash(args map[string]any) string {
	if len(args) == 0 {
		return "noargs"
	}
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+strings.ReplaceAll(strings.TrimSpace(string(jsonMustMarshal(args[k]))), `"`, ""))
	}
	return strings.Join(parts, ",")
}

func jsonMustMarshal(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

// ---------- helpers ----------

func contentText(t *testing.T, res *sdkmcp.CallToolResult) string {
	t.Helper()
	if len(res.Content) == 0 {
		t.Fatal("no content blocks")
	}
	tb, ok := res.Content[0].(*sdkmcp.TextContent)
	if !ok {
		t.Fatalf("content[0] not TextContent: %T", res.Content[0])
	}
	return tb.Text
}

// TestCanonicalToolListIsSorted guards a small invariant: ToolNames must be
// in sorted order so cross-language reflections (Python sorted() in the
// conformance scripts, TS .sort() in client_against_aha.ts) compare against
// the same sequence. A drift elsewhere should be the loud failure.
func TestCanonicalToolListIsSorted(t *testing.T) {
	names := append([]string(nil), mcp.ToolNames...)
	sort.Strings(names)
	if !reflect.DeepEqual(names, mcp.ToolNames) {
		t.Fatalf("mcp.ToolNames must be sorted; got %v want %v", mcp.ToolNames, names)
	}
}

// TestCanonicalToolListReferencedByConformanceScripts asserts that every
// language harness that hard-codes the tool list (so it can be diffed
// against the running server) references each tool name. The check is
// substring-level on purpose — language syntax for "list of strings"
// varies — but it catches the common drift mode of someone adding a tool
// to tools.go and forgetting to update one of the harness arrays.
func TestCanonicalToolListReferencedByConformanceScripts(t *testing.T) {
	files := []string{
		"../../scripts/mcp-conformance/client_against_aha.py",
		"../../scripts/mcp-conformance/client_against_aha.ts",
		"./conformance/go_sdk_test.go",
	}
	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			body, err := os.ReadFile(file)
			if err != nil {
				t.Fatalf("read %s: %v", file, err)
			}
			text := string(body)
			for _, name := range mcp.ToolNames {
				if !strings.Contains(text, `"`+name+`"`) {
					t.Fatalf("%s missing canonical tool %q (mcp.ToolNames source of truth)", file, name)
				}
			}
		})
	}
}

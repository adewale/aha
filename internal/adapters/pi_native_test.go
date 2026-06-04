package adapters

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adewale/aha/internal/model"
)

// TestPiNativeProjectionSynthetic pins the Pi-native projection layer
// (the gap discovered via the pi-mono corpus). Pi uses content blocks
// of type "toolCall" with an "arguments" object (not "tool_use"/"input"),
// "thinking" blocks, and a camelCase usage schema — none of which the
// shared Anthropic-shaped parser handles.
func TestPiNativeProjectionSynthetic(t *testing.T) {
	input := strings.Join([]string{
		`{"type":"session","version":3,"id":"pi-native","timestamp":"2026-01-01T00:00:00Z","cwd":"/tmp"}`,
		`{"type":"message","id":"a1","parentId":"","timestamp":"2026-01-01T00:00:01Z","message":{"role":"assistant","content":[{"type":"thinking","thinking":"let me reason about this","thinkingSignature":"sig"},{"type":"toolCall","id":"toolu_1","name":"bash","arguments":{"command":"git status"}}],"model":"claude-sonnet-4-6","usage":{"input":10,"output":140,"cacheRead":7,"cacheWrite":5518,"totalTokens":5675,"cost":{"total":0.038}}}}`,
	}, "\n")

	ps, err := Pi{}.ParseSession(t.Context(), model.SessionFile{Source: "pi", SessionID: "fallback"}, strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if len(ps.Entries) != 1 {
		t.Fatalf("entries=%d want 1 (header stripped)", len(ps.Entries))
	}
	e := ps.Entries[0]

	// toolCall → tool_name + command + files_json
	if e.ToolName != "bash" {
		t.Fatalf("toolCall tool_name=%q want bash", e.ToolName)
	}
	if e.Command != "git status" {
		t.Fatalf("toolCall command=%q want 'git status'", e.Command)
	}
	if !strings.Contains(e.FilesJSON, "git status") {
		t.Fatalf("toolCall files_json=%q want it to contain the arguments", e.FilesJSON)
	}

	// thinking → text (so reasoning is searchable)
	if !strings.Contains(e.Text, "let me reason about this") {
		t.Fatalf("thinking text not projected: %q", e.Text)
	}

	// camelCase usage → tokens + cache splits
	if e.Tokens != 5675 {
		t.Fatalf("tokens=%d want 5675 (Pi totalTokens)", e.Tokens)
	}
	if e.CacheReadTokens != 7 {
		t.Fatalf("cache_read=%d want 7", e.CacheReadTokens)
	}
	if e.CacheWriteTokens != 5518 {
		t.Fatalf("cache_write=%d want 5518", e.CacheWriteTokens)
	}
	if e.Cost != 0.038 {
		t.Fatalf("cost=%v want 0.038", e.Cost)
	}
}

// TestPiUsageSumsWhenTotalAbsent pins the projectPiUsage fallback: when
// Pi omits the precomputed totalTokens, tokens is the sum of
// input+output+cacheRead+cacheWrite. Targets the arithmetic and
// boundary mutants at pi.go:180-182.
func TestPiUsageSumsWhenTotalAbsent(t *testing.T) {
	input := strings.Join([]string{
		`{"type":"session","version":3,"id":"pi-sum","timestamp":"2026-01-01T00:00:00Z","cwd":"/tmp"}`,
		`{"type":"message","id":"a1","parentId":"","timestamp":"2026-01-01T00:00:01Z","message":{"role":"assistant","content":[{"type":"text","text":"hi"}],"usage":{"input":3,"output":11,"cacheRead":100,"cacheWrite":1000}}}`,
	}, "\n")
	ps, err := Pi{}.ParseSession(t.Context(), model.SessionFile{Source: "pi", SessionID: "fallback"}, strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	e := ps.Entries[0]
	if e.Tokens != 3+11+100+1000 {
		t.Fatalf("summed tokens=%d want %d", e.Tokens, 3+11+100+1000)
	}
	if e.CacheReadTokens != 100 || e.CacheWriteTokens != 1000 {
		t.Fatalf("cache splits read=%d write=%d want 100/1000", e.CacheReadTokens, e.CacheWriteTokens)
	}
}

// TestPiUsageZeroDoesNotProject pins that an all-zero usage block leaves
// tokens at 0 rather than writing a spurious 0 row of work. Targets the
// `total > 0` boundary guard at pi.go:182.
func TestPiUsageZeroDoesNotProject(t *testing.T) {
	input := strings.Join([]string{
		`{"type":"session","version":3,"id":"pi-zero","timestamp":"2026-01-01T00:00:00Z","cwd":"/tmp"}`,
		`{"type":"message","id":"a1","parentId":"","timestamp":"2026-01-01T00:00:01Z","message":{"role":"assistant","content":[{"type":"text","text":"hi"}],"usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0}}}`,
	}, "\n")
	ps, err := Pi{}.ParseSession(t.Context(), model.SessionFile{Source: "pi", SessionID: "fallback"}, strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if ps.Entries[0].Tokens != 0 {
		t.Fatalf("tokens=%d want 0 for all-zero usage", ps.Entries[0].Tokens)
	}
}

// TestPiNativeProjectionRealCorpus asserts the camelCase-token gap is
// closed for real pi-mono sessions: at least one assistant entry must
// carry a populated tool call and non-zero tokens (before this layer,
// every Pi assistant turn projected zero tokens and no tool name).
func TestPiNativeProjectionRealCorpus(t *testing.T) {
	dir := filepath.Join("testdata", "corpora", "pi-mono-sample")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Skipf("pi-mono corpus not vendored: %v", err)
	}
	sawToolCall := false
	sawTokens := false
	for _, de := range entries {
		if de.IsDir() || !strings.HasSuffix(de.Name(), ".jsonl") {
			continue
		}
		f, err := os.Open(filepath.Join(dir, de.Name()))
		if err != nil {
			t.Fatal(err)
		}
		ps, err := Pi{}.ParseSession(t.Context(), model.SessionFile{Source: "pi", SessionID: de.Name()}, f)
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range ps.Entries {
			if e.ToolName != "" {
				sawToolCall = true
			}
			if e.Tokens > 0 {
				sawTokens = true
			}
		}
	}
	if !sawToolCall {
		t.Fatal("no Pi entry across the corpus projected a tool_name (toolCall blocks not handled)")
	}
	if !sawTokens {
		t.Fatal("no Pi entry across the corpus projected tokens (camelCase usage not handled)")
	}
}

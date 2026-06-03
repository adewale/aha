package adapters

import (
	"bufio"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/adewale/aha/internal/model"
)

// TestRawJSONIsVerbatimAcrossAdapters pins the highest-fidelity invariant we
// can offer at the corpus layer: every source line is recoverable byte-for-byte
// via an entry's RawJSON, or (for adapters that strip a session header) via
// ps.Metadata["header"]. The bundle is byte-exact by content-addressing; this
// test extends the same guarantee one layer up.
func TestRawJSONIsVerbatimAcrossAdapters(t *testing.T) {
	cases := []struct {
		name    string
		adapter SourceAdapter
		fixture string
		file    model.SessionFile
	}{
		{name: "pi", adapter: Pi{}, fixture: "testdata/pi_realish.jsonl", file: model.SessionFile{Source: "pi", SessionID: "path-fallback"}},
		{name: "claude", adapter: ClaudeCode{}, fixture: "testdata/claude_realish.jsonl", file: model.SessionFile{Source: "claude-code", SessionID: "claude-path", CWD: "/Users/me/work"}},
		{name: "codex", adapter: CodexCLI{}, fixture: "testdata/codex_realish.jsonl", file: model.SessionFile{Source: "codex", SessionID: "codex-path"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sourceLines := readNonEmptyLines(t, tc.fixture)
			f, err := os.Open(tc.fixture)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			ps, err := tc.adapter.ParseSession(t.Context(), tc.file, f)
			if err != nil {
				t.Fatal(err)
			}
			preserved := collectPreservedRawJSON(t, ps)
			assertMultisetEqual(t, preserved, parseJSONLines(t, sourceLines))
		})
	}
}

// TestRawJSONIsVerbatimNoHeaderStrip exercises a synthetic input every adapter
// shares: there is no special "session" entry to strip, so the entire input
// must round-trip through entries.
func TestRawJSONIsVerbatimNoHeaderStrip(t *testing.T) {
	const input = `{"type":"message","id":"m1","timestamp":"2026-01-01T00:00:01Z","message":{"role":"user","content":[{"type":"text","text":"a"}]}}
{"type":"message","id":"m2","parentId":"m1","timestamp":"2026-01-01T00:00:02Z","message":{"role":"assistant","content":[{"type":"text","text":"b"}]}}
`
	for _, source := range []string{"pi", "claude-code", "codex"} {
		t.Run(source, func(t *testing.T) {
			ps, err := parseGenericJSONL(source, model.SessionFile{Source: source, SessionID: "s"}, strings.NewReader(input))
			if err != nil {
				t.Fatal(err)
			}
			if len(ps.Entries) != 2 {
				t.Fatalf("entries=%d want 2", len(ps.Entries))
			}
			preserved := collectPreservedRawJSON(t, ps)
			assertMultisetEqual(t, preserved, parseJSONLines(t, []string{
				`{"type":"message","id":"m1","timestamp":"2026-01-01T00:00:01Z","message":{"role":"user","content":[{"type":"text","text":"a"}]}}`,
				`{"type":"message","id":"m2","parentId":"m1","timestamp":"2026-01-01T00:00:02Z","message":{"role":"assistant","content":[{"type":"text","text":"b"}]}}`,
			}))
		})
	}
}

// readNonEmptyLines returns each non-blank, non-whitespace line from path.
func readNonEmptyLines(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []string
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 1024*1024), 8*1024*1024)
	for s.Scan() {
		if strings.TrimSpace(s.Text()) == "" {
			continue
		}
		out = append(out, s.Text())
	}
	if err := s.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func parseJSONLines(t *testing.T, lines []string) []any {
	t.Helper()
	out := make([]any, 0, len(lines))
	for i, l := range lines {
		var v any
		if err := json.Unmarshal([]byte(l), &v); err != nil {
			t.Fatalf("source line %d not valid JSON: %v\n%s", i+1, err, l)
		}
		out = append(out, v)
	}
	return out
}

// collectPreservedRawJSON gathers every JSON value that the projection has kept
// verbatim: every entry's RawJSON, plus any string-valued ps.Metadata["header"]
// (Pi's stripped session header).
func collectPreservedRawJSON(t *testing.T, ps *model.ParsedSession) []any {
	t.Helper()
	out := make([]any, 0, len(ps.Entries)+1)
	for i, e := range ps.Entries {
		if e.RawJSON == "" {
			t.Fatalf("entry %d (%s) has empty RawJSON", i, e.EntryID)
		}
		var v any
		if err := json.Unmarshal([]byte(e.RawJSON), &v); err != nil {
			t.Fatalf("entry %d (%s) RawJSON not valid JSON: %v\n%s", i, e.EntryID, err, e.RawJSON)
		}
		out = append(out, v)
	}
	if hdr, ok := ps.Metadata["header"].(string); ok && hdr != "" {
		var v any
		if err := json.Unmarshal([]byte(hdr), &v); err != nil {
			t.Fatalf("Metadata[\"header\"] not valid JSON: %v\n%s", err, hdr)
		}
		out = append(out, v)
	}
	return out
}

// assertMultisetEqual asserts that got and want hold the same JSON values as
// multisets (order-independent, exact-match including nested structure).
func assertMultisetEqual(t *testing.T, got, want []any) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("preserved=%d source=%d", len(got), len(want))
	}
	used := make([]bool, len(want))
	for i, gv := range got {
		matched := false
		for j, wv := range want {
			if used[j] {
				continue
			}
			if reflect.DeepEqual(gv, wv) {
				used[j] = true
				matched = true
				break
			}
		}
		if !matched {
			t.Fatalf("preserved[%d] has no matching source line: %v", i, gv)
		}
	}
}

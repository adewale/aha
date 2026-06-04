package adapters

import (
	"os"
	"strings"
	"testing"

	"github.com/adewale/aha/internal/model"
)

func FuzzParseGenericJSONL(f *testing.F) {
	f.Add(`{"type":"user","message":{"content":"hello"}}`)
	f.Add(`not json`)
	f.Add(`{"type":"assistant","message":{"content":[{"type":"text","text":"hi"}]}}`)
	f.Fuzz(func(t *testing.T, input string) {
		_, err := parseGenericJSONL("claude-code", model.SessionFile{Source: "claude-code", SessionID: "s"}, strings.NewReader(input))
		if err != nil {
			t.Fatalf("parser returned scanner error: %v", err)
		}
	})
}

func TestParseCommittedRealishFixtures(t *testing.T) {
	tests := []struct {
		name       string
		source     string
		file       string
		wantID     string
		wantNeedle string
		wantAssets int
	}{
		{name: "pi", source: "pi", file: "testdata/pi_realish.jsonl", wantID: "pi-realish", wantNeedle: "search needle from pi fixture"},
		{name: "claude", source: "claude-code", file: "testdata/claude_realish.jsonl", wantID: "path-fallback", wantNeedle: "claude fixture needle", wantAssets: 1},
		{name: "codex", source: "codex", file: "testdata/codex_realish.jsonl", wantID: "codex-realish", wantNeedle: "codex fixture needle"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, err := os.Open(tt.file)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			ps, err := parseGenericJSONL(tt.source, model.SessionFile{Source: tt.source, SessionID: "path-fallback"}, f)
			if err != nil {
				t.Fatal(err)
			}
			if ps.Source != tt.source || ps.SourceSessionID != tt.wantID {
				t.Fatalf("bad parsed identity: %+v", ps)
			}
			var found *model.ParsedEntry
			for i := range ps.Entries {
				if ps.Entries[i].Text == tt.wantNeedle {
					found = &ps.Entries[i]
				}
			}
			if found == nil {
				t.Fatalf("fixture text not parsed: %+v", ps.Entries)
			}
			if len(found.Assets) != tt.wantAssets {
				t.Fatalf("fixture assets=%d want %d: %+v", len(found.Assets), tt.wantAssets, found.Assets)
			}
		})
	}
}

func TestParsePiNestedMessageRole(t *testing.T) {
	input := `{"type":"message","id":"p1","timestamp":"2026-01-01T00:00:01Z","message":{"role":"user","content":[{"type":"text","text":"real pi text"}]}}
{"type":"message","id":"p2","timestamp":"2026-01-01T00:00:02Z","message":{"role":"toolResult","toolName":"bash","content":[{"type":"text","text":"tool output"}]}}
`
	ps, err := parseGenericJSONL("pi", model.SessionFile{Source: "pi", SessionID: "s"}, strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if len(ps.Entries) != 2 {
		t.Fatalf("entries=%d", len(ps.Entries))
	}
	if ps.Entries[0].Role != "user" || ps.Entries[0].Text != "real pi text" {
		t.Fatalf("bad user entry: %+v", ps.Entries[0])
	}
	if ps.Entries[1].Role != "toolResult" || ps.Entries[1].ToolName != "bash" {
		t.Fatalf("bad tool entry: %+v", ps.Entries[1])
	}
}

// TestParseExtractsToolResultContent pins priority 6 from
// docs/research/cross-agent-data-capture.md: tool_result content
// blocks (both string and array forms) reach pe.Text so the
// index_tool_output config flag has something to index. Until this
// change parser.extractContent's tool_result case was a deliberate
// no-op even though the gated index path existed.
func TestParseExtractsToolResultContent(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "tool_result content as string",
			input: `{"type":"user","id":"r1","timestamp":"2026-01-01T00:00:01Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu_001","content":"stdout: file written"}]}}`,
			want:  "stdout: file written",
		},
		{
			name:  "tool_result content as nested text block",
			input: `{"type":"user","id":"r2","timestamp":"2026-01-01T00:00:01Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu_002","content":[{"type":"text","text":"nested block stdout"}]}]}}`,
			want:  "nested block stdout",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ps, err := parseGenericJSONL("claude-code", model.SessionFile{Source: "claude-code", SessionID: "s"}, strings.NewReader(tc.input))
			if err != nil {
				t.Fatal(err)
			}
			if len(ps.Entries) != 1 {
				t.Fatalf("entries=%d", len(ps.Entries))
			}
			if !strings.Contains(ps.Entries[0].Text, tc.want) {
				t.Fatalf("Text=%q, want it to contain %q", ps.Entries[0].Text, tc.want)
			}
		})
	}
}

func TestParseImageDimensions(t *testing.T) {
	input := `{"type":"user","message":{"content":[{"type":"image","source":{"media_type":"image/png","data":"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAAAAAA6fptVAAAACklEQVR42mP8z8AABQMBgAAn5B6iAAAAAElFTkSuQmCC"}}]}}`
	ps, err := parseGenericJSONL("claude-code", model.SessionFile{Source: "claude-code", SessionID: "s"}, strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if len(ps.Entries) != 1 || len(ps.Entries[0].Assets) != 1 {
		t.Fatalf("missing image asset: %+v", ps)
	}
	a := ps.Entries[0].Assets[0]
	if a.Width != 1 || a.Height != 1 || a.MimeType != "image/png" {
		t.Fatalf("bad image asset: %+v", a)
	}
}

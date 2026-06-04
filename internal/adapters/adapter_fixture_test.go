package adapters

import (
	"os"
	"testing"

	"github.com/adewale/aha/internal/model"
)

func TestAdapterParseSessionCommittedFixtures(t *testing.T) {
	tests := []struct {
		name        string
		adapter     SourceAdapter
		fixture     string
		file        model.SessionFile
		wantID      string
		wantSource  string
		wantEntries int
		wantNeedle  string
		check       func(*testing.T, *model.ParsedSession)
	}{
		{name: "pi", adapter: Pi{}, fixture: "testdata/pi_realish.jsonl", file: model.SessionFile{Source: "pi", SessionID: "path-fallback"}, wantID: "pi-realish", wantSource: "pi", wantEntries: 3, wantNeedle: "search needle from pi fixture", check: func(t *testing.T, ps *model.ParsedSession) {
			if ps.Entries[0].EntryType == "session" || ps.Entries[1].Role != "toolResult" || ps.Entries[1].ToolName != "bash" || ps.Entries[2].ParentID != "tool1" {
				t.Fatalf("pi adapter did not strip header/preserve tool/parent metadata: %+v", ps.Entries)
			}
		}},
		{name: "claude", adapter: ClaudeCode{}, fixture: "testdata/claude_realish.jsonl", file: model.SessionFile{Source: "claude-code", SessionID: "claude-path", CWD: "/Users/me/work"}, wantID: "claude-path", wantSource: "claude-code", wantEntries: 2, wantNeedle: "claude fixture needle", check: func(t *testing.T, ps *model.ParsedSession) {
			if len(ps.Entries[0].Assets) != 1 || ps.Entries[0].Assets[0].MimeType != "image/png" || ps.Entries[1].Model != "claude-test" || ps.Entries[1].Tokens != 19 {
				t.Fatalf("claude adapter did not preserve image/model/token metadata: %+v", ps.Entries)
			}
		}},
		{name: "codex", adapter: CodexCLI{}, fixture: "testdata/codex_realish.jsonl", file: model.SessionFile{Source: "codex", SessionID: "codex-path"}, wantID: "codex-realish", wantSource: "codex", wantEntries: 3, wantNeedle: "codex fixture needle", check: func(t *testing.T, ps *model.ParsedSession) {
			if ps.CWD != "/Users/me/work" || ps.StartedAt != "2026-05-20T12:00:00Z" || ps.Entries[1].Role != "user" || ps.Entries[2].Role != "assistant" {
				t.Fatalf("codex adapter did not preserve session/message metadata: %+v", ps)
			}
		}},
		{name: "codex-modern", adapter: CodexCLI{}, fixture: "testdata/codex_modern_realish.jsonl", file: model.SessionFile{Source: "codex", SessionID: "path-fallback"}, wantID: "codex-modern-id", wantSource: "codex", wantEntries: 7, wantNeedle: "codex modern needle", check: func(t *testing.T, ps *model.ParsedSession) {
			if ps.CWD != "/Users/me/proj" || ps.Entries[2].Role != "user" || ps.Entries[3].Role != "assistant" || ps.Entries[3].Model != "gpt-5-codex" {
				t.Fatalf("codex modern adapter did not unwrap message envelope: %+v", ps)
			}
			tool := false
			for _, e := range ps.Entries {
				if e.ToolName == "shell" && e.Command == "bash -lc ls -la" {
					tool = true
				}
			}
			if !tool {
				t.Fatalf("codex modern adapter did not extract the function_call tool/command: %+v", ps.Entries)
			}
		}},
		{name: "opencode", adapter: OpenCode{}, fixture: "testdata/opencode_realish.jsonl", file: model.SessionFile{Source: "opencode", SessionID: "path-fallback", CWD: ""}, wantID: "ses_realish", wantSource: "opencode", wantEntries: 2, wantNeedle: "opencode fixture needle", check: func(t *testing.T, ps *model.ParsedSession) {
			if ps.CWD != "/Users/me/work" || ps.Entries[0].Role != "user" || ps.Entries[1].Role != "assistant" || ps.Entries[1].Model != "opencode-test-model" || ps.Entries[1].Tokens != 19 || ps.Entries[1].ToolName != "bash" || ps.Entries[1].Command != "ls -la" {
				t.Fatalf("opencode adapter did not preserve session/message metadata: %+v", ps)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, err := os.Open(tt.fixture)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			ps, err := tt.adapter.ParseSession(t.Context(), tt.file, f)
			if err != nil {
				t.Fatal(err)
			}
			if ps.Source != tt.wantSource || ps.SourceSessionID != tt.wantID || len(ps.Entries) != tt.wantEntries {
				t.Fatalf("bad parsed session: source=%q id=%q entries=%d want source=%q id=%q entries=%d", ps.Source, ps.SourceSessionID, len(ps.Entries), tt.wantSource, tt.wantID, tt.wantEntries)
			}
			found := false
			for _, entry := range ps.Entries {
				if entry.Text == tt.wantNeedle {
					found = true
				}
			}
			if !found {
				t.Fatalf("fixture needle %q not found in entries: %+v", tt.wantNeedle, ps.Entries)
			}
			if tt.check != nil {
				tt.check(t, ps)
			}
		})
	}
}

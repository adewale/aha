package adapters

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adewale/aha/internal/model"
)

// TestCodexNativeProjectionSynthetic pins the Codex modern "payload"
// envelope projection (the gap discovered via the cfahlgren1/codex-sessions
// corpus). The shared parser reads message.* / message.content[]; Codex
// modern wraps everything under payload.*, so before this layer existed
// role/text/model/tokens were all empty for modern rollouts.
func TestCodexNativeProjectionSynthetic(t *testing.T) {
	input := strings.Join([]string{
		`{"type":"session_meta","timestamp":"2026-03-27T14:21:42Z","payload":{"id":"sess-codex","cwd":"/tmp/work","model_provider":"openai"}}`,
		`{"type":"event_msg","timestamp":"2026-03-27T14:21:43Z","payload":{"type":"user_message","message":"the user prompt text"}}`,
		`{"type":"turn_context","timestamp":"2026-03-27T14:21:44Z","payload":{"model":"gpt-5.4","collaboration_mode":{"settings":{"reasoning_effort":"xhigh"}}}}`,
		`{"type":"response_item","timestamp":"2026-03-27T14:21:45Z","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"the assistant answer"}]}}`,
		`{"type":"response_item","timestamp":"2026-03-27T14:21:46Z","payload":{"type":"function_call","name":"shell","call_id":"call_1","arguments":"{\"command\":\"ls -la\"}"}}`,
		`{"type":"event_msg","timestamp":"2026-03-27T14:21:47Z","payload":{"type":"agent_message","message":"final agent message"}}`,
		`{"type":"event_msg","timestamp":"2026-03-27T14:21:48Z","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":100,"output_tokens":40,"cached_input_tokens":60,"reasoning_output_tokens":25,"total_tokens":225}}}}`,
	}, "\n")

	ps, err := CodexCLI{}.ParseSession(t.Context(), model.SessionFile{Source: "codex", SessionID: "fallback"}, strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	byType := map[string]*model.ParsedEntry{}
	for i := range ps.Entries {
		e := &ps.Entries[i]
		// Key by the payload inner type where present, else outer type.
		byType[e.EntryType+"|"+e.Role] = e
	}

	// user_message → role=user, text populated
	um := findEntry(t, ps.Entries, func(e model.ParsedEntry) bool { return strings.Contains(e.Text, "the user prompt text") })
	if um.Role != "user" {
		t.Fatalf("user_message role=%q want user", um.Role)
	}

	// response_item/message assistant → role + text
	am := findEntry(t, ps.Entries, func(e model.ParsedEntry) bool { return strings.Contains(e.Text, "the assistant answer") })
	if am.Role != "assistant" {
		t.Fatalf("assistant message role=%q want assistant", am.Role)
	}
	// model comes from the preceding turn_context; the projection carries
	// the most recent model forward to message entries.
	if am.Model != "gpt-5.4" {
		t.Fatalf("assistant model=%q want gpt-5.4 (turn_context propagation)", am.Model)
	}

	// function_call → tool_name + command
	fc := findEntry(t, ps.Entries, func(e model.ParsedEntry) bool { return e.ToolName == "shell" })
	if fc.Command != "ls -la" {
		t.Fatalf("function_call command=%q want 'ls -la'", fc.Command)
	}

	// agent_message → text
	findEntry(t, ps.Entries, func(e model.ParsedEntry) bool { return strings.Contains(e.Text, "final agent message") })

	// token_count → tokens + splits on that entry
	tc := findEntry(t, ps.Entries, func(e model.ParsedEntry) bool { return e.Tokens > 0 })
	if tc.Tokens != 225 {
		t.Fatalf("token_count tokens=%d want 225 (total_tokens)", tc.Tokens)
	}
	if tc.CacheReadTokens != 60 {
		t.Fatalf("token_count cache_read=%d want 60", tc.CacheReadTokens)
	}
	if tc.ReasoningTokens != 25 {
		t.Fatalf("token_count reasoning=%d want 25", tc.ReasoningTokens)
	}

	// provider from session_meta carries forward to the assistant entry.
	if am.Provider != "openai" {
		t.Fatalf("assistant provider=%q want openai (session_meta propagation)", am.Provider)
	}
}

// TestCodexNativeProjectionRealCorpus asserts that, after the native
// projection layer, the vendored Codex rollouts no longer leave the
// core columns empty: a session that clearly contains a user prompt and
// assistant output must yield at least one user-role and one
// assistant-role entry with non-empty text.
func TestCodexNativeProjectionRealCorpus(t *testing.T) {
	dir := filepath.Join("testdata", "corpora", "codex-sample")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Skipf("codex corpus not vendored: %v", err)
	}
	for _, de := range entries {
		if de.IsDir() || !strings.HasSuffix(de.Name(), ".jsonl") {
			continue
		}
		t.Run(de.Name(), func(t *testing.T) {
			f, err := os.Open(filepath.Join(dir, de.Name()))
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			ps, err := CodexCLI{}.ParseSession(t.Context(), model.SessionFile{Source: "codex", SessionID: de.Name()}, f)
			if err != nil {
				t.Fatal(err)
			}
			var users, assistants, withText int
			for _, e := range ps.Entries {
				if strings.TrimSpace(e.Text) != "" {
					withText++
				}
				switch e.Role {
				case "user":
					users++
				case "assistant":
					assistants++
				}
			}
			if withText == 0 {
				t.Fatalf("%s: no entry has text after projection (payload envelope not handled)", de.Name())
			}
			if users == 0 {
				t.Fatalf("%s: no user-role entry after projection", de.Name())
			}
			if assistants == 0 {
				t.Fatalf("%s: no assistant-role entry after projection", de.Name())
			}
		})
	}
}

func findEntry(t *testing.T, entries []model.ParsedEntry, pred func(model.ParsedEntry) bool) model.ParsedEntry {
	t.Helper()
	for _, e := range entries {
		if pred(e) {
			return e
		}
	}
	t.Fatalf("no entry matched predicate")
	return model.ParsedEntry{}
}

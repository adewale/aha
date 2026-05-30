package testutil

import (
	"database/sql"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// FixtureRoots holds the source roots for each supported coding agent. Each
// field points at a fake-but-realistic source tree (or, for OpenCode, the
// directory containing a fake SQLite database) so the same fixtures can drive
// the whole pipeline end-to-end: discovery -> snapshot -> ingest -> search.
// Every agent's primary user message contains a distinct "<source> needle"
// string so tests can assert per-source searchability.
type FixtureRoots struct{ PiRoot, ClaudeRoot, CodexRoot, OpenCodeRoot string }

func WriteAgentFixtures(t *testing.T, root string) FixtureRoots {
	t.Helper()
	piRoot := filepath.Join(root, "pi")
	claudeRoot := filepath.Join(root, "claude")
	codexRoot := filepath.Join(root, "codex", "sessions")
	openCodeRoot := filepath.Join(root, "opencode")
	mustMkdir(t, filepath.Join(piRoot, "--Users-me-proj--"))
	mustMkdir(t, filepath.Join(piRoot, "--Users-me-proj--", "subagent-artifacts"))
	mustMkdir(t, filepath.Join(claudeRoot, "-Users-me-proj"))
	mustMkdir(t, filepath.Join(codexRoot, "2026", "05", "20"))
	png := tinyPNGBase64()
	pi := `{"type":"session","version":3,"id":"pi-session","timestamp":"2026-01-01T00:00:00Z","cwd":"/Users/me/proj"}
{"id":"p1","parentId":"","type":"user","role":"user","timestamp":"2026-01-01T00:00:01Z","message":{"content":"hello needle"}}
{"id":"p2","parentId":"p1","type":"assistant","role":"assistant","timestamp":"2026-01-01T00:00:02Z","message":{"content":[{"type":"text","text":"assistant reply"}]}}
`
	claude := `{"type":"user","timestamp":"2026-01-02T00:00:01Z","message":{"content":[{"type":"text","text":"claude needle"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"` + png + `"}}]}}
{"type":"assistant","timestamp":"2026-01-02T00:00:02Z","message":{"content":[{"type":"text","text":"claude reply"}],"model":"claude-test","usage":{"input_tokens":1,"output_tokens":2}}}
{"type":"assistant","uuid":"ctool","timestamp":"2026-01-02T00:00:03Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_fix","name":"Bash","input":{"command":"gh pr create --title needle"}}]}}
{"type":"user","uuid":"cres","timestamp":"2026-01-02T00:00:04Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_fix","is_error":true,"content":[{"type":"text","text":"pull request create failed: Head sha can't be blank"}]}]}}
`
	if err := os.WriteFile(filepath.Join(piRoot, "--Users-me-proj--", "2026_pi.jsonl"), []byte(pi), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(piRoot, "--Users-me-proj--", "subagent-artifacts", "pi-session_task_output.md"), []byte("artifact needle text"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(piRoot, "--Users-me-proj--", "subagent-artifacts", "unlinked_output.md"), []byte("unlinked artifact preserved"), 0o644); err != nil {
		t.Fatal(err)
	}
	imgBytes, _ := base64.StdEncoding.DecodeString("R0lGODlhAQABAIAAAAAAAP///ywAAAAAAQABAAACAUwAOw==")
	if err := os.WriteFile(filepath.Join(piRoot, "--Users-me-proj--", "subagent-artifacts", "prompt.gif"), imgBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claudeRoot, "-Users-me-proj", "abc.jsonl"), []byte(claude), 0o644); err != nil {
		t.Fatal(err)
	}
	codex := `{"type":"session","id":"codex-session","timestamp":"2026-01-03T00:00:00Z","cwd":"/Users/me/proj"}
{"id":"c1","type":"message","role":"user","timestamp":"2026-01-03T00:00:01Z","message":{"content":"codex needle"}}
{"id":"c2","type":"message","role":"assistant","timestamp":"2026-01-03T00:00:02Z","message":{"content":[{"type":"text","text":"codex reply"}]}}
`
	if err := os.WriteFile(filepath.Join(codexRoot, "2026", "05", "20", "rollout-2026-05-20T00-00-00-codex-session.jsonl"), []byte(codex), 0o644); err != nil {
		t.Fatal(err)
	}
	agent := `{"type":"user","timestamp":"2026-01-02T00:01:00Z","message":{"content":"agent subtask"}}
{"type":"assistant","timestamp":"2026-01-02T00:01:01Z","message":{"content":[{"type":"text","text":"agent done"}]}}
`
	if err := os.WriteFile(filepath.Join(claudeRoot, "-Users-me-proj", "agent-worker.jsonl"), []byte(agent), 0o644); err != nil {
		t.Fatal(err)
	}
	mustMkdir(t, openCodeRoot)
	writeOpenCodeDB(t, filepath.Join(openCodeRoot, "opencode.db"))
	return FixtureRoots{PiRoot: piRoot, ClaudeRoot: claudeRoot, CodexRoot: codexRoot, OpenCodeRoot: openCodeRoot}
}

// writeOpenCodeDB creates a fake OpenCode SQLite database matching the
// session/message/part schema the adapter expects, with one session whose user
// message carries the "opencode needle" and an assistant turn carrying a model,
// token usage, and a bash tool call.
func writeOpenCodeDB(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	stmts := []string{
		`CREATE TABLE session (id TEXT PRIMARY KEY, data TEXT)`,
		`CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT, data TEXT)`,
		`CREATE TABLE part (id TEXT PRIMARY KEY, message_id TEXT, session_id TEXT, data TEXT)`,
		`INSERT INTO session VALUES ('oc-session', '{"directory":"/Users/me/proj","time":{"created":1716200000000},"title":"opencode fixture"}')`,
		`INSERT INTO message VALUES ('ocm1', 'oc-session', '{"role":"user","time":{"created":1716200001000}}')`,
		`INSERT INTO message VALUES ('ocm2', 'oc-session', '{"role":"assistant","modelID":"opencode-test","providerID":"anthropic","tokens":{"input":5,"output":6,"cache":{"read":0,"write":0}},"time":{"created":1716200002000}}')`,
		`INSERT INTO part VALUES ('ocp1', 'ocm1', 'oc-session', '{"type":"text","text":"opencode needle"}')`,
		`INSERT INTO part VALUES ('ocp2', 'ocm2', 'oc-session', '{"type":"text","text":"opencode reply"}')`,
		`INSERT INTO part VALUES ('ocp3', 'ocm2', 'oc-session', '{"type":"tool","tool":"bash","state":{"input":{"command":"ls -la"}}}')`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("seed opencode db: %v", err)
		}
	}
}

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}
func tinyPNGBase64() string {
	b, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAAAAAA6fptVAAAACklEQVR42mP8z8AABQMBgAAn5B6iAAAAAElFTkSuQmCC")
	return base64.StdEncoding.EncodeToString(b)
}

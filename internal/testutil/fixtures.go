package testutil

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

type FixtureRoots struct{ PiRoot, ClaudeRoot string }

func WriteAgentFixtures(t *testing.T, root string) FixtureRoots {
	t.Helper()
	piRoot := filepath.Join(root, "pi")
	claudeRoot := filepath.Join(root, "claude")
	mustMkdir(t, filepath.Join(piRoot, "--Users-me-proj--"))
	mustMkdir(t, filepath.Join(piRoot, "--Users-me-proj--", "subagent-artifacts"))
	mustMkdir(t, filepath.Join(claudeRoot, "-Users-me-proj"))
	png := tinyPNGBase64()
	pi := `{"type":"session","version":3,"id":"pi-session","timestamp":"2026-01-01T00:00:00Z","cwd":"/Users/me/proj"}
{"id":"p1","parentId":"","type":"user","role":"user","timestamp":"2026-01-01T00:00:01Z","message":{"content":"hello needle"}}
{"id":"p2","parentId":"p1","type":"assistant","role":"assistant","timestamp":"2026-01-01T00:00:02Z","message":{"content":[{"type":"text","text":"assistant reply"}]}}
`
	claude := `{"type":"user","timestamp":"2026-01-02T00:00:01Z","message":{"content":[{"type":"text","text":"claude needle"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"` + png + `"}}]}}
{"type":"assistant","timestamp":"2026-01-02T00:00:02Z","message":{"content":[{"type":"text","text":"claude reply"}],"model":"claude-test","usage":{"input_tokens":1,"output_tokens":2}}}
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
	if err := os.WriteFile(filepath.Join(claudeRoot, "-Users-me-proj", "abc.jsonl"), []byte(claude), 0o644); err != nil {
		t.Fatal(err)
	}
	return FixtureRoots{PiRoot: piRoot, ClaudeRoot: claudeRoot}
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

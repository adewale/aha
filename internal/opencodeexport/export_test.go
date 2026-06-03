package opencodeexport

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func buildDB(t *testing.T, path string) {
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
		`INSERT INTO session VALUES ('s1', '{"directory":"/work","time":{"created":1716200000000},"title":"t"}')`,
		`INSERT INTO message VALUES ('m1', 's1', '{"role":"user","time":{"created":1716200001000}}')`,
		`INSERT INTO message VALUES ('m2', 's1', '{"role":"assistant","modelID":"oc-model","tokens":{"input":10,"output":7,"cache":{"read":1,"write":1}}}')`,
		`INSERT INTO part VALUES ('p1', 'm1', 's1', '{"type":"text","text":"hello needle"}')`,
		`INSERT INTO part VALUES ('p2', 'm2', 's1', '{"type":"text","text":"sure"}')`,
		`INSERT INTO part VALUES ('p3', 'm2', 's1', '{"type":"tool","tool":"bash","state":{"input":{"command":"ls -la"}}}')`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("exec %q: %v", s, err)
		}
	}
}

func TestRunExportsDeterministicLosslessJSONL(t *testing.T) {
	src := t.TempDir()
	dbPath := filepath.Join(src, "opencode.db")
	buildDB(t, dbPath)

	dest1 := filepath.Join(t.TempDir(), "out")
	sessions, err := Run(context.Background(), dbPath, dest1)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("got %d sessions, want 1", len(sessions))
	}
	if sessions[0].ID != "s1" || sessions[0].Messages != 2 || filepath.Base(sessions[0].Path) != "s1.jsonl" {
		t.Fatalf("unexpected session %+v", sessions[0])
	}

	got1, err := os.ReadFile(sessions[0].Path)
	if err != nil {
		t.Fatal(err)
	}

	// Determinism: a second export of identical content is byte-identical.
	dest2 := filepath.Join(t.TempDir(), "out")
	if _, err := Run(context.Background(), dbPath, dest2); err != nil {
		t.Fatal(err)
	}
	got2, err := os.ReadFile(filepath.Join(dest2, "s1.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got1) != string(got2) {
		t.Fatalf("export not deterministic:\n--- run1 ---\n%s\n--- run2 ---\n%s", got1, got2)
	}

	assertLossless(t, got1)
}

func assertLossless(t *testing.T, data []byte) {
	t.Helper()
	lines := splitLines(data)
	if len(lines) != 3 {
		t.Fatalf("got %d records, want 3 (1 session + 2 messages)", len(lines))
	}
	var session map[string]any
	if err := json.Unmarshal(lines[0], &session); err != nil {
		t.Fatal(err)
	}
	if session["type"] != "session" || session["id"] != "s1" {
		t.Fatalf("bad session record: %v", session)
	}
	// The full JSON `data` column survives the round trip.
	srow := session["row"].(map[string]any)
	sdata := srow["data"].(map[string]any)
	if sdata["directory"] != "/work" || sdata["title"] != "t" {
		t.Fatalf("session data not preserved: %v", sdata)
	}

	var asst map[string]any
	if err := json.Unmarshal(lines[2], &asst); err != nil {
		t.Fatal(err)
	}
	parts := asst["parts"].([]any)
	if len(parts) != 2 {
		t.Fatalf("assistant message lost parts: %v", parts)
	}
	tool := parts[1].(map[string]any)["data"].(map[string]any)
	if tool["tool"] != "bash" {
		t.Fatalf("tool part not preserved: %v", tool)
	}
	cmd := tool["state"].(map[string]any)["input"].(map[string]any)["command"]
	if cmd != "ls -la" {
		t.Fatalf("nested tool input not preserved: %v", cmd)
	}
}

func splitLines(b []byte) [][]byte {
	var out [][]byte
	start := 0
	for i, c := range b {
		if c == '\n' {
			if i > start {
				out = append(out, b[start:i])
			}
			start = i + 1
		}
	}
	if start < len(b) {
		out = append(out, b[start:])
	}
	return out
}

func TestRunDoesNotMutateSource(t *testing.T) {
	src := t.TempDir()
	dbPath := filepath.Join(src, "opencode.db")
	buildDB(t, dbPath)

	before := dirState(t, src)
	if _, err := Run(context.Background(), dbPath, filepath.Join(t.TempDir(), "out")); err != nil {
		t.Fatal(err)
	}
	after := dirState(t, src)
	if before != after {
		t.Fatalf("source directory changed:\nbefore=%s\nafter =%s", before, after)
	}
}

func TestRunPrunesStaleExports(t *testing.T) {
	src := t.TempDir()
	dbPath := filepath.Join(src, "opencode.db")
	buildDB(t, dbPath)
	dest := filepath.Join(t.TempDir(), "out")

	if _, err := Run(context.Background(), dbPath, dest); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(dest, "ghost.jsonl")
	if err := os.WriteFile(stale, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), dbPath, dest); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale export was not pruned: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "s1.jsonl")); err != nil {
		t.Fatalf("current export missing: %v", err)
	}
}

func dirState(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var b []byte
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			t.Fatal(err)
		}
		b = append(b, []byte(e.Name())...)
		b = append(b, ':')
		b = append(b, []byte(itoa(info.Size()))...)
		b = append(b, ';')
	}
	return string(b)
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

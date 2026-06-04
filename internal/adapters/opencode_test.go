package adapters

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adewale/aha/internal/model"
	_ "modernc.org/sqlite"
)

func mustTouch(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestOpenCodeExportDirHonorsEnvOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AHA_OPENCODE_EXPORT_DIR", dir)
	got, err := openCodeExportDir("/some/where/opencode.db")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, dir+string(filepath.Separator)) {
		t.Fatalf("export dir %q not under override base %q", got, dir)
	}
	// Same database always maps to the same directory (stable for reuse).
	again, _ := openCodeExportDir("/some/where/opencode.db")
	if got != again {
		t.Fatalf("export dir not stable: %q vs %q", got, again)
	}
	other, _ := openCodeExportDir("/some/where/opencode-beta.db")
	if other == got {
		t.Fatalf("distinct databases must map to distinct dirs")
	}
}

func TestOpenCodeDefaultRootHonorsXDGDataHome(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_DATA_HOME", xdg)
	roots := (OpenCode{}).DefaultRoots()
	if len(roots) != 1 {
		t.Fatalf("got %d roots, want 1", len(roots))
	}
	want := filepath.Join(xdg, "opencode")
	if roots[0].Path != want {
		t.Fatalf("OpenCode default root = %q, want XDG data root %q", roots[0].Path, want)
	}
}

func TestOpenCodeDBPathsResolvesDefaultAndChannel(t *testing.T) {
	root := t.TempDir()
	mustTouch(t, filepath.Join(root, "opencode.db"))
	mustTouch(t, filepath.Join(root, "opencode-beta.db"))
	mustTouch(t, filepath.Join(root, "notes.txt")) // ignored

	got := openCodeDBPaths(root)
	if len(got) != 2 {
		t.Fatalf("got %v, want the two opencode databases", got)
	}
	if filepath.Base(got[0]) != "opencode-beta.db" && filepath.Base(got[1]) != "opencode-beta.db" {
		t.Fatalf("channel database not discovered: %v", got)
	}
}

func TestOpenCodeDBPathsEnvOverridesConfiguredRoot(t *testing.T) {
	root := t.TempDir()
	overrideDir := t.TempDir()
	configured := filepath.Join(root, "opencode.db")
	override := filepath.Join(overrideDir, "custom.db")
	mustTouch(t, configured)
	mustTouch(t, override)
	t.Setenv("OPENCODE_DB", override)

	got := openCodeDBPaths(root)
	if len(got) != 1 || got[0] != override {
		t.Fatalf("OPENCODE_DB should be an exclusive override; got %v, want [%s]", got, override)
	}
}

func TestOpenCodePartsProjectToolInvocation(t *testing.T) {
	parts := []any{map[string]any{"data": map[string]any{
		"type":   "tool",
		"tool":   "bash",
		"callID": "call_1",
		"state": map[string]any{
			"status": "failed",
			"input":  map[string]any{"command": "git push origin main"},
			"output": "error: failed to push some refs",
		},
	}}}
	_, tool, command, _, _, calls, results := openCodeParts(parts)
	if tool != "bash" || command != "git push origin main" {
		t.Fatalf("legacy fields not projected: tool=%q command=%q", tool, command)
	}
	if len(calls) != 1 || calls[0].ID != "call_1" || calls[0].Command != "git push origin main" {
		t.Fatalf("bad tool call projection: %+v", calls)
	}
	if len(results) != 1 || results[0].ForID != "call_1" || !results[0].IsError || !strings.Contains(results[0].OutcomeText, "failed to push") {
		t.Fatalf("bad tool result projection: %+v", results)
	}
}

func TestOpenCodeDiscoverNamespacesDuplicateSessionIDsByDatabase(t *testing.T) {
	root := t.TempDir()
	buildMinimalOpenCodeDB(t, filepath.Join(root, "opencode.db"), "s1", "main needle")
	buildMinimalOpenCodeDB(t, filepath.Join(root, "opencode-beta.db"), "s1", "beta needle")
	t.Setenv("AHA_OPENCODE_EXPORT_DIR", t.TempDir())

	got, err := (OpenCode{}).Discover(t.Context(), model.SourceConfig{Type: "opencode", Root: root, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d sessions, want 2: %+v", len(got), got)
	}
	if got[0].RelativePath == got[1].RelativePath {
		t.Fatalf("duplicate relative paths are representable: %+v", got)
	}
	if got[0].SessionID == got[1].SessionID {
		t.Fatalf("duplicate corpus session IDs are representable: %+v", got)
	}
	for _, sf := range got {
		if !strings.HasSuffix(sf.SessionID, "/s1") {
			t.Fatalf("namespaced session id %q should preserve raw OpenCode id suffix", sf.SessionID)
		}
		if !strings.Contains(sf.RelativePath, "/") {
			t.Fatalf("relative path %q should include a database namespace directory", sf.RelativePath)
		}
	}
}

func buildMinimalOpenCodeDB(t *testing.T, path, sessionID, text string) {
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
		`INSERT INTO session VALUES (?, '{"directory":"/work"}')`,
		`INSERT INTO message VALUES (?, ?, '{"role":"user"}')`,
		`INSERT INTO part VALUES (?, ?, ?, json_object('type','text','text',?))`,
	}
	if _, err := db.Exec(stmts[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(stmts[1]); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(stmts[2]); err != nil {
		t.Fatal(err)
	}
	msgID := sessionID + "-m1"
	partID := sessionID + "-p1"
	if _, err := db.Exec(stmts[3], sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(stmts[4], msgID, sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(stmts[5], partID, msgID, sessionID, text); err != nil {
		t.Fatal(err)
	}
}

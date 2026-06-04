package corpus_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adewale/aha/internal/adapters"
	"github.com/adewale/aha/internal/archive"
	"github.com/adewale/aha/internal/config"
	"github.com/adewale/aha/internal/corpus"
	"github.com/adewale/aha/internal/model"
	"github.com/adewale/aha/internal/search"
	"github.com/adewale/aha/internal/testutil"
	_ "modernc.org/sqlite"
)

// TestEndToEndAllSources drives every supported coding agent through the full
// pipeline against the shared fake fixtures — discovery -> snapshot -> bundle
// -> ingest -> search -> read — and asserts each source's distinctive needle is
// both searchable and readable. OpenCode (SQLite, converted to JSONL during
// discovery) goes through the same path as the JSONL agents.
func TestEndToEndAllSources(t *testing.T) {
	// Keep the OpenCode JSONL export hermetic and out of the real user cache.
	t.Setenv("AHA_OPENCODE_EXPORT_DIR", t.TempDir())

	root := t.TempDir()
	fx := testutil.WriteAgentFixtures(t, root)

	cfg := config.Default()
	cfg.MachineID = "e2e"
	cfg.Sources = []model.SourceConfig{
		{Type: "pi", Root: fx.PiRoot, Enabled: true},
		{Type: "claude-code", Root: fx.ClaudeRoot, Enabled: true},
		{Type: "codex", Root: fx.CodexRoot, Enabled: true},
		{Type: "opencode", Root: fx.OpenCodeRoot, Enabled: true},
	}

	bundle, err := archive.Capture(t.Context(), cfg, adapters.Builtins(), archive.Options{CapturedAt: "2026-01-03T00:00:00Z", BundleID: "e2e"})
	if err != nil {
		t.Fatal(err)
	}
	bundlePath := filepath.Join(root, "bundle.tar.zst")
	if _, err := archive.Write(bundlePath, bundle); err != nil {
		t.Fatal(err)
	}
	store, err := corpus.Open(filepath.Join(root, "corpus"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := corpus.IngestBundle(store, adapters.Builtins(), bundlePath); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		source string
		needle string
	}{
		{"pi", "hello needle"},
		{"claude-code", "claude needle"},
		{"codex", "codex needle"},
		{"opencode", "opencode needle"},
	}
	for _, tc := range cases {
		t.Run(tc.source, func(t *testing.T) {
			results, err := search.Query(store.DB, tc.needle, search.Filters{Source: tc.source, Limit: 5})
			if err != nil {
				t.Fatalf("search %q: %v", tc.needle, err)
			}
			if len(results) == 0 {
				t.Fatalf("no search hits for %s needle %q — source did not flow through the pipeline", tc.source, tc.needle)
			}
			r := results[0]
			if r.Source != tc.source {
				t.Fatalf("hit came from source %q, want %q", r.Source, tc.source)
			}
			// The ref returned by search must read back to real context.
			entries, err := corpus.ReadRef(store.DB, r.Ref, 0, 3)
			if err != nil {
				t.Fatalf("read %s: %v", r.RefText, err)
			}
			if !entryTextContains(entries, tc.needle) {
				t.Fatalf("read of %s did not surface needle %q; got %d entries", r.RefText, tc.needle, len(entries))
			}
		})
	}

	// OpenCode-specific richness: the converted assistant turn must preserve the
	// model, token usage, and the bash tool call in normalized corpus columns.
	t.Run("opencode-metadata", func(t *testing.T) {
		var modelName, toolName, command string
		var tokens int64
		err := store.DB.QueryRow(`select coalesce(model,''), coalesce(tool_name,''), coalesce(command,''), tokens from messages where text='opencode reply'`).Scan(&modelName, &toolName, &command, &tokens)
		if err != nil {
			t.Fatal(err)
		}
		if modelName != "opencode-test" || toolName != "bash" || command != "ls -la" || tokens != 11 {
			t.Fatalf("opencode normalized metadata = model %q tool %q command %q tokens %d", modelName, toolName, command, tokens)
		}
	})
}

func TestOpenCodeDuplicateSessionIDsAcrossDatabasesStayDistinct(t *testing.T) {
	t.Setenv("AHA_OPENCODE_EXPORT_DIR", t.TempDir())
	root := t.TempDir()
	ocRoot := filepath.Join(root, "opencode")
	if err := os.MkdirAll(ocRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	writeOpenCodeDuplicateDB(t, filepath.Join(ocRoot, "opencode.db"), "dup-session", "main duplicate needle")
	writeOpenCodeDuplicateDB(t, filepath.Join(ocRoot, "opencode-beta.db"), "dup-session", "beta duplicate needle")

	cfg := config.Default()
	cfg.MachineID = "opencode-dups"
	cfg.Sources = []model.SourceConfig{{Type: "opencode", Root: ocRoot, Enabled: true}}
	store := captureWriteIngest(t, root, cfg, "opencode-dups")
	defer store.Close()

	var count int
	if err := store.DB.QueryRow(`select count(*) from sessions where source_name='opencode'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("got %d OpenCode sessions, want 2 distinct sessions", count)
	}
	rows, err := store.DB.Query(`select source_session_id from sessions where source_name='opencode' order by source_session_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0] == ids[1] {
		t.Fatalf("OpenCode duplicate raw session IDs were not namespaced: %v", ids)
	}
	for _, id := range ids {
		if !strings.HasSuffix(id, "/dup-session") {
			t.Fatalf("namespaced id %q should retain raw OpenCode id suffix", id)
		}
	}
	for _, needle := range []string{"main duplicate needle", "beta duplicate needle"} {
		results, err := search.Query(store.DB, needle, search.Filters{Source: "opencode", Limit: 5})
		if err != nil || len(results) == 0 {
			t.Fatalf("needle %q not searchable after duplicate-db ingest: hits=%d err=%v", needle, len(results), err)
		}
	}
}

func TestModernCodexToolCallsPersistMetadataWithoutText(t *testing.T) {
	root := t.TempDir()
	codexRoot := filepath.Join(root, "codex")
	if err := os.MkdirAll(codexRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	fixture, err := os.ReadFile(filepath.Join("..", "adapters", "testdata", "codex_modern_realish.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codexRoot, "rollout-modern.jsonl"), fixture, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.MachineID = "modern-codex"
	cfg.Sources = []model.SourceConfig{{Type: "codex", Root: codexRoot, Enabled: true}}
	store := captureWriteIngest(t, root, cfg, "modern-codex")
	defer store.Close()

	var toolName, command, text string
	err = store.DB.QueryRow(`select coalesce(tool_name,''), coalesce(command,''), coalesce(text,'') from messages where role='toolCall'`).Scan(&toolName, &command, &text)
	if err != nil {
		t.Fatalf("modern Codex tool call metadata was not persisted as a normalized message row: %v", err)
	}
	if toolName != "shell" || command != "bash -lc ls -la" {
		t.Fatalf("tool metadata = name %q command %q, want shell / bash -lc ls -la", toolName, command)
	}
	if text != "" {
		t.Fatalf("tool-call metadata row should not invent searchable text, got %q", text)
	}
}

func captureWriteIngest(t *testing.T, root string, cfg model.Config, bundleID string) *corpus.Store {
	t.Helper()
	bundle, err := archive.Capture(t.Context(), cfg, adapters.Builtins(), archive.Options{CapturedAt: "2026-01-03T00:00:00Z", BundleID: bundleID})
	if err != nil {
		t.Fatal(err)
	}
	bundlePath := filepath.Join(root, bundleID+".tar.zst")
	if _, err := archive.Write(bundlePath, bundle); err != nil {
		t.Fatal(err)
	}
	store, err := corpus.Open(filepath.Join(root, bundleID+"-corpus"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := corpus.IngestBundle(store, adapters.Builtins(), bundlePath); err != nil {
		store.Close()
		t.Fatal(err)
	}
	return store
}

func writeOpenCodeDuplicateDB(t *testing.T, path, sessionID, text string) {
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
		`INSERT INTO session VALUES (?, '{"directory":"/Users/me/dup"}')`,
		`INSERT INTO message VALUES (?, ?, '{"role":"user"}')`,
		`INSERT INTO part VALUES (?, ?, ?, json_object('type','text','text',?))`,
	}
	for i := 0; i < 3; i++ {
		if _, err := db.Exec(stmts[i]); err != nil {
			t.Fatal(err)
		}
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

func entryTextContains(entries []corpus.ReadEntry, needle string) bool {
	for _, e := range entries {
		if strings.Contains(e.Text, needle) {
			return true
		}
	}
	return false
}

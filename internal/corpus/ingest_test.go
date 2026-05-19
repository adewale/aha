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
)

func makeBundle(t *testing.T, root string) (string, string) {
	t.Helper()
	fx := testutil.WriteAgentFixtures(t, root)
	cfg := config.Default()
	cfg.MachineID = "test-machine"
	cfg.Sources = []model.SourceConfig{{Type: "pi", Root: fx.PiRoot, Enabled: true}, {Type: "claude-code", Root: fx.ClaudeRoot, Enabled: true}}
	b, err := archive.Capture(t.Context(), cfg, adapters.Builtins(), archive.Options{CapturedAt: "2026-01-03T00:00:00Z", BundleID: "fixed"})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "bundle.tar.zst")
	if _, err := archive.Write(path, b); err != nil {
		t.Fatal(err)
	}
	return path, filepath.Join(root, "corpus")
}

func TestIngestIdempotentSearchReadAndImages(t *testing.T) {
	root := t.TempDir()
	bundle, corpusDir := makeBundle(t, root)
	store, err := corpus.Open(corpusDir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	rep, err := corpus.IngestBundle(store, adapters.Builtins(), bundle)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Sessions != 2 || rep.Messages != 4 || rep.Images != 1 || rep.Artifacts != 2 {
		t.Fatalf("bad first ingest: %+v", rep)
	}
	rep, err = corpus.IngestBundle(store, adapters.Builtins(), bundle)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Duplicate {
		t.Fatalf("second ingest should be duplicate: %+v", rep)
	}
	assertCount(t, store.DB, "bundles", 1)
	assertCount(t, store.DB, "sessions", 2)
	assertCount(t, store.DB, "messages", 4)
	assertCount(t, store.DB, "images", 1)
	assertCount(t, store.DB, "entry_assets", 1)
	var parent string
	if err := store.DB.QueryRow(`select parent_session_key from artifacts where parent_session_key is not null`).Scan(&parent); err != nil {
		t.Fatal(err)
	}
	if parent != "pi:test-machine:pi-session" {
		t.Fatalf("artifact parent=%q", parent)
	}
	var unlinked int
	if err := store.DB.QueryRow(`select count(*) from artifacts where parent_session_key is null`).Scan(&unlinked); err != nil {
		t.Fatal(err)
	}
	if unlinked != 1 {
		t.Fatalf("unlinked artifact count=%d", unlinked)
	}
	var width, height int
	if err := store.DB.QueryRow(`select width,height from images`).Scan(&width, &height); err != nil {
		t.Fatal(err)
	}
	if width != 1 || height != 1 {
		t.Fatalf("image dimensions = %dx%d, want 1x1", width, height)
	}
	results, err := search.Query(store.DB, "needle", search.Filters{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) < 3 {
		t.Fatalf("expected message and artifact hits, got %+v", results)
	}
	ctx, err := corpus.ReadContext(store.DB, "abc", "", 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(ctx) == 0 || !strings.Contains(ctx[0].Text, "claude needle") {
		t.Fatalf("bad read context: %+v", ctx)
	}
	var artifactSHA string
	if err := store.DB.QueryRow(`select artifact_sha256 from artifacts limit 1`).Scan(&artifactSHA); err != nil {
		t.Fatal(err)
	}
	artifactCtx, err := corpus.ReadContext(store.DB, "pi-session", artifactSHA, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(artifactCtx) != 1 || !strings.Contains(artifactCtx[0].Text, "artifact needle") {
		t.Fatalf("bad artifact read context: %+v", artifactCtx)
	}
	unlinkedResults, err := search.Query(store.DB, "unlinked", search.Filters{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(unlinkedResults) != 1 || !strings.HasPrefix(unlinkedResults[0].SessionKey, "artifact:") {
		t.Fatalf("bad unlinked artifact search: %+v", unlinkedResults)
	}
	unlinkedCtx, err := corpus.ReadContext(store.DB, unlinkedResults[0].SessionKey, unlinkedResults[0].EntryID, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(unlinkedCtx) != 1 || !strings.Contains(unlinkedCtx[0].Text, "unlinked artifact") {
		t.Fatalf("bad unlinked artifact read: %+v", unlinkedCtx)
	}
}

func TestLaterBundleAppendOnlyMerge(t *testing.T) {
	root := t.TempDir()
	fx := testutil.WriteAgentFixtures(t, root)
	corpusDir := filepath.Join(root, "corpus")
	bundle1 := writeBundleFromRoots(t, root, fx, "test-machine", "b1")
	store, err := corpus.Open(corpusDir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := corpus.IngestBundle(store, adapters.Builtins(), bundle1); err != nil {
		t.Fatal(err)
	}
	piFile := filepath.Join(fx.PiRoot, "--Users-me-proj--", "2026_pi.jsonl")
	f, err := os.OpenFile(piFile, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString(`{"id":"p3","parentId":"p2","type":"user","role":"user","timestamp":"2026-01-01T00:00:03Z","message":{"content":"new appended needle"}}` + "\n")
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}
	bundle2 := writeBundleFromRoots(t, root, fx, "test-machine", "b2")
	if _, err := corpus.IngestBundle(store, adapters.Builtins(), bundle2); err != nil {
		t.Fatal(err)
	}
	assertCount(t, store.DB, "sessions", 2)
	var versions int
	if err := store.DB.QueryRow(`select count(*) from session_versions where session_key='pi:test-machine:pi-session'`).Scan(&versions); err != nil {
		t.Fatal(err)
	}
	if versions != 2 {
		t.Fatalf("session_versions=%d want 2", versions)
	}
	var text string
	if err := store.DB.QueryRow(`select text from messages where entry_id='p3'`).Scan(&text); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "new appended") {
		t.Fatalf("appended text missing: %q", text)
	}
}

func TestConflictQuarantineFromBundle(t *testing.T) {
	root := t.TempDir()
	fx := testutil.WriteAgentFixtures(t, root)
	bundle1 := writeBundleFromRoots(t, root, fx, "test-machine", "b1")
	store, err := corpus.Open(filepath.Join(root, "corpus"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := corpus.IngestBundle(store, adapters.Builtins(), bundle1); err != nil {
		t.Fatal(err)
	}
	piFile := filepath.Join(fx.PiRoot, "--Users-me-proj--", "2026_pi.jsonl")
	data, err := os.ReadFile(piFile)
	if err != nil {
		t.Fatal(err)
	}
	changed := strings.Replace(string(data), `"hello needle"`, `"changed conflicting needle"`, 1)
	if err := os.WriteFile(piFile, []byte(changed), 0o644); err != nil {
		t.Fatal(err)
	}
	bundle2 := writeBundleFromRoots(t, root, fx, "test-machine", "b2")
	if _, err := corpus.IngestBundle(store, adapters.Builtins(), bundle2); err != nil {
		t.Fatal(err)
	}
	assertCount(t, store.DB, "conflicts", 1)
	var text string
	if err := store.DB.QueryRow(`select text from messages where entry_id='p1'`).Scan(&text); err != nil {
		t.Fatal(err)
	}
	if text != "hello needle" {
		t.Fatalf("original message overwritten: %q", text)
	}
}

func TestMalformedDiagnosticsPersistAndToolOutputNotIndexed(t *testing.T) {
	root := t.TempDir()
	fx := testutil.WriteAgentFixtures(t, root)
	piFile := filepath.Join(fx.PiRoot, "--Users-me-proj--", "2026_pi.jsonl")
	f, err := os.OpenFile(piFile, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString("{bad json\n" + `{"id":"tool1","type":"toolResult","role":"toolResult","message":{"content":"secret-tool-output-never-index"}}` + "\n")
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}
	bundle := writeBundleFromRoots(t, root, fx, "test-machine", "diag")
	store, err := corpus.Open(filepath.Join(root, "corpus"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := corpus.IngestBundle(store, adapters.Builtins(), bundle); err != nil {
		t.Fatal(err)
	}
	var metadata string
	if err := store.DB.QueryRow(`select source_metadata_json from sessions where source_name='pi'`).Scan(&metadata); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(metadata, "diagnostics") {
		t.Fatalf("diagnostics missing: %s", metadata)
	}
	results, err := search.Query(store.DB, "secret-tool-output-never-index", search.Filters{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("tool output should not be indexed: %+v", results)
	}
}

func TestDuplicateBundleIDDifferentSHAErrors(t *testing.T) {
	root := t.TempDir()
	fx := testutil.WriteAgentFixtures(t, root)
	bundle1 := writeBundleFromRoots(t, root, fx, "test-machine", "same-id")
	store, err := corpus.Open(filepath.Join(root, "corpus"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := corpus.IngestBundle(store, adapters.Builtins(), bundle1); err != nil {
		t.Fatal(err)
	}
	piFile := filepath.Join(fx.PiRoot, "--Users-me-proj--", "2026_pi.jsonl")
	f, err := os.OpenFile(piFile, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString(`{"id":"p99","type":"user","role":"user","message":{"content":"different sha"}}` + "\n")
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}
	bundle2 := writeBundleFromRoots(t, root, fx, "test-machine", "same-id")
	if _, err := corpus.IngestBundle(store, adapters.Builtins(), bundle2); err == nil {
		t.Fatalf("expected duplicate bundle_id with different sha to error")
	}
}

func TestCrossMachineConflict(t *testing.T) {
	root := t.TempDir()
	fx := testutil.WriteAgentFixtures(t, root)
	bundle1 := writeBundleFromRoots(t, root, fx, "machine-a", "b1")
	store, err := corpus.Open(filepath.Join(root, "corpus"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := corpus.IngestBundle(store, adapters.Builtins(), bundle1); err != nil {
		t.Fatal(err)
	}
	piFile := filepath.Join(fx.PiRoot, "--Users-me-proj--", "2026_pi.jsonl")
	data, err := os.ReadFile(piFile)
	if err != nil {
		t.Fatal(err)
	}
	changed := strings.Replace(string(data), `"hello needle"`, `"other machine divergent"`, 1)
	if err := os.WriteFile(piFile, []byte(changed), 0o644); err != nil {
		t.Fatal(err)
	}
	bundle2 := writeBundleFromRoots(t, root, fx, "machine-b", "b2")
	if _, err := corpus.IngestBundle(store, adapters.Builtins(), bundle2); err != nil {
		t.Fatal(err)
	}
	assertCount(t, store.DB, "conflicts", 1)
}

func writeBundleFromRoots(t *testing.T, root string, fx testutil.FixtureRoots, machine, bundleID string) string {
	t.Helper()
	cfg := config.Default()
	cfg.MachineID = machine
	cfg.Sources = []model.SourceConfig{{Type: "pi", Root: fx.PiRoot, Enabled: true}, {Type: "claude-code", Root: fx.ClaudeRoot, Enabled: true}}
	b, err := archive.Capture(t.Context(), cfg, adapters.Builtins(), archive.Options{CapturedAt: "2026-01-03T00:00:00Z", BundleID: bundleID})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, bundleID+".tar.zst")
	if _, err := archive.Write(path, b); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertCount(t *testing.T, db *sql.DB, table string, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow("select count(*) from " + table).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("%s count=%d want %d", table, got, want)
	}
}

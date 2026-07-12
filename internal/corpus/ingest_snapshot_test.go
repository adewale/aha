package corpus_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/adewale/aha/internal/adapters"
	"github.com/adewale/aha/internal/corpus"
	"github.com/adewale/aha/internal/hash"
	"github.com/adewale/aha/internal/model"
)

// snapshotFixture builds a v2 snapshot manifest plus a counting blob
// opener over the given (sessionID -> content) state, using the counting
// adapter from the performance contract tests.
func snapshotFixture(state map[string][]byte) (model.SnapshotManifest, *countingBlobOpener) {
	var files []model.ManifestFile
	opener := &countingBlobOpener{byKey: map[string][]byte{}}
	for id, data := range state {
		mf := model.ManifestFile{Source: "counting", Kind: "session", RelativePath: "sources/counting/sessions/" + id + ".jsonl", RawPath: id + ".jsonl", SHA256: hash.SHA256Bytes(data), Bytes: int64(len(data)), SessionID: id, CWD: "/repo/project", StartedAt: "2026-01-01T00:00:00Z", CopyState: "stable"}
		files = append(files, mf)
		opener.byKey[mf.SHA256] = data
	}
	manifest := model.SnapshotManifest{
		Schema:     model.SnapshotManifestSchema,
		MachineID:  "machine",
		CapturedAt: "2026-01-01T00:00:00Z",
		CreatedBy:  "aha test",
		Policy:     model.ManifestPolicy{PathMode: "raw", IncludeSubagents: true, IncludeImages: true, Redaction: "none-v1"},
		Adapters:   []model.ManifestAdapt{{Name: "counting", Version: "test"}},
		Files:      files,
	}
	return manifest, opener
}

type countingBlobOpener struct {
	byKey   map[string][]byte
	fetches atomic.Int64
}

func (o *countingBlobOpener) Open(key model.BlobKey) (io.ReadCloser, error) {
	o.fetches.Add(1)
	b, ok := o.byKey[key.String()]
	if !ok {
		return nil, fmt.Errorf("no blob %s", key)
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (r cancelOnClose) Close() error {
	err := r.ReadCloser.Close()
	r.cancel()
	return err
}

func TestIngestSnapshotCancellationBeforeCommitRollsBack(t *testing.T) {
	store, err := corpus.Open(filepath.Join(t.TempDir(), "corpus"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	manifest, opener := snapshotFixture(map[string][]byte{"alpha": []byte("session alpha bytes")})
	registry, _ := countingRegistry()
	ctx, cancel := context.WithCancel(t.Context())
	ing := corpus.NewIngestor(store, registry)
	ing.Context = ctx
	_, err = ing.IngestSnapshot(manifest, func(key model.BlobKey) (io.ReadCloser, error) {
		rc, err := opener.Open(key)
		if err != nil {
			return nil, err
		}
		return cancelOnClose{ReadCloser: rc, cancel: cancel}, nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("IngestSnapshot error=%v want context.Canceled", err)
	}
	var snapshots int
	if err := store.DB.QueryRow(`select count(*) from snapshots`).Scan(&snapshots); err != nil {
		t.Fatal(err)
	}
	if snapshots != 0 {
		t.Fatalf("snapshots=%d want 0 after cancelled ingest", snapshots)
	}
}

func countingRegistry() (map[string]adapters.SourceAdapter, *atomic.Int64) {
	calls := &atomic.Int64{}
	return map[string]adapters.SourceAdapter{"counting": countingAdapter{calls: calls}}, calls
}

// TestIngestSnapshotRoundTrip pins the v2 pull path: a snapshot manifest
// plus blob fetches produce the same corpus rows bundle ingest would, and
// the snapshot is recorded under its manifest identity.
func TestIngestSnapshotRoundTrip(t *testing.T) {
	store, err := corpus.Open(filepath.Join(t.TempDir(), "corpus"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	manifest, opener := snapshotFixture(map[string][]byte{"alpha": []byte("session alpha bytes")})
	registry, _ := countingRegistry()
	_, sha, err := model.EncodeSnapshotManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := corpus.NewIngestor(store, registry).IngestSnapshot(manifest, opener.Open)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Sessions != 1 || rep.Entries != 1 || rep.Duplicate {
		t.Fatalf("report: %+v", rep)
	}
	var count int
	if err := store.DB.QueryRow(`select count(*) from snapshots where manifest_sha256=?`, sha.String()).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("snapshots rows=%d want 1", count)
	}
	var text string
	if err := store.DB.QueryRow(`select text from messages where entry_id='entry-1'`).Scan(&text); err != nil {
		t.Fatal(err)
	}
	if text != "needle" {
		t.Fatalf("message text=%q", text)
	}
}

// TestIngestSnapshotFetchesOnlyUnknownBlobs pins acceptance property 2 on
// the pull side: a grown snapshot fetches (and parses) only content the
// corpus does not already prove present — anti-entropy, not replay.
func TestIngestSnapshotFetchesOnlyUnknownBlobs(t *testing.T) {
	store, err := corpus.Open(filepath.Join(t.TempDir(), "corpus"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	registry, parses := countingRegistry()
	ing := corpus.NewIngestor(store, registry)

	manifestA, openerA := snapshotFixture(map[string][]byte{"alpha": []byte("alpha content")})
	if _, err := ing.IngestSnapshot(manifestA, openerA.Open); err != nil {
		t.Fatal(err)
	}
	if got := openerA.fetches.Load(); got != 1 {
		t.Fatalf("first snapshot fetched %d blobs, want 1", got)
	}
	manifestB, openerB := snapshotFixture(map[string][]byte{"alpha": []byte("alpha content"), "beta": []byte("beta content")})
	if _, err := ing.IngestSnapshot(manifestB, openerB.Open); err != nil {
		t.Fatal(err)
	}
	if got := openerB.fetches.Load(); got != 1 {
		t.Fatalf("grown snapshot fetched %d blobs, want only the new one", got)
	}
	if got := parses.Load(); got != 2 {
		t.Fatalf("ParseSession calls=%d, want 2 (one per unique file version)", got)
	}
	var versions int
	if err := store.DB.QueryRow(`select count(*) from session_versions`).Scan(&versions); err != nil {
		t.Fatal(err)
	}
	if versions != 3 {
		t.Fatalf("session_versions=%d want 3 (alpha twice for provenance, beta once)", versions)
	}
}

// TestIngestSnapshotDuplicateIsZeroWork pins that re-ingesting an
// already-known snapshot identity does nothing: no fetches, no parses.
func TestIngestSnapshotDuplicateIsZeroWork(t *testing.T) {
	store, err := corpus.Open(filepath.Join(t.TempDir(), "corpus"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	registry, parses := countingRegistry()
	ing := corpus.NewIngestor(store, registry)
	manifest, opener := snapshotFixture(map[string][]byte{"alpha": []byte("alpha content")})
	if _, err := ing.IngestSnapshot(manifest, opener.Open); err != nil {
		t.Fatal(err)
	}
	before := opener.fetches.Load()
	rep, err := ing.IngestSnapshot(manifest, opener.Open)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Duplicate {
		t.Fatalf("duplicate snapshot not reported: %+v", rep)
	}
	if got := opener.fetches.Load(); got != before {
		t.Fatalf("duplicate ingest fetched %d more blobs", got-before)
	}
	if got := parses.Load(); got != 1 {
		t.Fatalf("duplicate ingest parsed sessions: calls=%d", got)
	}
}

// TestIngestSnapshotEquivalentToBundleIngest pins acceptance property 4:
// a corpus built by pulling a v2 snapshot equals a corpus built by
// importing a v1 bundle of the same captured state.
func TestIngestSnapshotEquivalentToBundleIngest(t *testing.T) {
	data := []byte("equivalence session bytes")
	registryA, _ := countingRegistry()
	registryB, _ := countingRegistry()

	bundleStore, err := corpus.Open(filepath.Join(t.TempDir(), "corpus-bundle"))
	if err != nil {
		t.Fatal(err)
	}
	defer bundleStore.Close()
	bundlePath := writeCountingBundleSessions(t, t.TempDir(), "equiv-bundle", map[string][]byte{"alpha": data})
	if _, err := corpus.IngestBundle(bundleStore, registryA, bundlePath); err != nil {
		t.Fatal(err)
	}

	snapStore, err := corpus.Open(filepath.Join(t.TempDir(), "corpus-snap"))
	if err != nil {
		t.Fatal(err)
	}
	defer snapStore.Close()
	manifest, opener := snapshotFixture(map[string][]byte{"alpha": data})
	if _, err := corpus.NewIngestor(snapStore, registryB).IngestSnapshot(manifest, opener.Open); err != nil {
		t.Fatal(err)
	}

	a := queryableFacts(t, bundleStore.DB)
	b := queryableFacts(t, snapStore.DB)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("bundle-built and snapshot-built corpora differ:\nbundle:   %#v\nsnapshot: %#v", a, b)
	}
}

// queryableFacts extracts the user-visible facts of a corpus: row counts
// of every content table plus the searchable message texts.
func queryableFacts(t *testing.T, db *sql.DB) map[string]any {
	t.Helper()
	out := map[string]any{}
	for _, table := range []string{"machines", "sources", "files", "sessions", "session_versions", "entries", "messages", "artifacts", "images", "conflicts", "tool_invocations", "fts_messages"} {
		var n int
		if err := db.QueryRow("select count(*) from " + table).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		out[table] = n
	}
	rows, err := db.Query(`select text from messages order by text`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var texts []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		texts = append(texts, s)
	}
	out["texts"] = texts
	return out
}

// TestOpenRejectsLegacyBundleCorpus pins the no-migration decision: a
// corpus created by the pre-v2 schema (bundles table) is rejected at open
// with a rebuild instruction instead of being silently mixed.
func TestOpenRejectsLegacyBundleCorpus(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "corpus")
	store, err := corpus.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.Exec(`create table bundles(bundle_id text primary key, bundle_sha256 text)`); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := corpus.Open(dir); err == nil || !containsAll(err.Error(), "workspace repair") {
		t.Fatalf("Open accepted a legacy corpus: err=%v", err)
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !bytes.Contains([]byte(s), []byte(sub)) {
			return false
		}
	}
	return true
}

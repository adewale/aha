package corpus_test

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/adewale/aha/internal/adapters"
	"github.com/adewale/aha/internal/archive"
	"github.com/adewale/aha/internal/corpus"
	"github.com/adewale/aha/internal/hash"
	"github.com/adewale/aha/internal/model"
)

func TestIngestPassesCancellationContextToSessionParser(t *testing.T) {
	path, _, _ := writeCountingBundle(t, t.TempDir(), "cancellation-contract", []byte("session"))
	store, err := corpus.Open(filepath.Join(t.TempDir(), "corpus"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx, cancel := context.WithCancel(t.Context())
	ing := corpus.NewIngestor(store, map[string]adapters.SourceAdapter{"counting": cancellingAdapter{cancel: cancel}})
	ing.Context = ctx
	if _, err := ing.IngestBundle(path); !errors.Is(err, context.Canceled) {
		t.Fatalf("IngestBundle error=%v want context.Canceled", err)
	}
}

func TestDuplicateBundleSkipsSessionParsing(t *testing.T) {
	path, registry, calls := writeCountingBundle(t, t.TempDir(), "duplicate-contract", []byte("session"))
	store, err := corpus.Open(filepath.Join(t.TempDir(), "corpus"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := corpus.IngestBundle(store, registry, path); err != nil {
		t.Fatal(err)
	}
	if _, err := corpus.IngestBundle(store, registry, path); err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("ParseSession calls=%d, want 1; duplicate bundles must skip entry work", got)
	}
}

// TestUnchangedSessionFileSkipsReparseAcrossBundles pins the per-file
// parse skip: when a NEW bundle (different bundle ID, different bundle
// SHA) from the same machine contains a session file whose bytes were
// already ingested, the parse must be skipped — only genuinely new
// files cost parse work. This is what keeps refresh O(delta): without
// it, every grown full-state bundle re-parses the entire history, and
// refresh re-parses its own just-pushed bundle.
func TestUnchangedSessionFileSkipsReparseAcrossBundles(t *testing.T) {
	root := t.TempDir()
	unchanged := []byte("unchanged session bytes")
	calls := &atomic.Int64{}
	registry := map[string]adapters.SourceAdapter{"counting": countingAdapter{calls: calls}}
	b1 := writeCountingBundleSessions(t, root, "skip-reparse-b1", map[string][]byte{"alpha": unchanged})
	b2 := writeCountingBundleSessions(t, root, "skip-reparse-b2", map[string][]byte{"alpha": unchanged, "beta": []byte("new session bytes")})
	store, err := corpus.Open(filepath.Join(t.TempDir(), "corpus"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := corpus.IngestBundle(store, registry, b1); err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("ParseSession calls after first bundle=%d, want 1", got)
	}
	if _, err := corpus.IngestBundle(store, registry, b2); err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("ParseSession calls after grown bundle=%d, want 2; the unchanged file must not be re-parsed", got)
	}
	var versions int
	if err := store.DB.QueryRow(`select count(*) from session_versions sv where sv.file_sha256=?`, hash.SHA256Bytes(unchanged)).Scan(&versions); err != nil {
		t.Fatal(err)
	}
	if versions != 2 {
		t.Fatalf("session_versions rows for unchanged file=%d, want 2; skipping the parse must still record bundle provenance", versions)
	}
}

// TestIdenticalBytesDifferentSessionStillParsed guards the skip's
// residual risk: byte-identical files belonging to DIFFERENT sessions
// (different path, different session ID) on the same machine must each
// be parsed — the skip key is (machine, source, path, content), never
// content alone.
func TestIdenticalBytesDifferentSessionStillParsed(t *testing.T) {
	root := t.TempDir()
	same := []byte("twin session bytes")
	calls := &atomic.Int64{}
	registry := map[string]adapters.SourceAdapter{"counting": countingAdapter{calls: calls}}
	b1 := writeCountingBundleSessions(t, root, "twin-bytes-b1", map[string][]byte{"alpha": same})
	b2 := writeCountingBundleSessions(t, root, "twin-bytes-b2", map[string][]byte{"alpha": same, "beta": same})
	store, err := corpus.Open(filepath.Join(t.TempDir(), "corpus"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := corpus.IngestBundle(store, registry, b1); err != nil {
		t.Fatal(err)
	}
	if _, err := corpus.IngestBundle(store, registry, b2); err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("ParseSession calls=%d, want 2; identical bytes under a different session must still be parsed", got)
	}
	var sessions int
	if err := store.DB.QueryRow(`select count(*) from sessions where source_name='counting'`).Scan(&sessions); err != nil {
		t.Fatal(err)
	}
	if sessions != 2 {
		t.Fatalf("sessions=%d, want 2", sessions)
	}
}

func TestKnownFileBlobSkipsRecompression(t *testing.T) {
	data := []byte("session bytes")
	path, registry, _ := writeCountingBundle(t, t.TempDir(), "known-file-contract", data)
	store, err := corpus.Open(filepath.Join(t.TempDir(), "corpus"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	sha := hash.SHA256Bytes(data)
	blobRel := filepath.ToSlash(filepath.Join("blobs", "files", sha+".zst"))
	blobPath := filepath.Join(store.Root, filepath.FromSlash(blobRel))
	if err := os.MkdirAll(filepath.Dir(blobPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blobPath, []byte("sentinel-existing-blob"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.Exec(`insert into snapshots(manifest_sha256,machine_id,captured_at,ingested_at,manifest_json) values(?,?,?,?,?)`, strings.Repeat("a", 64), "m", "2026-01-01T00:00:00Z", "2026-01-01T00:00:01Z", `{}`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.Exec(`insert into files(file_sha256,kind,bytes,compressed_blob_path,first_seen_manifest_sha256) values(?,?,?,?,?)`, sha, "session", len(data), blobRel, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	if _, err := corpus.IngestBundle(store, registry, path); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(blobPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "sentinel-existing-blob" {
		t.Fatalf("known file blob was rewritten/recompressed: %q", string(got))
	}
}

func writeCountingBundle(t *testing.T, root, bundleID string, data []byte) (string, map[string]adapters.SourceAdapter, *atomic.Int64) {
	t.Helper()
	calls := &atomic.Int64{}
	path := writeCountingBundleSessions(t, root, bundleID, map[string][]byte{"session": data})
	return path, map[string]adapters.SourceAdapter{"counting": countingAdapter{calls: calls}}, calls
}

// writeCountingBundleSessions writes a bundle for machine "machine"
// containing one session file per (sessionID -> bytes) pair.
func writeCountingBundleSessions(t *testing.T, root, bundleID string, sessions map[string][]byte) string {
	t.Helper()
	ids := make([]string, 0, len(sessions))
	for id := range sessions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var mfs []model.ManifestFile
	var captured []model.CapturedFile
	var totalBytes int64
	for _, id := range ids {
		data := sessions[id]
		mf := model.ManifestFile{Source: "counting", Kind: "session", RelativePath: "sources/counting/sessions/" + id + ".jsonl", RawPath: id + ".jsonl", SHA256: hash.SHA256Bytes(data), Bytes: int64(len(data)), SessionID: id, CWD: "/repo/project", StartedAt: "2026-01-01T00:00:00Z", Entries: 1, CopyState: "stable"}
		mfs = append(mfs, mf)
		captured = append(captured, model.CapturedFile{Manifest: mf, Data: data})
		totalBytes += int64(len(data))
	}
	manifest := model.Manifest{Schema: model.BundleSchema, BundleID: bundleID, MachineID: "machine", CapturedAt: "2026-01-01T00:00:00Z", Policy: model.ManifestPolicy{PathMode: "raw", IncludeSubagents: true, IncludeImages: true, Redaction: "none-v1"}, Counts: model.ManifestCounts{SessionFiles: len(mfs), BytesUncompressed: totalBytes}, Adapters: []model.ManifestAdapt{{Name: "counting", Version: "test"}}, Files: mfs}
	path := filepath.Join(root, bundleID+".tar.zst")
	if _, err := archive.Write(path, archive.Bundle{Manifest: manifest, Files: captured}); err != nil {
		t.Fatal(err)
	}
	return path
}

type cancellingAdapter struct{ cancel context.CancelFunc }

func (a cancellingAdapter) Name() string                      { return "counting" }
func (a cancellingAdapter) Version() string                   { return "test" }
func (a cancellingAdapter) DefaultRoots() []model.DefaultRoot { return nil }
func (a cancellingAdapter) Capabilities() model.AdapterCapabilities {
	return model.AdapterCapabilities{}
}
func (a cancellingAdapter) Discover(context.Context, model.SourceConfig) ([]model.SessionFile, error) {
	return nil, nil
}
func (a cancellingAdapter) DiscoverArtifacts(context.Context, model.SessionFile) ([]model.ArtifactFile, error) {
	return nil, nil
}
func (a cancellingAdapter) ParseSession(ctx context.Context, _ model.SessionFile, _ io.Reader) (*model.ParsedSession, error) {
	a.cancel()
	<-ctx.Done()
	return nil, ctx.Err()
}

type countingAdapter struct{ calls *atomic.Int64 }

func (a countingAdapter) Name() string                            { return "counting" }
func (a countingAdapter) Version() string                         { return "test" }
func (a countingAdapter) DefaultRoots() []model.DefaultRoot       { return nil }
func (a countingAdapter) Capabilities() model.AdapterCapabilities { return model.AdapterCapabilities{} }
func (a countingAdapter) Discover(context.Context, model.SourceConfig) ([]model.SessionFile, error) {
	return nil, nil
}
func (a countingAdapter) DiscoverArtifacts(context.Context, model.SessionFile) ([]model.ArtifactFile, error) {
	return nil, nil
}
func (a countingAdapter) ParseSession(ctx context.Context, file model.SessionFile, r io.Reader) (*model.ParsedSession, error) {
	a.calls.Add(1)
	_, _ = io.Copy(io.Discard, r)
	return &model.ParsedSession{Source: "counting", SourceSessionID: file.SessionID, CWD: "/repo/project", StartedAt: "2026-01-01T00:00:00Z", Metadata: map[string]any{}, Entries: []model.ParsedEntry{{EntryID: "entry-1", LineNo: 1, EntryType: "message", Timestamp: "2026-01-01T00:00:01Z", Role: "user", RawJSON: `{"id":"entry-1"}`, Text: "needle"}}}, nil
}

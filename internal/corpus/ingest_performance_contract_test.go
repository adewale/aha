package corpus_test

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/adewale/aha/internal/adapters"
	"github.com/adewale/aha/internal/archive"
	"github.com/adewale/aha/internal/corpus"
	"github.com/adewale/aha/internal/hash"
	"github.com/adewale/aha/internal/model"
)

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
	if _, err := store.DB.Exec(`insert into bundles(bundle_id,bundle_sha256,machine_id,captured_at,ingested_at,manifest_json) values(?,?,?,?,?,?)`, "existing-file-owner", strings.Repeat("a", 64), "m", "2026-01-01T00:00:00Z", "2026-01-01T00:00:01Z", `{}`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.Exec(`insert into files(file_sha256,kind,bytes,compressed_blob_path,first_seen_bundle_id) values(?,?,?,?,?)`, sha, "session", len(data), blobRel, "existing-file-owner"); err != nil {
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
	mf := model.ManifestFile{Source: "counting", Kind: "session", RelativePath: "sources/counting/sessions/session.jsonl", RawPath: "session.jsonl", SHA256: hash.SHA256Bytes(data), Bytes: int64(len(data)), SessionID: "session", CWD: "/repo/project", StartedAt: "2026-01-01T00:00:00Z", Entries: 1, CopyState: "stable"}
	manifest := model.Manifest{Schema: model.BundleSchema, BundleID: bundleID, MachineID: "machine", CapturedAt: "2026-01-01T00:00:00Z", Policy: model.ManifestPolicy{PathMode: "raw", IncludeSubagents: true, IncludeImages: true, Redaction: "none-v1"}, Counts: model.ManifestCounts{SessionFiles: 1, BytesUncompressed: int64(len(data))}, Adapters: []model.ManifestAdapt{{Name: "counting", Version: "test"}}, Files: []model.ManifestFile{mf}}
	path := filepath.Join(root, bundleID+".tar.zst")
	if _, err := archive.Write(path, archive.Bundle{Manifest: manifest, Files: []model.CapturedFile{{Manifest: mf, Data: data}}}); err != nil {
		t.Fatal(err)
	}
	return path, map[string]adapters.SourceAdapter{"counting": countingAdapter{calls: calls}}, calls
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
	return &model.ParsedSession{Source: "counting", SourceSessionID: "session", CWD: "/repo/project", StartedAt: "2026-01-01T00:00:00Z", Metadata: map[string]any{}, Entries: []model.ParsedEntry{{EntryID: "entry-1", LineNo: 1, EntryType: "message", Timestamp: "2026-01-01T00:00:01Z", Role: "user", RawJSON: `{"id":"entry-1"}`, Text: "needle"}}}, nil
}

package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/adewale/aha/internal/corpus"
	"github.com/adewale/aha/internal/depot"
	"github.com/adewale/aha/internal/hash"
	"github.com/adewale/aha/internal/model"
)

// TestDepotBehindV2CountsMachinesWithUningestedSnapshots pins the
// status --depot comparison: a machine whose latest snapshot identity is
// in the corpus is current; one whose identity is unknown counts as
// behind. The comparison is metadata-only (index + pointers).
func TestDepotBehindV2CountsMachinesWithUningestedSnapshots(t *testing.T) {
	ctx := context.Background()
	v2, err := depot.NewLocalV2(filepath.Join(t.TempDir(), "depot"))
	if err != nil {
		t.Fatal(err)
	}
	if err := v2.Init(ctx); err != nil {
		t.Fatal(err)
	}
	push := func(machine, content string) model.ManifestSHA256 {
		t.Helper()
		dir := t.TempDir()
		src := filepath.Join(dir, "s.jsonl")
		if err := os.WriteFile(src, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		manifest := model.SnapshotManifest{
			Schema:     model.SnapshotManifestSchema,
			MachineID:  machine,
			CapturedAt: "2026-06-09T00:00:00Z",
			Policy:     model.ManifestPolicy{PathMode: "raw", IncludeSubagents: true, IncludeImages: true, Redaction: "none-v1"},
			Files:      []model.ManifestFile{{Source: "pi", Kind: "session", RelativePath: "sources/pi/sessions/s.jsonl", RawPath: src, SHA256: hash.SHA256Bytes([]byte(content)), Bytes: int64(len(content)), SessionID: "s", CopyState: "stable"}},
		}
		res, err := depot.PushV2(ctx, v2, manifest, pathBlobSource{path: src, sha: hash.SHA256Bytes([]byte(content))})
		if err != nil {
			t.Fatal(err)
		}
		return res.ManifestSHA256()
	}
	shaA := push("mach-a", "content a")
	push("mach-b", "content b")

	store, err := corpus.Open(filepath.Join(t.TempDir(), "corpus"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	// Mark mach-a's snapshot as ingested.
	if _, err := store.DB.Exec(`insert into snapshots(manifest_sha256,machine_id,captured_at,ingested_at,manifest_json) values(?,?,?,?,?)`, shaA.String(), "mach-a", "2026", "2026", "{}"); err != nil {
		t.Fatal(err)
	}
	report, err := depotBehindV2FromDepot(store, v2)
	if err != nil {
		t.Fatal(err)
	}
	if report.Machines != 2 || report.Behind != 1 {
		t.Fatalf("behind report=%+v, want machines=2 behind=1", report)
	}
}

type pathBlobSource struct {
	path string
	sha  string
}

func (s pathBlobSource) BlobPath(key model.BlobKey) (string, error) {
	if key.String() != s.sha {
		return "", os.ErrNotExist
	}
	return s.path, nil
}

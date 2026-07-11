package corpus

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/adewale/aha/internal/adapters"
	"github.com/adewale/aha/internal/archive"
	"github.com/adewale/aha/internal/config"
	"github.com/adewale/aha/internal/model"
	"github.com/adewale/aha/internal/testutil"
)

// The corpus never stores bundle blobs under depot v2; the no-bundle-blob
// assertions below pin that an interrupted bundle import leaves neither
// database rows nor any bundle-shaped artifact behind.
func TestCancellationBeforeCommitRollsBackIngest(t *testing.T) {
	bundlePath := writeFailureBundle(t, t.TempDir(), "cancel-before-commit")
	store := openFailureStore(t)
	ctx, cancel := context.WithCancel(t.Context())
	ing := Ingestor{
		Context:  ctx,
		Store:    store,
		Registry: adapters.Builtins(),
		hooks: ingestHooks{afterManifestFiles: func() error {
			cancel()
			return nil
		}},
	}
	if _, err := ing.IngestBundle(bundlePath); !errors.Is(err, context.Canceled) {
		t.Fatalf("IngestBundle err=%v want context.Canceled", err)
	}
	assertIngestDatabaseRolledBack(t, store)
}

func TestIngestFailureAfterFileBlobsRollsBackDatabaseAndLeavesNoBundleBlob(t *testing.T) {
	bundlePath := writeFailureBundle(t, t.TempDir(), "failure-after-files")
	store := openFailureStore(t)
	boom := errors.New("after manifest files")
	ing := Ingestor{Store: store, Registry: adapters.Builtins(), hooks: ingestHooks{afterManifestFiles: func() error { return boom }}}
	_, err := ing.IngestBundle(bundlePath)
	if !errors.Is(err, boom) {
		t.Fatalf("IngestBundle err=%v, want injected error", err)
	}
	assertIngestDatabaseRolledBack(t, store)
	assertNoFinalBundleBlob(t, store, bundlePath)
	assertNoStagingBundleBlobs(t, store)
	if matches, err := filepath.Glob(filepath.Join(store.Root, "blobs", "files", "*.zst")); err != nil || len(matches) == 0 {
		t.Fatalf("expected repairable content-addressed file blobs before injected failure, matches=%v err=%v", matches, err)
	}
}

func writeFailureBundle(t *testing.T, root, bundleID string) string {
	t.Helper()
	fx := testutil.WriteAgentFixtures(t, root)
	cfg := config.Default()
	cfg.MachineID = "failure-machine"
	cfg.Sources = []model.SourceConfig{{Type: "pi", Root: fx.PiRoot, Enabled: true}}
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

func openFailureStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func assertIngestDatabaseRolledBack(t *testing.T, store *Store) {
	t.Helper()
	for _, table := range []string{"snapshots", "ingest_attempts", "files", "sessions", "session_versions", "entries", "messages", "artifacts", "images", "entry_assets", "fts_messages", "fts_artifacts"} {
		if got := countRows(t, store, table); got != 0 {
			t.Fatalf("%s rows=%d want 0 after rollback", table, got)
		}
	}
}

func assertNoFinalBundleBlob(t *testing.T, store *Store, bundlePath string) {
	t.Helper()
	b, err := os.ReadFile(bundlePath)
	if err != nil {
		t.Fatal(err)
	}
	sha := sha256.Sum256(b)
	if _, err := os.Stat(filepath.Join(store.Root, "blobs", "bundles", hex.EncodeToString(sha[:])+".tar.zst")); !os.IsNotExist(err) {
		t.Fatalf("final bundle blob exists or stat failed: %v", err)
	}
}

func assertNoStagingBundleBlobs(t *testing.T, store *Store) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(store.Root, "blobs", "bundles", ".ingest-*.tar.zst"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("staging bundle blobs left behind: %v", matches)
	}
}

func countRows(t *testing.T, store *Store, table string) int {
	t.Helper()
	var n int
	if err := store.DB.QueryRow(`select count(*) from ` + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

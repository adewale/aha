package corpus_test

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/adewale/aha/internal/corpus"
	ahaprogress "github.com/adewale/aha/internal/progress"
	_ "modernc.org/sqlite"
)

func writeLegacyCorpus(t *testing.T, root string) {
	t.Helper()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(root, "corpus.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`create table bundles(bundle_id text primary key)`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "legacy-marker"), []byte("preserve me"), 0o600); err != nil {
		t.Fatal(err)
	}
}

type rebuildProgressClock struct{}

func (rebuildProgressClock) Now() time.Time { return time.Unix(0, 0) }

func TestRebuildProgressOrdersBuildVerifyAndAtomicSwap(t *testing.T) {
	root := filepath.Join(t.TempDir(), "corpus")
	writeLegacyCorpus(t, root)
	var events []ahaprogress.Event
	tracker := ahaprogress.NewTracker(ahaprogress.ObserverFunc(func(event ahaprogress.Event) { events = append(events, event) }), rebuildProgressClock{})
	_, err := corpus.RebuildWithBackupOptions(root, func(staging string) error {
		store, err := corpus.Open(staging)
		if err != nil {
			return err
		}
		return store.Close()
	}, corpus.RebuildOptions{Progress: tracker})
	if err != nil {
		t.Fatal(err)
	}
	var completed []ahaprogress.Phase
	for _, event := range events {
		if event.Kind == ahaprogress.Completed {
			completed = append(completed, event.Phase)
		}
	}
	want := []ahaprogress.Phase{ahaprogress.PhaseRebuildBuild, ahaprogress.PhaseRebuildVerify, ahaprogress.PhaseRebuildSwap}
	if !reflect.DeepEqual(completed, want) {
		t.Fatalf("completed phases=%v want %v; events=%+v", completed, want, events)
	}
}

func TestRebuildWithBackupPromotesVerifiedStagingAndPreservesLegacyDirectory(t *testing.T) {
	root := filepath.Join(t.TempDir(), "corpus")
	writeLegacyCorpus(t, root)
	report, err := corpus.RebuildWithBackup(root, func(staging string) error {
		store, err := corpus.Open(staging)
		if err != nil {
			return err
		}
		if err := store.Close(); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(staging, "fresh-marker"), []byte("ready"), 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Root != root || report.Backup == "" {
		t.Fatalf("report=%+v", report)
	}
	if got, err := os.ReadFile(filepath.Join(report.Backup, "legacy-marker")); err != nil || string(got) != "preserve me" {
		t.Fatalf("backup did not preserve legacy corpus: %q err=%v", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(root, "fresh-marker")); err != nil || string(got) != "ready" {
		t.Fatalf("fresh corpus not promoted: %q err=%v", got, err)
	}
	store, err := corpus.OpenExisting(root)
	if err != nil {
		t.Fatalf("promoted corpus not openable: %v", err)
	}
	_ = store.Close()
}

func TestRebuildWithBackupBuildFailureLeavesLegacyCorpusInPlace(t *testing.T) {
	root := filepath.Join(t.TempDir(), "corpus")
	writeLegacyCorpus(t, root)
	_, err := corpus.RebuildWithBackup(root, func(staging string) error {
		if err := os.WriteFile(filepath.Join(staging, "partial"), []byte("partial"), 0o600); err != nil {
			return err
		}
		return errors.New("injected build failure")
	})
	if err == nil || !errors.Is(err, corpus.ErrRebuildFailed) {
		t.Fatalf("RebuildWithBackup error=%v", err)
	}
	if got, readErr := os.ReadFile(filepath.Join(root, "legacy-marker")); readErr != nil || string(got) != "preserve me" {
		t.Fatalf("legacy corpus changed after failed build: %q err=%v", got, readErr)
	}
	matches, globErr := filepath.Glob(root + ".pre-v2-*")
	if globErr != nil || len(matches) != 0 {
		t.Fatalf("failed preflight created backup: matches=%v err=%v", matches, globErr)
	}
}

func TestRebuildWithBackupVerificationFailureLeavesLegacyCorpusInPlace(t *testing.T) {
	root := filepath.Join(t.TempDir(), "corpus")
	writeLegacyCorpus(t, root)
	_, err := corpus.RebuildWithBackup(root, func(staging string) error {
		return os.WriteFile(filepath.Join(staging, "not-a-corpus"), []byte("partial"), 0o600)
	})
	if err == nil || !errors.Is(err, corpus.ErrRebuildFailed) {
		t.Fatalf("verification failure error=%v", err)
	}
	if got, readErr := os.ReadFile(filepath.Join(root, "legacy-marker")); readErr != nil || string(got) != "preserve me" {
		t.Fatalf("verification failure changed legacy corpus: %q err=%v", got, readErr)
	}
}

func TestRebuildWithBackupRejectsHealthyCorpus(t *testing.T) {
	root := filepath.Join(t.TempDir(), "corpus")
	store, err := corpus.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	_ = store.Close()
	if _, err := corpus.RebuildWithBackup(root, func(string) error { return nil }); !errors.Is(err, corpus.ErrNotLegacyCorpus) {
		t.Fatalf("healthy corpus rebuild error=%v", err)
	}
}

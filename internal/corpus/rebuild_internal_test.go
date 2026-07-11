package corpus

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRebuildWithBackupAtomicPromotionFailureLeavesLegacyRootUntouched(t *testing.T) {
	root := filepath.Join(t.TempDir(), "corpus")
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
	_ = db.Close()
	marker := filepath.Join(root, "legacy-marker")
	if err := os.WriteFile(marker, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	injected := errors.New("injected swap failure")
	_, err = rebuildWithBackupAtUsingSwap(root, time.Date(2026, 7, 11, 10, 1, 0, 0, time.UTC), func(staging string) error {
		store, err := Open(staging)
		if err != nil {
			return err
		}
		return store.Close()
	}, func(string, string) error { return injected })
	if err == nil || !errors.Is(err, ErrRebuildFailed) {
		t.Fatalf("promotion error=%v", err)
	}
	if got, readErr := os.ReadFile(marker); readErr != nil || string(got) != "old" {
		t.Fatalf("failed promotion changed legacy root: %q err=%v", got, readErr)
	}
	matches, _ := filepath.Glob(root + ".pre-v2-*")
	if len(matches) != 0 {
		t.Fatalf("failed promotion left staging backup paths: %v", matches)
	}
}

func TestRebuildWithBackupAvoidsExistingTimestampedBackup(t *testing.T) {
	root := filepath.Join(t.TempDir(), "corpus")
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
	_ = db.Close()
	now := time.Date(2026, 7, 11, 10, 1, 0, 0, time.UTC)
	collision := root + ".pre-v2-20260711-100100"
	if err := os.Mkdir(collision, 0o755); err != nil {
		t.Fatal(err)
	}
	report, err := rebuildWithBackupAt(root, now, func(staging string) error {
		store, err := Open(staging)
		if err != nil {
			return err
		}
		return store.Close()
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Backup != collision+"-1" {
		t.Fatalf("backup=%q want %q", report.Backup, collision+"-1")
	}
}

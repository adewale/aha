package corpus

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/adewale/aha/internal/model"
)

func TestRebuildCanonicalizesSymlinkRootBeforeLockAndSwap(t *testing.T) {
	realRoot := filepath.Join(t.TempDir(), "real-corpus")
	writeInternalLegacyCorpus(t, realRoot)
	alias := filepath.Join(t.TempDir(), "corpus-alias")
	if err := os.Symlink(realRoot, alias); err != nil {
		t.Fatal(err)
	}
	report, err := rebuildWithBackupAt(alias, time.Date(2026, 7, 11, 10, 1, 0, 0, time.UTC), func(staging string) error {
		store, err := Open(staging)
		if err != nil {
			return err
		}
		return store.Close()
	})
	if err != nil {
		t.Fatal(err)
	}
	canonicalRoot, err := canonicalCorpusIdentity(realRoot)
	if err != nil {
		t.Fatal(err)
	}
	if report.Root != canonicalRoot || filepath.Dir(report.Backup) != filepath.Dir(canonicalRoot) {
		t.Fatalf("report=%+v want canonical root %q", report, canonicalRoot)
	}
	if info, err := os.Lstat(alias); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("alias was swapped instead of canonical target: info=%v err=%v", info, err)
	}
	if _, err := os.Stat(filepath.Join(report.Backup, "legacy-marker")); err != nil {
		t.Fatalf("canonical backup missing legacy marker: %v", err)
	}
}

func TestRebuildDurabilityOrderingAndPostSwapFailurePreservesBothDirectories(t *testing.T) {
	root := filepath.Join(t.TempDir(), "corpus")
	writeInternalLegacyCorpus(t, root)
	var order []string
	postSync := errors.New("post-swap parent sync failed")
	parentSyncs := 0
	ops := rebuildOps{
		syncTree: func(path string) error { order = append(order, "sync-tree:"+filepath.Base(path)); return nil },
		syncDir: func(path string) error {
			parentSyncs++
			order = append(order, "sync-parent")
			if parentSyncs == 2 {
				return postSync
			}
			return nil
		},
		swap: func(first, second string) error {
			order = append(order, "swap")
			return atomicSwapDirectories(first, second)
		},
	}
	report, err := rebuildWithBackupAtUsingOps(root, time.Date(2026, 7, 11, 10, 1, 0, 0, time.UTC), func(staging string) error {
		store, err := Open(staging)
		if err != nil {
			return err
		}
		if err := store.Close(); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(staging, "fresh-marker"), []byte("new"), 0o600)
	}, RebuildOptions{}, ops)
	if err == nil || !errors.Is(err, postSync) {
		t.Fatalf("error=%v want post-swap sync failure", err)
	}
	if len(order) != 4 || !strings.HasPrefix(order[0], "sync-tree:") || order[1] != "sync-parent" || order[2] != "swap" || order[3] != "sync-parent" {
		t.Fatalf("durability order=%v want sync-tree,parent,swap,parent", order)
	}
	if _, statErr := os.Stat(filepath.Join(root, "fresh-marker")); statErr != nil {
		t.Fatalf("new root lost after completed exchange: %v", statErr)
	}
	matches, _ := filepath.Glob(root + ".pre-v2-*")
	if len(matches) != 1 {
		t.Fatalf("backup guarantee lost after post-swap failure: %v report=%+v", matches, report)
	}
}

func TestRebuildDurabilityFailureBeforeSwapLeavesLegacyUntouched(t *testing.T) {
	for _, phase := range []string{"tree", "parent"} {
		t.Run(phase, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "corpus")
			writeInternalLegacyCorpus(t, root)
			injected := errors.New("injected durability failure")
			ops := rebuildOps{
				syncTree: func(string) error {
					if phase == "tree" {
						return injected
					}
					return nil
				},
				syncDir: func(string) error {
					if phase == "parent" {
						return injected
					}
					return nil
				},
				swap: func(string, string) error { t.Fatal("swap ran after durability failure"); return nil },
			}
			_, err := rebuildWithBackupAtUsingOps(root, time.Date(2026, 7, 11, 10, 1, 0, 0, time.UTC), func(staging string) error {
				store, err := Open(staging)
				if err != nil {
					return err
				}
				return store.Close()
			}, RebuildOptions{}, ops)
			if err == nil || !errors.Is(err, injected) || !errors.Is(err, ErrRebuildFailed) {
				t.Fatalf("error=%v", err)
			}
			if got, readErr := os.ReadFile(filepath.Join(root, "legacy-marker")); readErr != nil || string(got) != "old" {
				t.Fatalf("durability failure changed legacy root: %q err=%v", got, readErr)
			}
			matches, _ := filepath.Glob(root + ".pre-v2-*")
			if len(matches) != 0 {
				t.Fatalf("failure left staging: %v", matches)
			}
		})
	}
}

func TestRebuildValidatesDerivedStagingBeforeCreatingIt(t *testing.T) {
	root := filepath.Join(t.TempDir(), "corpus")
	writeInternalLegacyCorpus(t, root)
	blocked := errors.New("derived staging overlaps source")
	var validated string
	_, err := rebuildWithBackupAtUsingSwapOptions(root, time.Date(2026, 7, 11, 10, 1, 0, 0, time.UTC), func(string) error {
		t.Fatal("build ran after derived staging rejection")
		return nil
	}, atomicSwapDirectories, RebuildOptions{ValidateStaging: func(path string) error { validated = path; return blocked }})
	if !errors.Is(err, blocked) {
		t.Fatalf("error=%v want validation failure", err)
	}
	if validated == "" {
		t.Fatal("derived staging was not validated")
	}
	if _, statErr := os.Lstat(validated); !os.IsNotExist(statErr) {
		t.Fatalf("rejected staging was created: %v", statErr)
	}
}

func writeInternalLegacyCorpus(t *testing.T, root string) {
	t.Helper()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(root, model.WorkspaceDatabaseFilename))
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
	if err := os.WriteFile(filepath.Join(root, "legacy-marker"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRepairPreservesBuilderCancellationCause(t *testing.T) {
	if !rebuildLifecycleSupported() || !atomicSwapSupported() {
		t.Skip("repair is unsupported on this platform")
	}
	root := filepath.Join(t.TempDir(), "workspace")
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = RepairWithBackup(root, func(string) error { return context.Canceled }, RebuildOptions{Context: t.Context()})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("RepairWithBackup error=%v, want context.Canceled", err)
	}
}

func TestRebuildCancellationBeforeLockDoesNotCreateStaging(t *testing.T) {
	root := filepath.Join(t.TempDir(), "corpus")
	writeInternalLegacyCorpus(t, root)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := rebuildWithBackupAtUsingSwapOptions(root, time.Now(), func(string) error { return nil }, atomicSwapDirectories, RebuildOptions{Context: ctx})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
	matches, _ := filepath.Glob(root + ".pre-v2-*")
	if len(matches) != 0 {
		t.Fatalf("cancelled rebuild created staging: %v", matches)
	}
}

func TestRebuildWithBackupAtomicPromotionFailureLeavesLegacyRootUntouched(t *testing.T) {
	root := filepath.Join(t.TempDir(), "corpus")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(root, model.WorkspaceDatabaseFilename))
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
	db, err := sql.Open("sqlite", filepath.Join(root, model.WorkspaceDatabaseFilename))
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
	canonicalRoot, err := canonicalCorpusIdentity(root)
	if err != nil {
		t.Fatal(err)
	}
	want := canonicalRoot + ".pre-v2-20260711-100100-1"
	if report.Backup != want {
		t.Fatalf("backup=%q want %q", report.Backup, want)
	}
}

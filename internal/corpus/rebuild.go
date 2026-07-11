package corpus

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	ahaclock "github.com/adewale/aha/internal/clock"
	"github.com/adewale/aha/internal/paths"
	ahaprogress "github.com/adewale/aha/internal/progress"
)

var (
	ErrLegacyCorpus       = errors.New("pre-v2 corpus")
	ErrNotLegacyCorpus    = errors.New("corpus is not a pre-v2 corpus")
	ErrRebuildFailed      = errors.New("corpus rebuild failed")
	ErrRebuildUnsupported = errors.New("safe corpus rebuild is unsupported on this platform")
)

type RebuildReport struct {
	Root   string `json:"root"`
	Backup string `json:"backup"`
}

// IsLegacyCorpus detects the bundle-keyed schema without running migrations.
func IsLegacyCorpus(root string) (bool, error) {
	expanded, err := paths.Expand(root)
	if err != nil {
		return false, err
	}
	dbPath := filepath.Join(expanded, "corpus.db")
	if _, err := os.Stat(dbPath); err != nil {
		return false, err
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return false, err
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(`select count(*) from sqlite_master where type='table' and (name='bundles' or (sql like '%bundle_id%' and name<>'bundles'))`).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

type RebuildOptions struct {
	Context  context.Context
	Progress *ahaprogress.Tracker
	// ValidateStaging applies source/corpus overlap policy to the exact derived
	// sibling path before it is created.
	ValidateStaging func(string) error
}

type rebuildOps struct {
	syncTree func(string) error
	syncDir  func(string) error
	swap     func(string, string) error
}

func productionRebuildOps(swap func(string, string) error) rebuildOps {
	return rebuildOps{syncTree: syncRebuildTree, syncDir: syncRebuildDirectory, swap: swap}
}

// RebuildWithBackup constructs and verifies a replacement at the final sibling
// backup path, then atomically swaps it with the legacy corpus.
func RebuildWithBackup(root string, build func(staging string) error) (RebuildReport, error) {
	return RebuildWithBackupOptions(root, build, RebuildOptions{})
}

func RebuildWithBackupOptions(root string, build func(staging string) error, opts RebuildOptions) (RebuildReport, error) {
	return rebuildWithBackupAtUsingSwapOptions(root, ahaclock.RealClock{}.Now(), build, atomicSwapDirectories, opts)
}

func rebuildWithBackupAt(root string, now time.Time, build func(staging string) error) (RebuildReport, error) {
	return rebuildWithBackupAtUsingSwapOptions(root, now, build, atomicSwapDirectories, RebuildOptions{})
}

func rebuildWithBackupAtUsingSwap(root string, now time.Time, build func(staging string) error, swap func(string, string) error) (RebuildReport, error) {
	return rebuildWithBackupAtUsingSwapOptions(root, now, build, swap, RebuildOptions{})
}

func rebuildWithBackupAtUsingSwapOptions(root string, now time.Time, build func(staging string) error, swap func(string, string) error, opts RebuildOptions) (RebuildReport, error) {
	return rebuildWithBackupAtUsingOps(root, now, build, opts, productionRebuildOps(swap))
}

func rebuildWithBackupAtUsingOps(root string, now time.Time, build func(staging string) error, opts RebuildOptions, ops rebuildOps) (RebuildReport, error) {
	ctx := opts.Context
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return RebuildReport{}, err
	}
	if !rebuildLifecycleSupported() || !atomicSwapSupported() {
		return RebuildReport{}, ErrRebuildUnsupported
	}
	expanded, err := paths.Expand(root)
	if err != nil {
		return RebuildReport{}, err
	}
	expanded, err = canonicalCorpusIdentity(expanded)
	if err != nil {
		return RebuildReport{}, err
	}
	lockTotal := ahaprogress.KnownTotal(1)
	opts.Progress.Start(ahaprogress.PhaseRebuildLock, lockTotal, ahaprogress.UnitSteps)
	lock, err := acquireLifecycleLock(ctx, expanded, true)
	if err != nil {
		if ctx.Err() != nil {
			opts.Progress.Cancel(ahaprogress.PhaseRebuildLock, 0, lockTotal, ahaprogress.UnitSteps)
		} else {
			opts.Progress.Fail(ahaprogress.PhaseRebuildLock, 0, lockTotal, ahaprogress.UnitSteps)
		}
		return RebuildReport{}, err
	}
	opts.Progress.Complete(ahaprogress.PhaseRebuildLock, 1, lockTotal, ahaprogress.UnitSteps)
	defer lock.release()
	legacy, err := IsLegacyCorpus(expanded)
	if err != nil {
		return RebuildReport{}, err
	}
	if !legacy {
		return RebuildReport{}, ErrNotLegacyCorpus
	}
	backup, err := unusedBackupPath(expanded, now)
	if err != nil {
		return RebuildReport{}, err
	}
	if opts.ValidateStaging != nil {
		if err := opts.ValidateStaging(backup); err != nil {
			return RebuildReport{}, err
		}
	}
	if err := os.Mkdir(backup, 0o700); err != nil {
		return RebuildReport{}, err
	}
	staging := backup
	promoted := false
	defer func() {
		if !promoted {
			_ = os.RemoveAll(staging)
		}
	}()
	activePhase := ahaprogress.PhaseRebuildBuild
	progressComplete := false
	defer func() {
		if progressComplete {
			return
		}
		if ctx.Err() != nil {
			opts.Progress.Cancel(activePhase, 0, ahaprogress.KnownTotal(1), ahaprogress.UnitSteps)
		} else {
			opts.Progress.Fail(activePhase, 0, ahaprogress.KnownTotal(1), ahaprogress.UnitSteps)
		}
	}()
	opts.Progress.Start(activePhase, ahaprogress.KnownTotal(1), ahaprogress.UnitSteps)
	if err := build(staging); err != nil {
		if ctx.Err() != nil {
			return RebuildReport{}, ctx.Err()
		}
		return RebuildReport{}, fmt.Errorf("%w: build replacement: %v", ErrRebuildFailed, err)
	}
	opts.Progress.Complete(activePhase, 1, ahaprogress.KnownTotal(1), ahaprogress.UnitSteps)
	activePhase = ahaprogress.PhaseRebuildVerify
	opts.Progress.Start(activePhase, ahaprogress.KnownTotal(1), ahaprogress.UnitSteps)
	store, err := OpenExisting(staging)
	if err != nil {
		return RebuildReport{}, fmt.Errorf("%w: open replacement: %v", ErrRebuildFailed, err)
	}
	verification, verifyErr := VerifyContext(ctx, store)
	closeErr := store.Close()
	if verifyErr != nil {
		return RebuildReport{}, fmt.Errorf("%w: verify replacement: %v", ErrRebuildFailed, verifyErr)
	}
	if closeErr != nil {
		return RebuildReport{}, fmt.Errorf("%w: close replacement: %v", ErrRebuildFailed, closeErr)
	}
	if len(verification.Problems) > 0 {
		return RebuildReport{}, fmt.Errorf("%w: replacement has %d verification problems", ErrRebuildFailed, len(verification.Problems))
	}
	opts.Progress.Complete(activePhase, 1, ahaprogress.KnownTotal(1), ahaprogress.UnitSteps)
	activePhase = ahaprogress.PhaseRebuildSwap
	opts.Progress.Start(activePhase, ahaprogress.KnownTotal(1), ahaprogress.UnitSteps)
	if err := ctx.Err(); err != nil {
		return RebuildReport{}, err
	}
	if err := ops.syncTree(staging); err != nil {
		return RebuildReport{}, fmt.Errorf("%w: sync replacement tree: %w", ErrRebuildFailed, err)
	}
	parent := filepath.Dir(expanded)
	if err := ops.syncDir(parent); err != nil {
		return RebuildReport{}, fmt.Errorf("%w: sync replacement parent before promotion: %w", ErrRebuildFailed, err)
	}
	if err := ops.swap(staging, expanded); err != nil {
		return RebuildReport{}, fmt.Errorf("%w: atomic promotion: %w", ErrRebuildFailed, err)
	}
	promoted = true
	report := RebuildReport{Root: expanded, Backup: backup}
	if err := ops.syncDir(parent); err != nil {
		return report, fmt.Errorf("%w: sync replacement parent after promotion: %w", ErrRebuildFailed, err)
	}
	opts.Progress.Complete(activePhase, 1, ahaprogress.KnownTotal(1), ahaprogress.UnitSteps)
	progressComplete = true
	return report, nil
}

func unusedBackupPath(root string, now time.Time) (string, error) {
	stamp := now.UTC().Format("20060102-150405")
	for i := 0; i < 1000; i++ {
		candidate := root + ".pre-v2-" + stamp
		if i > 0 {
			candidate = fmt.Sprintf("%s-%d", candidate, i)
		}
		if _, err := os.Lstat(candidate); os.IsNotExist(err) {
			return candidate, nil
		} else if err != nil {
			return "", err
		}
	}
	return "", fmt.Errorf("cannot allocate unique corpus backup path")
}

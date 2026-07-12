package corpus

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	ahaclock "github.com/adewale/aha/internal/clock"
	"github.com/adewale/aha/internal/paths"
	ahaprogress "github.com/adewale/aha/internal/progress"
)

// RepairWithBackup rebuilds any existing Workspace into a verified sibling,
// atomically exchanges it, and leaves the prior Workspace at Backup.
func RepairWithBackup(root string, build func(string) error, opts RebuildOptions) (RebuildReport, error) {
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
	if _, err := os.Stat(filepath.Join(expanded, "corpus.db")); err != nil {
		return RebuildReport{}, err
	}
	lock, err := acquireLifecycleLock(ctx, expanded, true)
	if err != nil {
		return RebuildReport{}, err
	}
	defer lock.release()
	backup, err := unusedWorkspaceBackupPath(expanded, ahaclock.RealClock{}.Now())
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
	promoted := false
	defer func() {
		if !promoted {
			_ = os.RemoveAll(backup)
		}
	}()
	opts.Progress.Start(ahaprogress.PhaseRebuildBuild, ahaprogress.KnownTotal(1), ahaprogress.UnitSteps)
	if err := build(backup); err != nil {
		opts.Progress.Fail(ahaprogress.PhaseRebuildBuild, 0, ahaprogress.KnownTotal(1), ahaprogress.UnitSteps)
		return RebuildReport{}, fmt.Errorf("%w: build replacement: %v", ErrRebuildFailed, err)
	}
	opts.Progress.Complete(ahaprogress.PhaseRebuildBuild, 1, ahaprogress.KnownTotal(1), ahaprogress.UnitSteps)
	store, err := OpenExisting(backup)
	if err != nil {
		return RebuildReport{}, fmt.Errorf("%w: open replacement: %v", ErrRebuildFailed, err)
	}
	verification, verifyErr := VerifyContext(ctx, store)
	closeErr := store.Close()
	if verifyErr != nil || closeErr != nil || len(verification.Problems) > 0 {
		return RebuildReport{}, fmt.Errorf("%w: replacement verification failed", ErrRebuildFailed)
	}
	if err := syncRebuildTree(backup); err != nil {
		return RebuildReport{}, fmt.Errorf("%w: sync replacement: %v", ErrRebuildFailed, err)
	}
	parent := filepath.Dir(expanded)
	if err := syncRebuildDirectory(parent); err != nil {
		return RebuildReport{}, err
	}
	if err := atomicSwapDirectories(backup, expanded); err != nil {
		return RebuildReport{}, fmt.Errorf("%w: atomic promotion: %v", ErrRebuildFailed, err)
	}
	promoted = true
	if err := syncRebuildDirectory(parent); err != nil {
		return RebuildReport{Root: expanded, Backup: backup}, err
	}
	return RebuildReport{Root: expanded, Backup: backup}, nil
}

func unusedWorkspaceBackupPath(root string, now time.Time) (string, error) {
	stamp := now.UTC().Format("20060102-150405")
	for i := 0; i < 1000; i++ {
		candidate := root + ".backup-" + stamp
		if i > 0 {
			candidate = fmt.Sprintf("%s-%d", candidate, i)
		}
		if _, err := os.Lstat(candidate); os.IsNotExist(err) {
			return candidate, nil
		} else if err != nil {
			return "", err
		}
	}
	return "", fmt.Errorf("cannot allocate unique Workspace backup path")
}

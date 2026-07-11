//go:build windows

package opencodeexport

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/adewale/aha/internal/clock"
	"golang.org/x/sys/windows"
)

func withExportLock(ctx context.Context, destDir string, fn func() error) error {
	lockPath := filepath.Join(destDir, ".export.lock")
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()

	handle := windows.Handle(f.Fd())
	var overlapped windows.Overlapped
	sleeper := clock.RealSleeper{}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := windows.LockFileEx(handle, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &overlapped)
		if err == nil {
			break
		}
		if !errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return err
		}
		sleeper.Sleep(25 * time.Millisecond)
	}
	defer windows.UnlockFileEx(handle, 0, 1, 0, &overlapped) //nolint:errcheck -- closing the handle also releases the lock
	return fn()
}

//go:build !windows

package depot

import (
	"os"
	"path/filepath"
	"syscall"
)

func withLocalLock(root string, fn func() error) error {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	lockPath := filepath.Join(root, ".depot.lock")
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return fn()
}

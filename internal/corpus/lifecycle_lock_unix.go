//go:build !windows

package corpus

import (
	"os"
	"path/filepath"
	"syscall"
)

type lifecycleLock struct{ file *os.File }

func rebuildLifecycleSupported() bool { return true }

func acquireLifecycleLock(root string, exclusive bool) (*lifecycleLock, error) {
	parent := filepath.Dir(root)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(parent, "."+filepath.Base(root)+".lifecycle.lock")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	how := syscall.LOCK_SH
	if exclusive {
		how = syscall.LOCK_EX
	}
	if err := syscall.Flock(int(file.Fd()), how); err != nil {
		_ = file.Close()
		return nil, err
	}
	return &lifecycleLock{file: file}, nil
}

func (l *lifecycleLock) release() error {
	if l == nil || l.file == nil {
		return nil
	}
	unlockErr := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	closeErr := l.file.Close()
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}

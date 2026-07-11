//go:build !windows

package corpus

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

type lifecycleLock struct{ file *os.File }

func rebuildLifecycleSupported() bool { return true }

func acquireLifecycleLock(ctx context.Context, root string, exclusive bool) (*lifecycleLock, error) {
	return acquireLifecycleLockWithWait(ctx, root, exclusive, waitForLifecycleLock)
}

func acquireLifecycleLockWithWait(ctx context.Context, root string, exclusive bool, wait func(context.Context) error) (*lifecycleLock, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	identity, err := canonicalCorpusIdentity(root)
	if err != nil {
		return nil, err
	}
	parent := filepath.Dir(identity)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(parent, "."+filepath.Base(identity)+".lifecycle.lock")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	how := syscall.LOCK_SH | syscall.LOCK_NB
	if exclusive {
		how = syscall.LOCK_EX | syscall.LOCK_NB
	}
	for {
		if err := ctx.Err(); err != nil {
			_ = file.Close()
			return nil, err
		}
		err := syscall.Flock(int(file.Fd()), how)
		if err == nil {
			return &lifecycleLock{file: file}, nil
		}
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			_ = file.Close()
			return nil, err
		}
		if err := wait(ctx); err != nil {
			_ = file.Close()
			return nil, err
		}
	}
}

func waitForLifecycleLock(ctx context.Context) error {
	timer := time.NewTimer(10 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
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

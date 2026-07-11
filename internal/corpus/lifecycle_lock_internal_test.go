//go:build !windows

package corpus

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLifecycleExclusiveLockReportsRealContentionBeforeReaderRelease(t *testing.T) {
	root := filepath.Join(t.TempDir(), "corpus")
	shared, err := acquireLifecycleLock(t.Context(), root, false)
	if err != nil {
		t.Fatal(err)
	}
	blocked := make(chan struct{}, 1)
	allowRetry := make(chan struct{})
	acquired := make(chan *lifecycleLock, 1)
	errs := make(chan error, 1)
	go func() {
		lock, err := acquireLifecycleLockWithWait(t.Context(), root, true, func(ctx context.Context) error {
			select {
			case blocked <- struct{}{}:
			default:
			}
			select {
			case <-allowRetry:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
		if err != nil {
			errs <- err
			return
		}
		acquired <- lock
	}()
	select {
	case <-blocked:
		// The kernel returned EWOULDBLOCK; this cannot pass with a no-op lock.
	case lock := <-acquired:
		_ = lock.release()
		_ = shared.release()
		t.Fatal("exclusive lifecycle lock acquired while shared lock was held")
	case err := <-errs:
		_ = shared.release()
		t.Fatal(err)
	case <-t.Context().Done():
		_ = shared.release()
		t.Fatal(t.Context().Err())
	}
	if err := shared.release(); err != nil {
		t.Fatal(err)
	}
	close(allowRetry)
	select {
	case lock := <-acquired:
		if err := lock.release(); err != nil {
			t.Fatal(err)
		}
	case err := <-errs:
		t.Fatal(err)
	case <-t.Context().Done():
		t.Fatal(t.Context().Err())
	}
}

func TestLifecycleLockWaitIsContextCancellable(t *testing.T) {
	root := filepath.Join(t.TempDir(), "corpus")
	shared, err := acquireLifecycleLock(t.Context(), root, false)
	if err != nil {
		t.Fatal(err)
	}
	defer shared.release()
	ctx, cancel := context.WithCancel(t.Context())
	blocked := make(chan struct{}, 1)
	errCh := make(chan error, 1)
	go func() {
		_, err := acquireLifecycleLockWithWait(ctx, root, true, func(ctx context.Context) error {
			select {
			case blocked <- struct{}{}:
			default:
			}
			<-ctx.Done()
			return ctx.Err()
		})
		errCh <- err
	}()
	<-blocked
	cancel()
	if err := <-errCh; !errors.Is(err, context.Canceled) {
		t.Fatalf("lock error=%v want context.Canceled", err)
	}
}

func TestLifecycleAliasesShareCanonicalLockIdentity(t *testing.T) {
	root := filepath.Join(t.TempDir(), "real-corpus")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	first, err := acquireLifecycleLock(t.Context(), alias, false)
	if err != nil {
		t.Fatal(err)
	}
	defer first.release()
	ctx, cancel := context.WithCancel(t.Context())
	blocked := make(chan struct{}, 1)
	errCh := make(chan error, 1)
	go func() {
		_, err := acquireLifecycleLockWithWait(ctx, root, true, func(ctx context.Context) error {
			select {
			case blocked <- struct{}{}:
			default:
			}
			<-ctx.Done()
			return ctx.Err()
		})
		errCh <- err
	}()
	<-blocked
	cancel()
	if err := <-errCh; !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
}

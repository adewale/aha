//go:build !windows

package corpus

import (
	"path/filepath"
	"testing"
	"time"
)

func TestLifecycleExclusiveLockWaitsForOpenCorpusReader(t *testing.T) {
	root := filepath.Join(t.TempDir(), "corpus")
	shared, err := acquireLifecycleLock(root, false)
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	acquired := make(chan *lifecycleLock, 1)
	errs := make(chan error, 1)
	go func() {
		close(started)
		lock, err := acquireLifecycleLock(root, true)
		if err != nil {
			errs <- err
			return
		}
		acquired <- lock
	}()
	<-started
	select {
	case lock := <-acquired:
		_ = lock.release()
		_ = shared.release()
		t.Fatal("exclusive lifecycle lock acquired while shared reader lock was held")
	case err := <-errs:
		_ = shared.release()
		t.Fatal(err)
	case <-time.After(50 * time.Millisecond):
		// Expected: the writer remains blocked until the reader closes.
	}
	if err := shared.release(); err != nil {
		t.Fatal(err)
	}
	select {
	case lock := <-acquired:
		if err := lock.release(); err != nil {
			t.Fatal(err)
		}
	case err := <-errs:
		t.Fatal(err)
	case <-time.After(time.Second):
		t.Fatal("exclusive lifecycle lock did not acquire after reader released")
	}
}

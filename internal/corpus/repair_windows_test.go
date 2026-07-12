//go:build windows

package corpus

import (
	"errors"
	"testing"
)

func TestWindowsRepairRefusesBeforeBuilderOrStaging(t *testing.T) {
	called := false
	_, err := RepairWithBackup(`C:\missing-workspace`, func(string) error {
		called = true
		return nil
	}, RebuildOptions{Context: t.Context()})
	if !errors.Is(err, ErrRebuildUnsupported) {
		t.Fatalf("RepairWithBackup error=%v, want ErrRebuildUnsupported", err)
	}
	if called {
		t.Fatal("unsupported Windows repair invoked builder")
	}
}

package fileutil_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adewale/aha/internal/fileutil"
)

func TestCopyHashRemovesOversizedDestination(t *testing.T) {
	root := t.TempDir()
	dst := filepath.Join(root, "out")
	if _, err := fileutil.CopyHash(dst, strings.NewReader("abcdef"), 3); err == nil {
		t.Fatalf("expected oversized input error")
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Fatalf("oversized destination remains: %v", err)
	}
}

func TestHashDiscardReturnsSHAAndBytes(t *testing.T) {
	res, err := fileutil.HashDiscard(strings.NewReader("abc"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Bytes != 3 || res.SHA256 != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Fatalf("bad hash result: %+v", res)
	}
}

func TestAtomicWriteRemovesTempOnError(t *testing.T) {
	root := t.TempDir()
	dst := filepath.Join(root, "nested", "out")
	if err := fileutil.AtomicWrite(dst, fileutil.AtomicOptions{TempPattern: "tmp-*"}, func(f *os.File) error {
		_, _ = f.WriteString("partial")
		return os.ErrInvalid
	}); err == nil {
		t.Fatalf("expected write error")
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Fatalf("destination exists after failed atomic write: %v", err)
	}
	entries, err := os.ReadDir(filepath.Dir(dst))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("temp files left behind: %v", entries)
	}
}

func TestAtomicCopyFileExistingOKPreservesDestination(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	dst := filepath.Join(root, "dst")
	if err := os.WriteFile(src, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := fileutil.AtomicCopyFile(dst, src, fileutil.AtomicOptions{ExistingOK: true}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "old" {
		t.Fatalf("existing destination overwritten: %q", got)
	}
}

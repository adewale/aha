package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/adewale/aha/internal/config"
)

func TestWriteAtomicallyReplacesConfigSymlinkWithoutFollowingIt(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "unrelated")
	if err := os.WriteFile(target, []byte("do-not-touch"), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, "config.jsonc")
	if err := os.Symlink(target, configPath); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Write(configPath, config.Default(), "// test\n"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "do-not-touch" {
		t.Fatalf("config write followed symlink and changed target: %q", got)
	}
	info, err := os.Lstat(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("config path is still a symlink; expected atomic replacement")
	}
}

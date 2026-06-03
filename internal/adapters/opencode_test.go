package adapters

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mustTouch(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestOpenCodeExportDirHonorsEnvOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AHA_OPENCODE_EXPORT_DIR", dir)
	got, err := openCodeExportDir("/some/where/opencode.db")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, dir+string(filepath.Separator)) {
		t.Fatalf("export dir %q not under override base %q", got, dir)
	}
	// Same database always maps to the same directory (stable for reuse).
	again, _ := openCodeExportDir("/some/where/opencode.db")
	if got != again {
		t.Fatalf("export dir not stable: %q vs %q", got, again)
	}
	other, _ := openCodeExportDir("/some/where/opencode-beta.db")
	if other == got {
		t.Fatalf("distinct databases must map to distinct dirs")
	}
}

func TestOpenCodeDBPathsResolvesDefaultAndChannel(t *testing.T) {
	root := t.TempDir()
	mustTouch(t, filepath.Join(root, "opencode.db"))
	mustTouch(t, filepath.Join(root, "opencode-beta.db"))
	mustTouch(t, filepath.Join(root, "notes.txt")) // ignored

	got := openCodeDBPaths(root)
	if len(got) != 2 {
		t.Fatalf("got %v, want the two opencode databases", got)
	}
	if filepath.Base(got[0]) != "opencode-beta.db" && filepath.Base(got[1]) != "opencode-beta.db" {
		t.Fatalf("channel database not discovered: %v", got)
	}
}

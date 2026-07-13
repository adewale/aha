package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adewale/aha/internal/config"
)

func TestWritePreservesExistingJSONCComments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.jsonc")
	before := []byte("// operator note\n{\n  // keep this source note\n  \"machine_id\": \"old\",\n  \"extensions\": {\n    \"example.com/tool\": {\n      // extension-owned note\n      \"future\": true,\n    },\n  },\n  \"sources\": [],\n  \"workspace_dir\": \"/old\"\n}\n")
	if err := os.WriteFile(path, before, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.MachineID = "new"
	cfg.Sources = nil
	cfg.WorkspaceDir = "/new"
	if _, err := config.Write(path, cfg, "// replacement header must not erase existing comments\n"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(got)
	for _, want := range []string{"// operator note", "// keep this source note", "// extension-owned note"} {
		if !strings.Contains(text, want) {
			t.Fatalf("rewritten config missing %q:\n%s", want, text)
		}
	}
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Schema != config.SchemaV1 || loaded.MachineID != "new" || loaded.WorkspaceDir != "/new" {
		t.Fatalf("rewritten config=%+v", loaded)
	}
}

func TestWriteCanRemoveOptionalConfigFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.jsonc")
	before := []byte(`{
  "machine_id": "m",
  "machine_label": "old label",
  "extensions": {"example.com/tool": {"enabled": true}},
  "sources": [],
  "workspace_dir": "/work"
}`)
	if err := os.WriteFile(path, before, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.MachineLabel = ""
	cfg.Extensions = nil
	if _, err := config.Write(path, cfg, ""); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "machine_label") || strings.Contains(string(body), "extensions") {
		t.Fatalf("optional fields survived explicit removal:\n%s", body)
	}
}

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

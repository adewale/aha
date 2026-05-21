package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/adewale/aha/internal/config"
)

func TestDefaultSourcesComeFromRegisteredAdapters(t *testing.T) {
	cfg := config.Default()
	seen := map[string]string{}
	for _, s := range cfg.Sources {
		seen[s.Type] = s.Root
	}
	if seen["pi"] != "~/.pi/agent/sessions" || seen["claude-code"] != "~/.claude/projects" || seen["codex"] != "~/.codex/sessions" {
		t.Fatalf("unexpected default sources: %+v", cfg.Sources)
	}
}

func TestLoadJSONC(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.jsonc")
	content := `// comment
{
  "machine_id": "m1",
  "sources": [
    {"type":"pi", "root":"/tmp/pi", "enabled":true},
  ],
  "corpus_dir": "/tmp/corpus",
}
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MachineID != "m1" || len(cfg.Sources) != 1 || cfg.Sources[0].Type != "pi" {
		t.Fatalf("bad config: %+v", cfg)
	}
}

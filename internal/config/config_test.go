package config_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/adewale/aha/internal/adapters"
	"github.com/adewale/aha/internal/config"
)

func TestDefaultSourcesComeFromRegisteredAdapters(t *testing.T) {
	cfg := config.Default()
	seen := map[string]string{}
	for _, s := range cfg.Sources {
		seen[s.Type] = s.Root
	}
	for name, adapter := range adapters.Builtins() {
		roots := adapter.DefaultRoots()
		if len(roots) == 0 {
			if _, ok := seen[name]; ok {
				t.Fatalf("adapter %s has no default roots but appears in default config", name)
			}
			continue
		}
		if got, want := seen[name], roots[0].Path; got != want {
			t.Fatalf("default source %s root = %q, want %q; all built-ins must stay in config sync", name, got, want)
		}
	}
	if len(seen) != len(adapters.Builtins()) {
		t.Fatalf("default config has unexpected source set: %+v", cfg.Sources)
	}
}

func TestDefaultUsesSeparateLocalArchiveAndWorkspace(t *testing.T) {
	cfg := config.Default()
	if cfg.Schema != config.SchemaV1 {
		t.Fatalf("config schema=%q want %q", cfg.Schema, config.SchemaV1)
	}
	if cfg.Archive.Type != "local" || cfg.Archive.Location != "~/.aha/archive" || cfg.WorkspaceDir != "~/.aha/workspace" {
		t.Fatalf("bad resource defaults: archive=%+v workspace=%q", cfg.Archive, cfg.WorkspaceDir)
	}
}

func TestLoadRejectsUnsupportedConfigSchemaBeforeUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.jsonc")
	if err := os.WriteFile(path, []byte(`{"schema":"aha.config.v9","future_field":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := config.Load(path)
	var unsupported *config.UnsupportedSchemaError
	if !errors.As(err, &unsupported) {
		t.Fatalf("Load error=%v want *UnsupportedSchemaError", err)
	}
	if unsupported.Found != "aha.config.v9" || unsupported.Supported != config.SchemaV1 {
		t.Fatalf("unsupported schema error=%+v", unsupported)
	}
}

func TestConfigExtensionsRequireNamespacedKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.jsonc")
	if err := os.WriteFile(path, []byte(`{"schema":"aha.config.v1","extensions":{"future":true}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(path); err == nil {
		t.Fatal("accepted non-namespaced extension key")
	}
}

func TestConfigExtensionsRoundTripWithoutUnderstandingTheirContents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.jsonc")
	cfg := config.Default()
	cfg.Extensions = map[string]json.RawMessage{
		"example.com/tool": json.RawMessage(`{"future":true,"nested":{"value":7}}`),
	}
	if _, err := config.Write(path, cfg, "// retained header\n"); err != nil {
		t.Fatal(err)
	}
	got, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, got.Extensions["example.com/tool"]); err != nil {
		t.Fatal(err)
	}
	if compact.String() != `{"future":true,"nested":{"value":7}}` {
		t.Fatalf("extension changed: %s", got.Extensions["example.com/tool"])
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
  "workspace_dir": "/tmp/workspace",
}
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MachineID != "m1" || cfg.WorkspaceDir != "/tmp/workspace" || len(cfg.Sources) != 1 || cfg.Sources[0].Type != "pi" {
		t.Fatalf("bad config: %+v", cfg)
	}
}

func TestLoadRejectsRemovedConfigVocabulary(t *testing.T) {
	for _, key := range []string{"corpus_dir", "depot", "accept_secrets_warning"} {
		t.Run(key, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.jsonc")
			if err := os.WriteFile(path, []byte(`{"`+key+`": true}`), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := config.Load(path); err == nil {
				t.Fatalf("removed config key %q was accepted", key)
			}
		})
	}
}

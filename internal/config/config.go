package config

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/adewale/aha/internal/model"
	"github.com/adewale/aha/internal/paths"
	"github.com/tailscale/hujson"
)

func Default() model.Config {
	return model.Config{
		Sources: []model.SourceConfig{
			{Type: "pi", Root: "~/.pi/agent/sessions", Enabled: true},
			{Type: "claude-code", Root: "~/.claude/projects", Enabled: true},
		},
		CorpusDir:        "~/.aha",
		BundleOutDir:     "~/agent-session-bundles",
		PathMode:         "raw",
		IncludeSubagents: true,
		IncludeImages:    true,
		IndexToolOutput:  false,
		Redaction:        "none-v1",
	}
}

func DefaultPath() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "aha", "config.jsonc")
	}
	return "~/.config/aha/config.jsonc"
}

func Load(path string) (model.Config, error) {
	cfg := Default()
	if path == "" {
		path = DefaultPath()
	}
	expanded, err := paths.Expand(path)
	if err != nil {
		return cfg, err
	}
	b, err := os.ReadFile(expanded)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}
	ast, err := hujson.Parse(b)
	if err != nil {
		return cfg, err
	}
	ast.Standardize()
	if err := json.Unmarshal(ast.Pack(), &cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func StarterJSONC() []byte {
	cfg := Default()
	b, _ := json.MarshalIndent(cfg, "", "  ")
	return append([]byte("// aha config (JSONC)\n// Set machine_id before running snapshot.\n"), append(b, '\n')...)
}

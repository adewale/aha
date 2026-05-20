package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/adewale/aha/internal/model"
	"github.com/adewale/aha/internal/paths"
	"github.com/tailscale/hujson"
)

func Default() model.Config {
	return model.Config{
		MachineID: defaultMachineID(),
		Sources: []model.SourceConfig{
			{Type: "pi", Root: "~/.pi/agent/sessions", Enabled: true},
			{Type: "claude-code", Root: "~/.claude/projects", Enabled: true},
		},
		CorpusDir:            "~/.aha",
		BundleOutDir:         "~/agent-session-bundles",
		PathMode:             "raw",
		IncludeSubagents:     true,
		IncludeImages:        true,
		IndexToolOutput:      false,
		Redaction:            "none-v1",
		AcceptSecretsWarning: false,
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
	return append([]byte("// aha config (JSONC)\n// machine_id defaults to the local hostname; edit it if you want a stable label across renames.\n// Set accept_secrets_warning to true only after reading the privacy warning.\n"), append(b, '\n')...)
}

func defaultMachineID() string {
	host, err := os.Hostname()
	if err != nil || strings.TrimSpace(host) == "" {
		return "local"
	}
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(host) {
		ok := unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '_' || r == '-'
		if ok {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-._")
	if out == "" {
		return "local"
	}
	return out
}

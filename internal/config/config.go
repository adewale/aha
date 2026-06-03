package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/adewale/aha/internal/adapters"
	"github.com/adewale/aha/internal/model"
	"github.com/adewale/aha/internal/paths"
	"github.com/tailscale/hujson"
)

func Default() model.Config {
	return model.Config{
		MachineID:            defaultMachineID(),
		Sources:              defaultSources(),
		CorpusDir:            "~/.aha",
		Depot:                model.DepotConfig{Type: "local", Location: "~/.aha/depot"},
		PathMode:             "raw",
		IncludeSubagents:     true,
		IncludeImages:        true,
		IndexToolOutput:      false,
		Redaction:            "none-v1",
		AcceptSecretsWarning: false,
	}
}

func defaultSources() []model.SourceConfig {
	builtins := adapters.Builtins()
	names := make([]string, 0, len(builtins))
	for name := range builtins {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]model.SourceConfig, 0, len(names))
	for _, name := range names {
		roots := builtins[name].DefaultRoots()
		if len(roots) == 0 {
			continue
		}
		out = append(out, model.SourceConfig{Type: name, Root: roots[0].Path, Enabled: true})
	}
	return out
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
	header := "// aha config (JSONC)\n" +
		"// machine_id defaults to the local hostname; edit it if you want a stable label across renames.\n" +
		"// Set accept_secrets_warning to true only after reading the privacy warning.\n" +
		"//\n" +
		"// depot: where snapshots are stored. Local is the default until you configure one.\n" +
		"//   Configure a depot with `aha depot init <addr>` (e.g. r2:aha-depot or local:/path);\n" +
		"//   switch the default later with `aha depot use <addr>`. Either command sets the default.\n" +
		"// R2 depots: keep the two secret keys in the environment, never in this file —\n" +
		"//   AHA_R2_ACCESS_KEY_ID and AHA_R2_SECRET_ACCESS_KEY. `aha depot init`/`use` persist the\n" +
		"//   non-secret depot.r2.account_id for you, so a flagless `aha refresh` then targets R2.\n" +
		"//   direnv (.envrc): export AHA_R2_ACCESS_KEY_ID=... ; export AHA_R2_SECRET_ACCESS_KEY=...\n"
	return append([]byte(header), append(b, '\n')...)
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

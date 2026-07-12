package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/adewale/aha/internal/adapters"
	"github.com/adewale/aha/internal/model"
	"github.com/adewale/aha/internal/paths"
	"github.com/tailscale/hujson"
)

const SchemaV1 = "aha.config.v1"

// UnsupportedSchemaError distinguishes a valid newer config from a misspelt
// field in the current schema. Commands must refuse it before mutation.
type UnsupportedSchemaError struct {
	Found     string
	Supported string
}

func (e *UnsupportedSchemaError) Error() string {
	return "config schema " + strconv.Quote(e.Found) + " is not supported; upgrade aha before using this config"
}

func Default() model.Config {
	return model.Config{
		Schema:                 SchemaV1,
		MachineID:              defaultMachineID(),
		Sources:                defaultSources(),
		WorkspaceDir:           "~/.aha/workspace",
		Archive:                model.ArchiveConfig{Type: "local", Location: "~/.aha/archive"},
		PathMode:               "raw",
		IncludeSubagents:       true,
		IncludeImages:          true,
		IndexToolOutput:        false,
		Redaction:              "none-v1",
		AcknowledgedRawHistory: false,
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
	packed := ast.Pack()
	var envelope struct {
		Schema string `json:"schema"`
	}
	if err := json.Unmarshal(packed, &envelope); err != nil {
		return cfg, err
	}
	if envelope.Schema != "" && envelope.Schema != SchemaV1 {
		return cfg, &UnsupportedSchemaError{Found: envelope.Schema, Supported: SchemaV1}
	}
	decoder := json.NewDecoder(bytes.NewReader(packed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return cfg, err
	}
	// Configs created before schemas were explicit are the baseline v1 shape.
	if cfg.Schema == "" {
		cfg.Schema = SchemaV1
	}
	for namespace := range cfg.Extensions {
		host, name, ok := strings.Cut(namespace, "/")
		if !ok || !strings.Contains(host, ".") || strings.TrimSpace(host) != host || strings.TrimSpace(name) == "" || strings.TrimSpace(name) != name {
			return cfg, fmt.Errorf("config extension key %q must use a reverse-domain namespace such as example.com/tool", namespace)
		}
	}
	return cfg, nil
}

func StarterJSONC() []byte {
	cfg := Default()
	b, _ := json.MarshalIndent(cfg, "", "  ")
	header := "// aha config (JSONC)\n" +
		"// machine_id defaults to the local hostname; edit it if you want a stable label across renames.\n" +
		"// Set acknowledged_raw_history to true only after reading the privacy warning.\n" +
		"//\n" +
		"// archive: durable aggregated history. Local is the default until you configure one.\n" +
		"//   Initialise an Archive with `aha archive init <address>` (for example r2:aha-archive or local:/path).\n" +
		"//   Select it with `aha archive set-default <address>`.\n" +
		"// R2 Archives: keep the two secret keys in the environment, never in this file —\n" +
		"//   AHA_R2_ACCESS_KEY_ID and AHA_R2_SECRET_ACCESS_KEY. Archive commands persist only\n" +
		"//   the non-secret archive.r2.account_id.\n" +
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

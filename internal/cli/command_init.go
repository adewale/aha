package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/adewale/aha/internal/config"
	"github.com/adewale/aha/internal/paths"
)

func cmdInit(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(flagOutput(args, stderr))
	configPath := fs.String("config", config.DefaultPath(), "config path")
	acceptSecrets := fs.Bool("accept-secrets", false, "write config with privacy warning acknowledged")
	jsonOut := fs.Bool("json", false, "JSON output")
	if err := fs.Parse(args); err != nil {
		return err
	}
	path, err := paths.Expand(*configPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("config already exists: %s", path)
	}
	content := config.StarterJSONC()
	if *acceptSecrets {
		cfg := config.Default()
		cfg.AcceptSecretsWarning = true
		b, _ := json.MarshalIndent(cfg, "", "  ")
		content = append([]byte("// aha config (JSONC)\n// Privacy warning acknowledged by `aha init --accept-secrets`. Bundles/corpora remain private.\n"), append(b, '\n')...)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		return err
	}
	if *jsonOut {
		return writeJSON(stdout, map[string]any{"config": path, "accepted_secrets": *acceptSecrets})
	}
	fmt.Fprintln(stdout, path)
	return nil
}

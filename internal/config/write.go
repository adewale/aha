package config

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/adewale/aha/internal/model"
	"github.com/adewale/aha/internal/paths"
)

func Write(path string, cfg model.Config, header string) (string, error) {
	if path == "" {
		path = DefaultPath()
	}
	expanded, err := paths.Expand(path)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(expanded), 0o755); err != nil {
		return "", err
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", err
	}
	if header == "" {
		header = "// aha config (JSONC)\n"
	}
	content := append([]byte(header), append(b, '\n')...)
	return expanded, os.WriteFile(expanded, content, 0o644)
}

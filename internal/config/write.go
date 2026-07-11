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
	dir := filepath.Dir(expanded)
	if err := os.MkdirAll(dir, 0o755); err != nil {
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

	mode := os.FileMode(0o600)
	if info, statErr := os.Lstat(expanded); statErr == nil && info.Mode().IsRegular() {
		mode = info.Mode().Perm()
	} else if statErr != nil && !os.IsNotExist(statErr) {
		return "", statErr
	}
	tmp, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}
	if err := tmp.Chmod(mode); err != nil {
		cleanup()
		return "", err
	}
	if _, err := tmp.Write(content); err != nil {
		cleanup()
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", err
	}
	if err := os.Rename(tmpPath, expanded); err != nil {
		_ = os.Remove(tmpPath)
		return "", err
	}
	if dirHandle, err := os.Open(dir); err == nil {
		_ = dirHandle.Sync()
		_ = dirHandle.Close()
	}
	return expanded, nil
}

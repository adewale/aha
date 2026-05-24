package config_test

import (
	"path/filepath"
	"testing"
	"testing/quick"

	"github.com/adewale/aha/internal/config"
	"github.com/adewale/aha/internal/model"
)

func TestConfigWriteLoadRoundTripProperty(t *testing.T) {
	prop := func(machine, depotName, corpusName, sourceRoot string) bool {
		cfg := config.Default()
		cfg.MachineID = safeConfigValue(machine, "machine")
		cfg.CorpusDir = filepath.Join(t.TempDir(), safeConfigValue(corpusName, "corpus"))
		cfg.Depot = model.DepotConfig{Type: "local", Location: filepath.Join(t.TempDir(), safeConfigValue(depotName, "depot"))}
		cfg.Sources = []model.SourceConfig{{Type: "pi", Root: filepath.Join(t.TempDir(), safeConfigValue(sourceRoot, "source")), Enabled: true}}
		path := filepath.Join(t.TempDir(), "config.jsonc")
		if _, err := config.Write(path, cfg, "// property config\n"); err != nil {
			return false
		}
		got, err := config.Load(path)
		if err != nil {
			return false
		}
		if got.MachineID != cfg.MachineID || got.CorpusDir != cfg.CorpusDir || got.Depot != cfg.Depot || len(got.Sources) != 1 || got.Sources[0] != cfg.Sources[0] {
			return false
		}
		return true
	}
	if err := quick.Check(prop, &quick.Config{MaxCount: 50}); err != nil {
		t.Fatal(err)
	}
}

func safeConfigValue(s, fallback string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			out = append(out, r)
		}
	}
	if len(out) == 0 {
		return fallback
	}
	if len(out) > 32 {
		out = out[:32]
	}
	return string(out)
}

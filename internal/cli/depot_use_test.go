package cli_test

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adewale/aha/internal/cli"
	"github.com/adewale/aha/internal/config"
)

// `aha depot use <addr>` switches the default depot to an already-initialized
// depot, and refuses (pointing at `aha depot init`) when the target is
// reachable but not yet provisioned.
func TestDepotUseSwitchesDefaultDepot(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.jsonc")
	depotA := filepath.Join(root, "depotA")
	depotB := filepath.Join(root, "depotB")

	seed := `{
		"machine_id":"use-test",
		"sources":[],
		"corpus_dir":"` + filepath.ToSlash(filepath.Join(root, "corpus")) + `",
		"depot":{"type":"local","location":"` + filepath.ToSlash(depotA) + `"},
		"accept_secrets_warning":true
	}`
	if err := os.WriteFile(configPath, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	mustRun := func(args ...string) {
		t.Helper()
		out.Reset()
		if err := cli.Run(args, &out, io.Discard); err != nil {
			t.Fatalf("aha %s: %v", strings.Join(args, " "), err)
		}
	}

	// Provision both depots; each init also sets the default, so after the
	// second init the default is B.
	mustRun("depot", "init", "--config", configPath, "local:"+depotA)
	mustRun("depot", "init", "--config", configPath, "local:"+depotB)

	// Switch the default back to the already-initialized A.
	mustRun("depot", "use", "--config", configPath, "local:"+depotA)

	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Depot.Type != "local" || cfg.Depot.Location != depotA {
		t.Fatalf("depot use did not switch default to A: got %s:%s", cfg.Depot.Type, cfg.Depot.Location)
	}

	// `use` on a reachable-but-uninitialized depot must refuse, name `init`,
	// and leave the configured default untouched.
	empty := filepath.Join(root, "empty")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	err = cli.Run([]string{"depot", "use", "--config", configPath, "local:" + empty}, &out, io.Discard)
	if err == nil {
		t.Fatal("depot use accepted an uninitialized depot")
	}
	if !strings.Contains(err.Error(), "init") {
		t.Fatalf("error should point at `aha depot init`: %v", err)
	}
	cfg2, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg2.Depot.Location != depotA {
		t.Fatalf("a failed `depot use` changed the default: got %s", cfg2.Depot.Location)
	}
}

package cli_test

import (
	"bytes"
	"encoding/json"
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

	out.Reset()
	if err := cli.Run([]string{"depot", "use", "--config", configPath, "--json", "local:" + depotB}, &out, io.Discard); err != nil {
		t.Fatalf("aha depot use --json: %v", err)
	}
	var payload struct {
		Switched bool   `json:"switched"`
		Config   string `json:"config"`
		Depot    struct {
			Type     string `json:"Type"`
			Location string `json:"Location"`
		} `json:"depot"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("decode depot use --json: %v\n%s", err, out.String())
	}
	if !payload.Switched || payload.Config == "" || payload.Depot.Type != "local" || payload.Depot.Location != depotB {
		t.Fatalf("unexpected depot use --json payload: %+v", payload)
	}
	mustRun("depot", "use", "--config", configPath, "local:"+depotA)

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
	wantInit := "run `aha depot init local:" + empty + "` first"
	if !strings.Contains(err.Error(), wantInit) {
		t.Fatalf("error should point at exact init command %q: %v", wantInit, err)
	}
	if _, statErr := os.Stat(filepath.Join(empty, "depot.json")); !os.IsNotExist(statErr) {
		t.Fatalf("failed depot use should not initialize target marker, stat err=%v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(empty, "catalog")); !os.IsNotExist(statErr) {
		t.Fatalf("failed depot use should not create target catalog dir, stat err=%v", statErr)
	}
	cfg2, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg2.Depot.Location != depotA {
		t.Fatalf("a failed `depot use` changed the default: got %s", cfg2.Depot.Location)
	}
}

func TestDepotUseMissingAddressDoesNotOpenCurrentDepot(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.jsonc")
	seed := `{
		"machine_id":"use-test",
		"sources":[],
		"corpus_dir":"` + filepath.ToSlash(filepath.Join(root, "corpus")) + `",
		"depot":{"type":"r2","location":"aha-depot"},
		"accept_secrets_warning":true
	}`
	if err := os.WriteFile(configPath, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AHA_R2_ACCOUNT_ID", "")
	t.Setenv("R2_ACCOUNT_ID", "")
	t.Setenv("AHA_R2_ACCESS_KEY_ID", "")
	t.Setenv("R2_ACCESS_KEY_ID", "")
	t.Setenv("AHA_R2_SECRET_ACCESS_KEY", "")
	t.Setenv("R2_SECRET_ACCESS_KEY", "")

	var out bytes.Buffer
	err := cli.Run([]string{"depot", "use", "--config", configPath}, &out, io.Discard)
	if err == nil {
		t.Fatal("depot use without an address succeeded")
	}
	if !strings.Contains(err.Error(), "depot use requires a depot address") {
		t.Fatalf("missing address should be reported before opening current depot, got: %v", err)
	}
}

func TestDoctorReportsPopulatedMissingMarkerDepotAsDegraded(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.jsonc")
	piRoot := filepath.Join(root, "pi", "--Users-me-proj--")
	if err := os.MkdirAll(piRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	piFile := filepath.Join(piRoot, "2026.jsonl")
	if err := os.WriteFile(piFile, []byte(`{"type":"session","version":3,"id":"pi-session","timestamp":"2026-01-01T00:00:00Z","cwd":"/Users/me/proj"}
{"id":"p1","parentId":"","type":"user","role":"user","timestamp":"2026-01-01T00:00:01Z","message":{"content":"hello"}}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	depotDir := filepath.Join(root, "depot")
	seed := `{
		"machine_id":"use-test",
		"sources":[{"type":"pi","root":"` + filepath.ToSlash(piRoot) + `","enabled":true}],
		"corpus_dir":"` + filepath.ToSlash(filepath.Join(root, "corpus")) + `",
		"depot":{"type":"local","location":"` + filepath.ToSlash(depotDir) + `"},
		"accept_secrets_warning":true
	}`
	if err := os.WriteFile(configPath, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := cli.Run([]string{"refresh", "--config", configPath, "--captured-at", "2026-01-01T00:00:00Z", "--bundle-id", "doctor-degraded"}, &out, io.Discard); err != nil {
		t.Fatalf("seed refresh: %v", err)
	}
	if err := os.Remove(filepath.Join(depotDir, "depot.json")); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	if err := cli.Run([]string{"doctor", "--config", configPath, "--json"}, &out, io.Discard); err != nil {
		t.Fatalf("doctor: %v", err)
	}
	var doc struct {
		Depot struct {
			OK          bool     `json:"ok"`
			Initialized bool     `json:"initialized"`
			Bundles     int      `json:"bundles"`
			Catalogs    int      `json:"catalogs"`
			Problems    []string `json:"problems"`
			Next        []string `json:"next"`
		} `json:"depot"`
	}
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("decode doctor: %v\n%s", err, out.String())
	}
	if doc.Depot.OK || !doc.Depot.Initialized || len(doc.Depot.Problems) == 0 {
		t.Fatalf("populated depot missing marker should be degraded, got %+v", doc.Depot)
	}
	if doc.Depot.Bundles == 0 || doc.Depot.Catalogs == 0 {
		t.Fatalf("test did not create a populated depot: %+v", doc.Depot)
	}
	wantRepair := "aha depot verify local:" + depotDir + " --repair"
	joinedNext := strings.Join(doc.Depot.Next, "\n")
	if !strings.Contains(joinedNext, wantRepair) || strings.Contains(joinedNext, "depot init") {
		t.Fatalf("degraded populated depot should point at repair %q, got %+v", wantRepair, doc.Depot)
	}

	out.Reset()
	if err := cli.Run([]string{"doctor", "--config", configPath}, &out, io.Discard); err != nil {
		t.Fatalf("doctor human: %v", err)
	}
	text := out.String()
	if !strings.Contains(text, "depot problem: missing depot marker") || !strings.Contains(text, "next: "+wantRepair) {
		t.Fatalf("human doctor should show degraded problem and repair next action, got:\n%s", text)
	}
}

func TestDepotUseRejectsPopulatedMissingMarkerDepotAsDegraded(t *testing.T) {
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
	if err := cli.Run([]string{"depot", "init", "--config", configPath, "local:" + depotA}, &out, io.Discard); err != nil {
		t.Fatalf("init A: %v", err)
	}
	if err := cli.Run([]string{"depot", "init", "--config", configPath, "local:" + depotB}, &out, io.Discard); err != nil {
		t.Fatalf("init B: %v", err)
	}
	if err := cli.Run([]string{"depot", "use", "--config", configPath, "local:" + depotA}, &out, io.Discard); err != nil {
		t.Fatalf("switch back to A: %v", err)
	}
	sha := strings.Repeat("a", 64)
	bundlePath := filepath.Join(depotB, "bundles", "v1", sha+".tar.zst")
	if err := os.MkdirAll(filepath.Dir(bundlePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bundlePath, []byte("not a real bundle; quick verify must not read it"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(depotB, "depot.json")); err != nil {
		t.Fatal(err)
	}

	err := cli.Run([]string{"depot", "use", "--config", configPath, "local:" + depotB}, &out, io.Discard)
	if err == nil {
		t.Fatal("depot use accepted a degraded populated depot")
	}
	wantRepair := "aha depot verify local:" + depotB + " --repair"
	if !strings.Contains(err.Error(), wantRepair) || strings.Contains(err.Error(), "depot init") {
		t.Fatalf("degraded depot use should point at repair %q, got: %v", wantRepair, err)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Depot.Location != depotA {
		t.Fatalf("failed `depot use` should leave config at prior default depotA, got %s", cfg.Depot.Location)
	}
}

func TestDepotFlagsMayFollowDepotAddress(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.jsonc")
	depotDir := filepath.Join(root, "depot")
	seed := `{
		"machine_id":"use-test",
		"sources":[],
		"corpus_dir":"` + filepath.ToSlash(filepath.Join(root, "corpus")) + `",
		"depot":{"type":"local","location":"` + filepath.ToSlash(depotDir) + `"},
		"accept_secrets_warning":true
	}`
	if err := os.WriteFile(configPath, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := cli.Run([]string{"depot", "init", "--config", configPath, "local:" + depotDir}, &out, io.Discard); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := os.Remove(filepath.Join(depotDir, "depot.json")); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	if err := cli.Run([]string{"depot", "verify", "--config", configPath, "local:" + depotDir, "--repair", "--json"}, &out, io.Discard); err != nil {
		t.Fatalf("verify with flags after depot address: %v", err)
	}
	if !json.Valid(out.Bytes()) {
		t.Fatalf("--json after depot address was ignored; output was:\n%s", out.String())
	}
	var report struct {
		Repaired bool `json:"repaired"`
	}
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("decode verify report: %v\n%s", err, out.String())
	}
	if !report.Repaired {
		t.Fatalf("--repair after depot address was ignored: %s", out.String())
	}
	if _, err := os.Stat(filepath.Join(depotDir, "depot.json")); err != nil {
		t.Fatalf("repair did not recreate marker: %v", err)
	}
}

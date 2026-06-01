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
	"github.com/adewale/aha/internal/corpus"
	"github.com/adewale/aha/internal/testutil"
)

func TestCLIDoctorReportsR2ConfigurationMistakes(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "aws-key")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "aws-secret")
	var out bytes.Buffer
	if err := cli.Run([]string{"doctor", "--depot", "r2:https://pub-example.r2.dev", "--json"}, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	body := out.String()
	for _, want := range []string{"depot address should be r2:BUCKET", "public r2.dev", "AHA ignores AWS_ACCESS_KEY_ID"} {
		if !strings.Contains(body, want) {
			t.Fatalf("doctor missing %q in %s", want, body)
		}
	}
	if strings.Contains(body, "aws-secret") {
		t.Fatalf("doctor leaked AWS secret: %s", body)
	}
}

func TestCLIDoctorReportsDepotSourceAndCorpusDiagnostics(t *testing.T) {
	root := t.TempDir()
	fx := testutil.WriteAgentFixtures(t, root)
	configPath := filepath.Join(root, "config.jsonc")
	depotDir := filepath.Join(root, "depot")
	corpusDir := filepath.Join(root, "corpus")
	cfg := `{
		"machine_id":"doctor-machine",
		"sources":[{"type":"pi","root":"` + filepath.ToSlash(fx.PiRoot) + `","enabled":true}],
		"corpus_dir":"` + filepath.ToSlash(corpusDir) + `",
		"depot":{"type":"local","location":"` + filepath.ToSlash(depotDir) + `"},
		"accept_secrets_warning":true
	}`
	if err := os.WriteFile(configPath, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := cli.Run([]string{"depot", "init", "--config", configPath, "local:" + depotDir}, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := cli.Run([]string{"doctor", "--config", configPath, "--depot", "local:" + depotDir, "--json"}, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	body := out.String()
	for _, want := range []string{`"depot"`, `"ok": true`, depotDir, `"sources"`, `"session_files"`, `"corpus"`, corpusDir} {
		if !strings.Contains(body, want) {
			t.Fatalf("doctor missing %q in %s", want, body)
		}
	}
}

func TestRunMainWritesOptionalProfiles(t *testing.T) {
	root := t.TempDir()
	cpuProfile := filepath.Join(root, "cpu.pprof")
	memProfile := filepath.Join(root, "heap.pprof")
	var out, stderr bytes.Buffer
	code := cli.RunMain([]string{"version", "--cpuprofile", cpuProfile, "--memprofile=" + memProfile}, &out, &stderr)
	if code != 0 {
		t.Fatalf("RunMain code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(out.String(), "aha ") {
		t.Fatalf("version output missing: %q", out.String())
	}
	for _, path := range []string{cpuProfile, memProfile} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("profile %s missing: %v", path, err)
		}
		if info.Size() == 0 {
			t.Fatalf("profile %s is empty", path)
		}
	}
}

func TestRunMainUsesProfileEnvironment(t *testing.T) {
	memProfile := filepath.Join(t.TempDir(), "env-heap.pprof")
	t.Setenv("AHA_MEM_PROFILE", memProfile)
	var out, stderr bytes.Buffer
	code := cli.RunMain([]string{"version"}, &out, &stderr)
	if code != 0 {
		t.Fatalf("RunMain code=%d stderr=%s", code, stderr.String())
	}
	info, err := os.Stat(memProfile)
	if err != nil {
		t.Fatalf("env profile missing: %v", err)
	}
	if info.Size() == 0 {
		t.Fatalf("env profile is empty")
	}
}

func TestRunMainRejectsProfileFlagWithoutPath(t *testing.T) {
	var out, stderr bytes.Buffer
	code := cli.RunMain([]string{"--cpuprofile"}, &out, &stderr)
	if code == 0 || !strings.Contains(stderr.String(), "--cpuprofile requires path") {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}

func TestSubcommandHelpIsSuccessful(t *testing.T) {
	for _, cmd := range cli.CommandNames() {
		var out bytes.Buffer
		if err := cli.Run([]string{cmd, "--help"}, &out, &out); err != nil {
			t.Fatalf("%s --help returned error: %v output=%s", cmd, err, out.String())
		}
		if !strings.Contains(out.String(), "Usage of") {
			t.Fatalf("%s --help missing usage: %s", cmd, out.String())
		}
	}
}

func TestReadRejectsLegacyRefsBeforeOpeningCorpus(t *testing.T) {
	for _, ref := range []string{"pi:m:s#e", "artifact:abc123"} {
		t.Run(ref, func(t *testing.T) {
			var out bytes.Buffer
			err := cli.Run([]string{"read", ref, "--json"}, &out, &out)
			if err == nil || !strings.Contains(err.Error(), "unsupported ref format") {
				t.Fatalf("read legacy ref err=%v output=%s", err, out.String())
			}
		})
	}
}

func TestOutputModesAreMutuallyExclusive(t *testing.T) {
	var out bytes.Buffer
	for _, args := range [][]string{
		{"search", "needle", "--json", "--refs"},
		{"search", "needle", "--files", "--md"},
		{"read", "pi:m:s#e", "--json", "--md"},
	} {
		err := cli.Run(args, &out, io.Discard)
		if err == nil || !strings.Contains(err.Error(), "mutually exclusive output modes") {
			t.Fatalf("Run(%v) err=%v, want mutually exclusive output modes", args, err)
		}
		out.Reset()
	}
}

func snapshotPathFromOutput(t *testing.T, output string) string {
	t.Helper()
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasSuffix(line, ".tar.zst") {
			return line
		}
	}
	t.Fatalf("snapshot output missing bundle path: %s", output)
	return ""
}

func TestSnapshotJSONIncludesGeneratedMetadata(t *testing.T) {
	root := t.TempDir()
	fx := testutil.WriteAgentFixtures(t, root)
	outDir := filepath.Join(root, "bundles")
	var out bytes.Buffer
	if err := cli.Run([]string{"snapshot", "--json", "--accept-secrets", "--machine", "m1", "--source", "pi=" + fx.PiRoot, "--depot", "local:" + outDir}, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), `"bundle_id": ""`) || strings.Contains(out.String(), `"captured_at": ""`) {
		t.Fatalf("snapshot JSON omitted generated metadata: %s", out.String())
	}
	if !strings.Contains(out.String(), `"bundle_id"`) || !strings.Contains(out.String(), `"captured_at"`) {
		t.Fatalf("snapshot JSON missing metadata fields: %s", out.String())
	}
}

func TestSnapshotRequiresPrivacyAcknowledgement(t *testing.T) {
	root := t.TempDir()
	fx := testutil.WriteAgentFixtures(t, root)
	outDir := filepath.Join(root, "bundles")
	var out, stderr bytes.Buffer
	err := cli.Run([]string{"snapshot", "--machine", "m1", "--source", "pi=" + fx.PiRoot, "--depot", "local:" + outDir}, &out, &stderr)
	if err == nil {
		t.Fatalf("snapshot succeeded without privacy acknowledgement")
	}
	if !strings.Contains(stderr.String(), "does not redact secrets") {
		t.Fatalf("missing privacy warning: %s", stderr.String())
	}
	matches, _ := filepath.Glob(filepath.Join(outDir, "bundles", "v1", "*.tar.zst"))
	if len(matches) != 0 {
		t.Fatalf("snapshot wrote bundles despite rejected privacy acknowledgement: %v", matches)
	}
}

func TestCLIRejectsWritesInsideSourceRoots(t *testing.T) {
	root := t.TempDir()
	fx := testutil.WriteAgentFixtures(t, root)
	insideSource := filepath.Join(fx.PiRoot, "aha-output")
	var out, stderr bytes.Buffer
	err := cli.Run([]string{"snapshot", "--accept-secrets", "--machine", "m1", "--source", "pi=" + fx.PiRoot, "--depot", "local:" + insideSource}, &out, &stderr)
	if err == nil || !strings.Contains(err.Error(), "must not be inside source root") {
		t.Fatalf("snapshot did not reject output inside source root: err=%v stderr=%s", err, stderr.String())
	}
	outDir := filepath.Join(root, "bundles")
	err = cli.Run([]string{"refresh", "--accept-secrets", "--machine", "m1", "--source", "pi=" + fx.PiRoot, "--depot", "local:" + outDir, "--repo", insideSource}, &out, &stderr)
	if err == nil || !strings.Contains(err.Error(), "must not be inside source root") {
		t.Fatalf("refresh did not reject repo inside source root before snapshot: err=%v", err)
	}
	matches, _ := filepath.Glob(filepath.Join(outDir, "bundles", "v1", "*.tar.zst"))
	if len(matches) != 0 {
		t.Fatalf("refresh wrote snapshot before repo validation: %v", matches)
	}
	cfgPath := filepath.Join(root, "config.jsonc")
	if err := os.WriteFile(cfgPath, []byte(`{"machine_id":"m1","accept_secrets_warning":true,"depot":{"type":"local","location":"`+filepath.ToSlash(filepath.Join(root, "bundles"))+`"},"corpus_dir":"`+filepath.ToSlash(filepath.Join(root, "corpus"))+`","sources":[{"type":"pi","root":"`+filepath.ToSlash(fx.PiRoot)+`","enabled":true}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	err = cli.Run([]string{"ingest", "--config", cfgPath, "--repo", insideSource, filepath.Join(root, "bundle.tar.zst")}, &out, &stderr)
	if err == nil || !strings.Contains(err.Error(), "must not be inside source root") {
		t.Fatalf("ingest did not reject repo inside source root: err=%v", err)
	}
	err = cli.Run([]string{"depot", "init", "--config", cfgPath, "local:" + insideSource}, &out, &stderr)
	if err == nil || !strings.Contains(err.Error(), "must not be inside source root") {
		t.Fatalf("depot init did not reject depot inside source root: err=%v", err)
	}
	linkToSource := filepath.Join(root, "repo-link")
	if err := os.Symlink(fx.PiRoot, linkToSource); err == nil {
		err = cli.Run([]string{"ingest", "--config", cfgPath, "--repo", filepath.Join(linkToSource, "corpus"), filepath.Join(root, "bundle.tar.zst")}, &out, &stderr)
		if err == nil || !strings.Contains(err.Error(), "must not be inside source root") {
			t.Fatalf("ingest did not reject symlinked repo into source root: err=%v", err)
		}
	}
}

func TestCLIReadCommandsDoNotCreateMissingCorpus(t *testing.T) {
	root := t.TempDir()
	fx := testutil.WriteAgentFixtures(t, root)
	missingRepo := filepath.Join(root, "missing-corpus")
	cfgPath := filepath.Join(root, "config.jsonc")
	if err := os.WriteFile(cfgPath, []byte(`{"machine_id":"m1","accept_secrets_warning":true,"depot":{"type":"local","location":"`+filepath.ToSlash(filepath.Join(root, "bundles"))+`"},"corpus_dir":"`+filepath.ToSlash(filepath.Join(root, "corpus"))+`","sources":[{"type":"pi","root":"`+filepath.ToSlash(fx.PiRoot)+`","enabled":true}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	commands := [][]string{
		{"search", "needle", "--config", cfgPath, "--repo", missingRepo},
		{"read", "--session", "x", "--config", cfgPath, "--repo", missingRepo},
		{"status", "--config", cfgPath, "--repo", missingRepo},
		{"conflicts", "--config", cfgPath, "--repo", missingRepo},
	}
	for _, args := range commands {
		var out bytes.Buffer
		if err := cli.Run(args, &out, &out); err == nil {
			t.Fatalf("%v unexpectedly created/opened missing corpus", args)
		}
		if _, err := os.Stat(missingRepo); !os.IsNotExist(err) {
			t.Fatalf("%v created repo dir: %v", args, err)
		}
	}
}

func TestCLIRefreshCreatesAggregationCorpus(t *testing.T) {
	root := t.TempDir()
	fx := testutil.WriteAgentFixtures(t, root)
	outDir := filepath.Join(root, "bundles")
	corpusDir := filepath.Join(root, "corpus")
	configPath := filepath.Join(root, "config.jsonc")
	cfg := `{
		"machine_id":"refresh-machine",
		"sources":[
			{"type":"pi","root":"` + filepath.ToSlash(fx.PiRoot) + `","enabled":true},
			{"type":"claude-code","root":"` + filepath.ToSlash(fx.ClaudeRoot) + `","enabled":true}
		],
		"corpus_dir":"` + filepath.ToSlash(corpusDir) + `",
		"depot":{"type":"local","location":"` + filepath.ToSlash(outDir) + `"},
		"path_mode":"raw",
		"include_subagents":true,
		"include_images":true,
		"index_tool_output":false,
		"redaction":"none-v1",
		"accept_secrets_warning":true
	}`
	if err := os.WriteFile(configPath, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := cli.Run([]string{"refresh", "--config", configPath, "--captured-at", "2026-01-03T00:00:00Z", "--bundle-id", "refresh"}, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "sha256:") || !strings.Contains(out.String(), "sessions=3") {
		t.Fatalf("bad refresh output: %s", out.String())
	}
	out.Reset()
	if err := cli.Run([]string{"search", "--config", configPath, "needle"}, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "claude-code") {
		t.Fatalf("refresh did not create searchable corpus: %s", out.String())
	}
}

func TestCLIRepoAliasAndSessionScopedJourneys(t *testing.T) {
	root := t.TempDir()
	fx := testutil.WriteAgentFixtures(t, root)
	outDir := filepath.Join(root, "bundles")
	repoDir := filepath.Join(root, "repo")
	var out bytes.Buffer
	if err := cli.Run([]string{"snapshot", "--machine", "scoped", "--source", "pi=" + fx.PiRoot, "--source", "claude-code=" + fx.ClaudeRoot, "--depot", "local:" + outDir, "--accept-secrets", "--session", "pi-session", "--captured-at", "2026-01-03T00:00:00Z", "--bundle-id", "pi-only"}, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	bundle := snapshotPathFromOutput(t, out.String())
	out.Reset()
	if err := cli.Run([]string{"ingest", "--repo", repoDir, bundle}, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "sessions=1") {
		t.Fatalf("expected one scoped session: %s", out.String())
	}
	out.Reset()
	if err := cli.Run([]string{"search", "--repo", repoDir, "needle"}, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "pi") || strings.Contains(out.String(), "claude-code") {
		t.Fatalf("repo/session scoped search mismatch: %s", out.String())
	}
}

func TestCLIRefreshCanLimitLocalSessions(t *testing.T) {
	root := t.TempDir()
	fx := testutil.WriteAgentFixtures(t, root)
	outDir := filepath.Join(root, "bundles")
	repoDir := filepath.Join(root, "repo")
	var out bytes.Buffer
	if err := cli.Run([]string{"refresh", "--machine", "limited", "--source", "pi=" + fx.PiRoot, "--source", "claude-code=" + fx.ClaudeRoot, "--depot", "local:" + outDir, "--repo", repoDir, "--accept-secrets", "--max-sessions", "1", "--captured-at", "2026-01-03T00:00:00Z", "--bundle-id", "one"}, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "sessions=1") {
		t.Fatalf("expected refresh to ingest one session: %s", out.String())
	}
}

func TestCLIDepotInitBindsConfig(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.jsonc")
	depotDir := filepath.Join(root, "bound-depot")
	var out bytes.Buffer
	if err := cli.Run([]string{"depot", "init", "--config", configPath, "local:" + depotDir, "--json"}, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), depotDir) || !strings.Contains(out.String(), configPath) {
		t.Fatalf("depot init did not report bound config: %s", out.String())
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Depot.Type != "local" || cfg.Depot.Location != depotDir {
		t.Fatalf("config depot not bound: %+v", cfg.Depot)
	}
}

func TestCLILocalDepotSnapshotIngestJourney(t *testing.T) {
	root := t.TempDir()
	fx := testutil.WriteAgentFixtures(t, root)
	depotDir := filepath.Join(root, "depot")
	corpusDir := filepath.Join(root, "corpus")
	var out bytes.Buffer
	if err := cli.Run([]string{"snapshot", "--machine", "m1", "--source", "pi=" + fx.PiRoot, "--depot", "local:" + depotDir, "--accept-secrets", "--captured-at", "2026-01-03T00:00:00Z", "--bundle-id", "depot-cli"}, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), filepath.Join("bundles", "v1")) || !strings.Contains(out.String(), "sha256:") {
		t.Fatalf("snapshot did not write content-addressed depot bundle: %s", out.String())
	}
	receipts, err := filepath.Glob(filepath.Join(depotDir, "bundles", "v1", "*.receipt.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(receipts) != 0 {
		t.Fatalf("snapshot wrote removed receipt sidecars: %v", receipts)
	}
	out.Reset()
	if err := cli.Run([]string{"ingest", "--corpus", corpusDir, "--depot", "local:" + depotDir}, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "sessions=1") {
		t.Fatalf("ingest from local depot failed: %s", out.String())
	}
	out.Reset()
	if err := cli.Run([]string{"status", "--corpus", corpusDir, "--depot", "local:" + depotDir, "--json"}, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"depot_behind_bundles": 0`) {
		t.Fatalf("status did not compare depot/corpus: %s", out.String())
	}
}

func TestCLIRefreshIsIdempotentWhenSourcesUnchanged(t *testing.T) {
	root := t.TempDir()
	fx := testutil.WriteAgentFixtures(t, root)
	depotDir := filepath.Join(root, "depot")
	corpusDir := filepath.Join(root, "corpus")
	args := []string{"refresh", "--machine", "idem", "--source", "pi=" + fx.PiRoot, "--depot", "local:" + depotDir, "--corpus", corpusDir, "--accept-secrets"}
	var out bytes.Buffer
	if err := cli.Run(args, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := cli.Run(args, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	matches, err := filepath.Glob(filepath.Join(depotDir, "bundles", "v1", "*.tar.zst"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("refresh should not create a second unchanged bundle, got %d: %v", len(matches), matches)
	}
	if strings.Contains(out.String(), "sessions=") {
		t.Fatalf("second refresh should not re-ingest already-current corpus: %s", out.String())
	}
}

func TestCLIDefaultSnapshotAndIngestJourney(t *testing.T) {
	root := t.TempDir()
	fx := testutil.WriteAgentFixtures(t, root)
	outDir := filepath.Join(root, "bundles")
	corpusDir := filepath.Join(root, "corpus")
	configPath := filepath.Join(root, "config.jsonc")
	cfg := `{
		"machine_id":"journey-machine",
		"sources":[
			{"type":"pi","root":"` + filepath.ToSlash(fx.PiRoot) + `","enabled":true},
			{"type":"claude-code","root":"` + filepath.ToSlash(fx.ClaudeRoot) + `","enabled":true}
		],
		"corpus_dir":"` + filepath.ToSlash(corpusDir) + `",
		"depot":{"type":"local","location":"` + filepath.ToSlash(outDir) + `"},
		"path_mode":"raw",
		"include_subagents":true,
		"include_images":true,
		"index_tool_output":false,
		"redaction":"none-v1",
		"accept_secrets_warning":true
	}`
	if err := os.WriteFile(configPath, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := cli.Run([]string{"snapshot", "--config", configPath, "--captured-at", "2026-01-03T00:00:00Z", "--bundle-id", "journey"}, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := cli.Run([]string{"ingest", "--config", configPath}, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "sessions=3") {
		t.Fatalf("bad default ingest output: %s", out.String())
	}
	out.Reset()
	if err := cli.Run([]string{"search", "--config", configPath, "needle"}, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "claude-code") {
		t.Fatalf("bad default search output: %s", out.String())
	}
}

func TestCLISnapshotIngestSearchReadStatus(t *testing.T) {
	root := t.TempDir()
	fx := testutil.WriteAgentFixtures(t, root)
	outDir := filepath.Join(root, "out")
	corpusDir := filepath.Join(root, "corpus")
	var out bytes.Buffer
	args := []string{"snapshot", "--machine", "m1", "--source", "pi=" + fx.PiRoot, "--source", "claude-code=" + fx.ClaudeRoot, "--depot", "local:" + outDir, "--accept-secrets", "--captured-at", "2026-01-03T00:00:00Z", "--bundle-id", "fixed"}
	if err := cli.Run(args, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	bundle := snapshotPathFromOutput(t, out.String())
	out.Reset()
	if err := cli.Run([]string{"ingest", "--corpus", corpusDir, bundle}, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "sessions=3") {
		t.Fatalf("bad ingest output: %s", out.String())
	}
	out.Reset()
	if err := cli.Run([]string{"search", "needle", "--corpus", corpusDir, "--json"}, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "[") || !strings.Contains(out.String(), "claude-code") || !strings.Contains(out.String(), "artifact") || !strings.Contains(out.String(), "\"ref\"") {
		t.Fatalf("bad search output: %s", out.String())
	}
	var searchJSON []struct {
		Ref struct {
			Kind string `json:"kind"`
		} `json:"ref"`
		RefText string `json:"ref_text"`
	}
	if err := json.Unmarshal(out.Bytes(), &searchJSON); err != nil {
		t.Fatalf("search JSON did not decode: %v\n%s", err, out.String())
	}
	if len(searchJSON) == 0 || !strings.HasPrefix(searchJSON[0].RefText, "msg:v1:") || searchJSON[0].Ref.Kind == "" {
		t.Fatalf("search JSON missing round-trippable refs: %+v", searchJSON)
	}
	out.Reset()
	if err := cli.Run([]string{"read", searchJSON[0].RefText, "--corpus", corpusDir, "--before", "0", "--after", "0", "--json"}, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "needle") {
		t.Fatalf("read search JSON ref failed: %s", out.String())
	}
	out.Reset()
	if err := cli.Run([]string{"search", "artifact needle", "--corpus", corpusDir, "--json"}, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	var artifactSearchJSON []struct {
		Role    string `json:"role"`
		RefText string `json:"ref_text"`
	}
	if err := json.Unmarshal(out.Bytes(), &artifactSearchJSON); err != nil {
		t.Fatalf("artifact search JSON did not decode: %v\n%s", err, out.String())
	}
	if len(artifactSearchJSON) == 0 || artifactSearchJSON[0].Role != "artifact" || artifactSearchJSON[0].RefText == "" {
		t.Fatalf("artifact search JSON missing ref_text: %+v", artifactSearchJSON)
	}
	out.Reset()
	if err := cli.Run([]string{"read", artifactSearchJSON[0].RefText, "--corpus", corpusDir, "--json"}, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "artifact needle text") {
		t.Fatalf("read artifact search ref failed: %s", out.String())
	}
	out.Reset()
	if err := cli.Run([]string{"search", "needle", "--corpus", corpusDir, "--refs"}, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	refLine := strings.Split(strings.TrimSpace(out.String()), "\n")[0]
	ref := strings.Split(refLine, "\t")[0]
	out.Reset()
	if err := cli.Run([]string{"read", ref, "--corpus", corpusDir, "--before", "0", "--after", "0", "--md"}, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "needle") {
		t.Fatalf("read ref failed: ref=%s output=%s", ref, out.String())
	}
	out.Reset()
	if err := cli.Run([]string{"search", "--corpus", corpusDir, "--", "--json"}, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "[") {
		t.Fatalf("literal flag-looking query should not enable --json flag: %s", out.String())
	}
	out.Reset()
	if err := cli.Run([]string{"read", "--corpus", corpusDir, "--session", "abc", "--before", "0", "--after", "1"}, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "claude needle") {
		t.Fatalf("bad read output: %s", out.String())
	}
	out.Reset()
	if err := cli.Run([]string{"status", "--corpus", corpusDir}, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "index_size_bytes") {
		t.Fatalf("bad status output: %s", out.String())
	}
	store, err := corpus.OpenExisting(corpusDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.Exec(`delete from fts_messages`); err != nil {
		store.Close()
		t.Fatal(err)
	}
	store.Close()
	out.Reset()
	if err := cli.Run([]string{"verify", "--corpus", corpusDir, "--json"}, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "missing_fts_messages") {
		t.Fatalf("verify did not report seeded FTS drift: %s", out.String())
	}
	out.Reset()
	if err := cli.Run([]string{"verify", "--corpus", corpusDir, "--repair-fts", "--json"}, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "missing_fts_messages") || !strings.Contains(out.String(), `"repaired_fts": true`) || !strings.Contains(out.String(), `"inserted_message_rows"`) || !strings.Contains(out.String(), `"stats"`) {
		t.Fatalf("verify --repair-fts did not repair drift or emit counters: %s", out.String())
	}
}

// TestCLIMcpDryRunSmokeChecksRegistration proves --dry-run opens the corpus,
// registers tools, prints a one-line summary, and exits 0 — without serving
// stdio. Hosts use this to confirm their `aha mcp` wiring is correct before
// connecting a real MCP client.
func TestCLIMcpDryRunSmokeChecksRegistration(t *testing.T) {
	root := t.TempDir()
	fx := testutil.WriteAgentFixtures(t, root)
	outDir := filepath.Join(root, "out")
	corpusDir := filepath.Join(root, "corpus")
	var out bytes.Buffer
	if err := cli.Run([]string{"snapshot", "--machine", "m1", "--source", "pi=" + fx.PiRoot, "--depot", "local:" + outDir, "--accept-secrets", "--captured-at", "2026-01-03T00:00:00Z", "--bundle-id", "mcp-dry"}, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	bundle := snapshotPathFromOutput(t, out.String())
	out.Reset()
	if err := cli.Run([]string{"ingest", "--corpus", corpusDir, bundle}, &out, io.Discard); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	if err := cli.Run([]string{"mcp", "--dry-run", "--corpus", corpusDir}, io.Discard, &stderr); err != nil {
		t.Fatalf("mcp --dry-run failed: %v\n%s", err, stderr.String())
	}
	got := stderr.String()
	if !strings.Contains(got, "dry-run ok") {
		t.Fatalf("dry-run summary missing 'dry-run ok': %q", got)
	}
	for _, name := range []string{"search", "read", "status", "verify", "conflicts", "corpus_size", "doctor"} {
		if !strings.Contains(got, name) {
			t.Fatalf("dry-run summary missing tool %q in %q", name, got)
		}
	}

	if err := cli.Run([]string{"mcp", "--dry-run", "--corpus", filepath.Join(root, "missing")}, io.Discard, io.Discard); err == nil {
		t.Fatal("dry-run should fail when corpus is missing")
	}
}

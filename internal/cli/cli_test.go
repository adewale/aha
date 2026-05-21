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
	"github.com/adewale/aha/internal/testutil"
)

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

func TestSnapshotJSONIncludesGeneratedMetadata(t *testing.T) {
	root := t.TempDir()
	fx := testutil.WriteAgentFixtures(t, root)
	outDir := filepath.Join(root, "bundles")
	var out bytes.Buffer
	if err := cli.Run([]string{"snapshot", "--json", "--accept-secrets", "--machine", "m1", "--source", "pi=" + fx.PiRoot, "--out", outDir}, &out, io.Discard); err != nil {
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
	err := cli.Run([]string{"snapshot", "--machine", "m1", "--source", "pi=" + fx.PiRoot, "--out", outDir}, &out, &stderr)
	if err == nil {
		t.Fatalf("snapshot succeeded without privacy acknowledgement")
	}
	if !strings.Contains(stderr.String(), "does not redact secrets") {
		t.Fatalf("missing privacy warning: %s", stderr.String())
	}
	matches, _ := filepath.Glob(filepath.Join(outDir, "*.tar.zst"))
	if len(matches) != 0 {
		t.Fatalf("snapshot wrote bundles despite rejected privacy acknowledgement: %v", matches)
	}
}

func TestCLIRejectsWritesInsideSourceRoots(t *testing.T) {
	root := t.TempDir()
	fx := testutil.WriteAgentFixtures(t, root)
	insideSource := filepath.Join(fx.PiRoot, "aha-output")
	var out, stderr bytes.Buffer
	err := cli.Run([]string{"snapshot", "--accept-secrets", "--machine", "m1", "--source", "pi=" + fx.PiRoot, "--out", insideSource}, &out, &stderr)
	if err == nil || !strings.Contains(err.Error(), "must not be inside source root") {
		t.Fatalf("snapshot did not reject output inside source root: err=%v stderr=%s", err, stderr.String())
	}
	outDir := filepath.Join(root, "bundles")
	err = cli.Run([]string{"refresh", "--accept-secrets", "--machine", "m1", "--source", "pi=" + fx.PiRoot, "--out", outDir, "--repo", insideSource}, &out, &stderr)
	if err == nil || !strings.Contains(err.Error(), "must not be inside source root") {
		t.Fatalf("refresh did not reject repo inside source root before snapshot: err=%v", err)
	}
	matches, _ := filepath.Glob(filepath.Join(outDir, "*.tar.zst"))
	if len(matches) != 0 {
		t.Fatalf("refresh wrote snapshot before repo validation: %v", matches)
	}
	cfgPath := filepath.Join(root, "config.jsonc")
	if err := os.WriteFile(cfgPath, []byte(`{"machine_id":"m1","accept_secrets_warning":true,"bundle_out_dir":"`+filepath.ToSlash(filepath.Join(root, "bundles"))+`","corpus_dir":"`+filepath.ToSlash(filepath.Join(root, "corpus"))+`","sources":[{"type":"pi","root":"`+filepath.ToSlash(fx.PiRoot)+`","enabled":true}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	err = cli.Run([]string{"ingest", "--config", cfgPath, "--repo", insideSource, filepath.Join(root, "bundle.tar.zst")}, &out, &stderr)
	if err == nil || !strings.Contains(err.Error(), "must not be inside source root") {
		t.Fatalf("ingest did not reject repo inside source root: err=%v", err)
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
	if err := os.WriteFile(cfgPath, []byte(`{"machine_id":"m1","accept_secrets_warning":true,"bundle_out_dir":"`+filepath.ToSlash(filepath.Join(root, "bundles"))+`","corpus_dir":"`+filepath.ToSlash(filepath.Join(root, "corpus"))+`","sources":[{"type":"pi","root":"`+filepath.ToSlash(fx.PiRoot)+`","enabled":true}]}`), 0o644); err != nil {
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
		"bundle_out_dir":"` + filepath.ToSlash(outDir) + `",
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
	if err := cli.Run([]string{"snapshot", "--machine", "scoped", "--source", "pi=" + fx.PiRoot, "--source", "claude-code=" + fx.ClaudeRoot, "--out", outDir, "--accept-secrets", "--session", "pi-session", "--captured-at", "2026-01-03T00:00:00Z", "--bundle-id", "pi-only"}, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	bundle := filepath.Join(outDir, "aha-sessions-scoped-2026-01-03T00-00-00Z-pi-only.tar.zst")
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
	if err := cli.Run([]string{"refresh", "--machine", "limited", "--source", "pi=" + fx.PiRoot, "--source", "claude-code=" + fx.ClaudeRoot, "--out", outDir, "--repo", repoDir, "--accept-secrets", "--max-sessions", "1", "--captured-at", "2026-01-03T00:00:00Z", "--bundle-id", "one"}, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "sessions=1") {
		t.Fatalf("expected refresh to ingest one session: %s", out.String())
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
		"bundle_out_dir":"` + filepath.ToSlash(outDir) + `",
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
	args := []string{"snapshot", "--machine", "m1", "--source", "pi=" + fx.PiRoot, "--source", "claude-code=" + fx.ClaudeRoot, "--out", outDir, "--accept-secrets", "--captured-at", "2026-01-03T00:00:00Z", "--bundle-id", "fixed"}
	if err := cli.Run(args, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	bundle := filepath.Join(outDir, "aha-sessions-m1-2026-01-03T00-00-00Z-fixed.tar.zst")
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
	if len(searchJSON) == 0 || !strings.Contains(searchJSON[0].RefText, "#") || searchJSON[0].Ref.Kind == "" {
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
}

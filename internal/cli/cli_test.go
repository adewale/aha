package cli_test

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adewale/aha/internal/cli"
	"github.com/adewale/aha/internal/testutil"
)

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
	if !strings.Contains(out.String(), "[") || !strings.Contains(out.String(), "claude-code") || !strings.Contains(out.String(), "artifact") {
		t.Fatalf("bad search output: %s", out.String())
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

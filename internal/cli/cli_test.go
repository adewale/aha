package cli_test

import (
	"bytes"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adewale/aha/internal/cli"
	"github.com/adewale/aha/internal/testutil"
)

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

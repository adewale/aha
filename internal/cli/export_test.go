package cli_test

import (
	"bytes"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adewale/aha/internal/cli"
	"github.com/adewale/aha/internal/testutil"
)

// TestExportImportRoundTrip pins the single-file hand-off journey: a
// machine's latest depot snapshot can be materialized as one v1
// bundle.tar.zst and re-imported into a fresh corpus, which then answers
// the same searches as a corpus built by pulling the depot directly.
func TestExportImportRoundTrip(t *testing.T) {
	root := t.TempDir()
	fx := testutil.WriteAgentFixtures(t, root)
	depotDir := filepath.Join(root, "depot")
	var out bytes.Buffer
	if err := cli.Run([]string{"snapshot", "--machine", "m1", "--source", "pi=" + fx.PiRoot, "--depot", "local:" + depotDir, "--accept-secrets", "--captured-at", "2026-01-03T00:00:00Z"}, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	exportPath := filepath.Join(root, "handoff.tar.zst")
	if err := cli.Run([]string{"export", "--machine", "m1", "--depot", "local:" + depotDir, "--out", exportPath, "--json"}, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	var exported struct {
		Bundle         string `json:"bundle"`
		SHA256         string `json:"sha256"`
		ManifestSHA256 string `json:"manifest_sha256"`
		Files          int    `json:"files"`
	}
	if err := json.Unmarshal(out.Bytes(), &exported); err != nil {
		t.Fatalf("export JSON: %v\n%s", err, out.String())
	}
	if exported.Bundle != exportPath || exported.SHA256 == "" || exported.ManifestSHA256 == "" || exported.Files == 0 {
		t.Fatalf("export payload: %+v", exported)
	}
	importCorpus := filepath.Join(root, "corpus-import")
	out.Reset()
	if err := cli.Run([]string{"ingest", "--corpus", importCorpus, exportPath}, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "sessions=") {
		t.Fatalf("import produced no sessions: %s", out.String())
	}
	pullCorpus := filepath.Join(root, "corpus-pull")
	if err := cli.Run([]string{"ingest", "--corpus", pullCorpus, "--depot", "local:" + depotDir}, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	search := func(corpusDir string) string {
		var buf bytes.Buffer
		if err := cli.Run([]string{"search", "needle", "--corpus", corpusDir, "--json"}, &buf, io.Discard); err != nil {
			t.Fatal(err)
		}
		return buf.String()
	}
	if search(importCorpus) != search(pullCorpus) {
		t.Fatalf("imported and pulled corpora answer differently:\nimport: %s\npull:   %s", search(importCorpus), search(pullCorpus))
	}
}

// TestExportRequiresExistingSnapshot pins the error path.
func TestExportRequiresExistingSnapshot(t *testing.T) {
	root := t.TempDir()
	depotDir := filepath.Join(root, "depot")
	configPath := filepath.Join(root, "config.jsonc")
	if err := cli.Run([]string{"depot", "init", "--config", configPath, "local:" + depotDir}, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	err := cli.Run([]string{"export", "--machine", "ghost", "--depot", "local:" + depotDir}, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "no snapshot") {
		t.Fatalf("export for unknown machine: err=%v", err)
	}
}

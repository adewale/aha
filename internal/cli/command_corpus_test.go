package cli_test

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adewale/aha/internal/cli"
	"github.com/adewale/aha/internal/corpus"
)

func TestCorpusSizeAndVacuumCommands(t *testing.T) {
	root := t.TempDir()
	corpusDir := filepath.Join(root, "corpus")
	store, err := corpus.Open(corpusDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.Exec(`create table if not exists scratch(x text)`); err != nil {
		store.Close()
		t.Fatal(err)
	}
	store.Close()
	cfgPath := filepath.Join(root, "config.jsonc")
	if err := os.WriteFile(cfgPath, []byte(`{"machine_id":"m","accept_secrets_warning":true,"corpus_dir":"`+filepath.ToSlash(corpusDir)+`","sources":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := cli.Run([]string{"corpus", "size", "--config", cfgPath, "--json"}, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"total_bytes"`) || !strings.Contains(out.String(), `"database_bytes"`) || strings.Contains(out.String(), `"database_bytes": 0`) {
		t.Fatalf("size JSON missing/nonzero database_bytes: %s", out.String())
	}
	out.Reset()
	if err := cli.Run([]string{"corpus", "vacuum", "--config", cfgPath, "--json"}, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"reclaimed_bytes"`) {
		t.Fatalf("vacuum JSON missing reclaimed_bytes: %s", out.String())
	}
	orphanDir := filepath.Join(corpusDir, "blobs", "files")
	if err := os.MkdirAll(orphanDir, 0o755); err != nil {
		t.Fatal(err)
	}
	orphanPath := filepath.Join(orphanDir, "orphan.zst")
	if err := os.WriteFile(orphanPath, []byte("orphan"), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := cli.Run([]string{"corpus", "prune-orphans", "--config", cfgPath, "--json"}, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"dry_run": true`) {
		t.Fatalf("prune-orphans dry-run JSON unexpected: %s", out.String())
	}
	if _, err := os.Stat(orphanPath); err != nil {
		t.Fatalf("dry run removed orphan: %v", err)
	}
	out.Reset()
	if err := cli.Run([]string{"corpus", "prune-orphans", "--config", cfgPath, "--force", "--json"}, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"deleted_files": 1`) {
		t.Fatalf("prune-orphans force JSON unexpected: %s", out.String())
	}
	if _, err := os.Stat(orphanPath); !os.IsNotExist(err) {
		t.Fatalf("force did not remove orphan, stat err=%v", err)
	}
}

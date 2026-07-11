package cli_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/adewale/aha/internal/cli"
	"github.com/adewale/aha/internal/corpus"
	"github.com/adewale/aha/internal/depot"
	_ "modernc.org/sqlite"
)

func TestCorpusRebuildBacksUpLegacyCorpusAndBuildsFromDepot(t *testing.T) {
	root := t.TempDir()
	corpusDir := filepath.Join(root, "corpus")
	if err := os.MkdirAll(corpusDir, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(corpusDir, "corpus.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`create table bundles(bundle_id text primary key)`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	_ = db.Close()
	if err := os.WriteFile(filepath.Join(corpusDir, "legacy-marker"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	depotDir := filepath.Join(root, "depot")
	v2, err := depot.NewLocalV2(depotDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := v2.Init(t.Context()); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(root, "config.jsonc")
	cfg := `{"machine_id":"m","accept_secrets_warning":true,"corpus_dir":` + strconv.Quote(filepath.ToSlash(corpusDir)) + `,"sources":[],"depot":{"type":"local","location":` + strconv.Quote(filepath.ToSlash(depotDir)) + `}}`
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := cli.Run([]string{"corpus", "rebuild", "--backup", "--config", cfgPath, "--json"}, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	var report struct {
		Root   string   `json:"root"`
		Backup string   `json:"backup"`
		Next   []string `json:"next"`
	}
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("decode rebuild JSON: %v\n%s", err, out.String())
	}
	canonicalCorpusDir, err := filepath.EvalSymlinks(corpusDir)
	if err != nil {
		t.Fatal(err)
	}
	if report.Root != canonicalCorpusDir || report.Backup == "" || len(report.Next) != 1 {
		t.Fatalf("rebuild report=%+v canonical=%q", report, canonicalCorpusDir)
	}
	if got, err := os.ReadFile(filepath.Join(report.Backup, "legacy-marker")); err != nil || string(got) != "old" {
		t.Fatalf("legacy backup missing: %q err=%v", got, err)
	}
	store, err := corpus.OpenExisting(corpusDir)
	if err != nil {
		t.Fatalf("fresh corpus not openable: %v", err)
	}
	_ = store.Close()
}

func TestCorpusRebuildRejectsCorpusInsideSourceBeforeMutation(t *testing.T) {
	root := t.TempDir()
	sourceRoot := filepath.Join(root, "source")
	if err := os.MkdirAll(sourceRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(sourceRoot, "must-remain")
	if err := os.WriteFile(marker, []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(root, "config.jsonc")
	cfg := `{"machine_id":"m","accept_secrets_warning":true,"corpus_dir":` + strconv.Quote(filepath.ToSlash(sourceRoot)) + `,"sources":[{"type":"pi","root":` + strconv.Quote(filepath.ToSlash(sourceRoot)) + `,"enabled":true}],"depot":{"type":"local","location":` + strconv.Quote(filepath.ToSlash(filepath.Join(root, "missing-depot"))) + `}}`
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err := cli.Run([]string{"corpus", "rebuild", "--backup", "--config", cfgPath}, &out, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "must not be inside source root") {
		t.Fatalf("corpus rebuild overlap error=%v", err)
	}
	if got, readErr := os.ReadFile(marker); readErr != nil || string(got) != "source" {
		t.Fatalf("overlap rejection mutated source: %q err=%v", got, readErr)
	}
}

func TestCorpusRebuildRejectsSourceInsideCorpusBeforeMutation(t *testing.T) {
	root := t.TempDir()
	corpusDir := filepath.Join(root, "corpus")
	sourceRoot := filepath.Join(corpusDir, "source")
	if err := os.MkdirAll(sourceRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(sourceRoot, "must-remain")
	if err := os.WriteFile(marker, []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(root, "config.jsonc")
	cfg := `{"machine_id":"m","accept_secrets_warning":true,"corpus_dir":` + strconv.Quote(filepath.ToSlash(corpusDir)) + `,"sources":[{"type":"pi","root":` + strconv.Quote(filepath.ToSlash(sourceRoot)) + `,"enabled":true}],"depot":{"type":"local","location":` + strconv.Quote(filepath.ToSlash(filepath.Join(root, "missing-depot"))) + `}}`
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err := cli.Run([]string{"corpus", "rebuild", "--backup", "--config", cfgPath}, &out, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "overlap") {
		t.Fatalf("corpus rebuild reverse-overlap error=%v", err)
	}
	if got, readErr := os.ReadFile(marker); readErr != nil || string(got) != "source" {
		t.Fatalf("reverse-overlap rejection mutated source: %q err=%v", got, readErr)
	}
}

func TestCorpusRebuildRequiresBackupMode(t *testing.T) {
	var out bytes.Buffer
	err := cli.Run([]string{"corpus", "rebuild"}, &out, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "--backup") {
		t.Fatalf("corpus rebuild error=%v", err)
	}
}

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

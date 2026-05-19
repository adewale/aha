package main

import (
	"archive/tar"
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
)

func writeFixture(t *testing.T, root string) (piRoot, claudeRoot string) {
	t.Helper()
	piRoot = filepath.Join(root, "pi")
	claudeRoot = filepath.Join(root, "claude")
	mustMkdir(t, filepath.Join(piRoot, "--Users-me-proj--"))
	mustMkdir(t, filepath.Join(claudeRoot, "-Users-me-proj"))
	img := "iVBORw0KGgo=" // enough bytes to prove extraction/dedupe; not a full PNG fixture
	pi := `{"type":"session","version":3,"id":"pi-session","timestamp":"2026-01-01T00:00:00Z","cwd":"/Users/me/proj"}
{"id":"p1","parentId":"","type":"user","role":"user","timestamp":"2026-01-01T00:00:01Z","message":{"content":"hello needle"}}
{"id":"p2","parentId":"p1","type":"assistant","role":"assistant","timestamp":"2026-01-01T00:00:02Z","message":{"content":[{"type":"text","text":"assistant reply"}]}}
`
	claude := `{"type":"user","timestamp":"2026-01-02T00:00:01Z","message":{"content":[{"type":"text","text":"claude needle"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"` + img + `"}}]}}
{"type":"assistant","timestamp":"2026-01-02T00:00:02Z","message":{"content":[{"type":"text","text":"claude reply"}],"model":"claude-test","usage":{"input_tokens":1,"output_tokens":2}}}
`
	if err := os.WriteFile(filepath.Join(piRoot, "--Users-me-proj--", "2026_pi.jsonl"), []byte(pi), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claudeRoot, "-Users-me-proj", "abc.jsonl"), []byte(claude), 0o644); err != nil {
		t.Fatal(err)
	}
	return piRoot, claudeRoot
}

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotBundleIsDeterministicAndReadOnly(t *testing.T) {
	root := t.TempDir()
	piRoot, claudeRoot := writeFixture(t, root)
	sourceFile := filepath.Join(piRoot, "--Users-me-proj--", "2026_pi.jsonl")
	before, err := os.Stat(sourceFile)
	if err != nil {
		t.Fatal(err)
	}
	cfg := defaultConfig()
	cfg.MachineID = "test-machine"
	cfg.Sources = []SourceConfig{{Type: "pi", Root: piRoot, Enabled: true}, {Type: "claude-code", Root: claudeRoot, Enabled: true}}
	m1, f1, err := capture(t.Context(), cfg, "2026-01-03T00:00:00Z", "fixed-id")
	if err != nil {
		t.Fatal(err)
	}
	m2, f2, err := capture(t.Context(), cfg, "2026-01-03T00:00:00Z", "fixed-id")
	if err != nil {
		t.Fatal(err)
	}
	b1 := filepath.Join(root, "b1.tar.zst")
	b2 := filepath.Join(root, "b2.tar.zst")
	s1, err := writeBundle(b1, m1, f1)
	if err != nil {
		t.Fatal(err)
	}
	s2, err := writeBundle(b2, m2, f2)
	if err != nil {
		t.Fatal(err)
	}
	bb1, _ := os.ReadFile(b1)
	bb2, _ := os.ReadFile(b2)
	if s1 != s2 || !bytes.Equal(bb1, bb2) {
		t.Fatalf("bundle output not deterministic: %s != %s", s1, s2)
	}
	after, err := os.Stat(sourceFile)
	if err != nil {
		t.Fatal(err)
	}
	if before.ModTime() != after.ModTime() || before.Size() != after.Size() {
		t.Fatalf("snapshot modified source file")
	}
	manifestBytes := readBundleFile(t, b1, "manifest.json")
	var manifest Manifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.BundleID != "fixed-id" || manifest.Counts.SessionFiles != 2 {
		t.Fatalf("bad manifest: %+v", manifest)
	}
}

func TestIngestIsIdempotentAndSearchReadWork(t *testing.T) {
	root := t.TempDir()
	piRoot, claudeRoot := writeFixture(t, root)
	cfg := defaultConfig()
	cfg.MachineID = "test-machine"
	cfg.CorpusDir = filepath.Join(root, "corpus")
	cfg.Sources = []SourceConfig{{Type: "pi", Root: piRoot, Enabled: true}, {Type: "claude-code", Root: claudeRoot, Enabled: true}}
	m, f, err := capture(t.Context(), cfg, "2026-01-03T00:00:00Z", "fixed-id")
	if err != nil {
		t.Fatal(err)
	}
	bundle := filepath.Join(root, "bundle.tar.zst")
	if _, err := writeBundle(bundle, m, f); err != nil {
		t.Fatal(err)
	}
	db, corpusRoot, err := openCorpus(cfg.CorpusDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if rep, err := ingestBundle(db, corpusRoot, bundle); err != nil {
		t.Fatal(err)
	} else if rep.Sessions != 2 || rep.Messages != 4 || rep.Images != 1 {
		t.Fatalf("bad first ingest: %+v", rep)
	}
	if rep, err := ingestBundle(db, corpusRoot, bundle); err != nil {
		t.Fatal(err)
	} else if !rep.Duplicate {
		t.Fatalf("second ingest should be duplicate: %+v", rep)
	}
	assertCount(t, db, "bundles", 1)
	assertCount(t, db, "sessions", 2)
	assertCount(t, db, "messages", 4)
	assertCount(t, db, "images", 1)
	assertCount(t, db, "entry_assets", 1)
	var out bytes.Buffer
	if err := run([]string{"search", "--corpus", cfg.CorpusDir, "needle"}, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, "needle") || !strings.Contains(got, "claude-code") {
		t.Fatalf("unexpected search output: %s", got)
	}
	out.Reset()
	if err := run([]string{"read", "--corpus", cfg.CorpusDir, "--session", "abc", "--before", "0", "--after", "1"}, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "claude needle") {
		t.Fatalf("unexpected read output: %s", out.String())
	}
}

func TestReadmeDocumentsCommandsAndPrivacy(t *testing.T) {
	b, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	for _, cmd := range []string{"snapshot", "ingest", "search", "read", "status", "conflicts", "doctor", "init"} {
		if !strings.Contains(text, "aha "+cmd) {
			t.Fatalf("README does not document command %s", cmd)
		}
	}
	if !strings.Contains(text, "does **not** redact secrets") {
		t.Fatalf("README privacy warning missing")
	}
}

func TestJSONCConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.jsonc")
	content := `// comment
{
  "machine_id": "m1", // trailing comment
  "sources": [
    {"type":"pi", "root":"/tmp/pi", "enabled":true},
  ],
  "corpus_dir": "/tmp/corpus",
}
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MachineID != "m1" || len(cfg.Sources) != 1 || cfg.Sources[0].Type != "pi" {
		t.Fatalf("bad jsonc config: %+v", cfg)
	}
}

func assertCount(t *testing.T, db *sql.DB, table string, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow("select count(*) from " + table).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("%s count=%d want %d", table, got, want)
	}
}

func readBundleFile(t *testing.T, path, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zstd.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if h.Name == name {
			data, err := io.ReadAll(tr)
			if err != nil {
				t.Fatal(err)
			}
			return data
		}
	}
	t.Fatalf("%s not found", name)
	return nil
}

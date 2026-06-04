package adapters

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestKeyPathCoverage walks every JSON key-path observed in committed
// adapter fixtures and asserts each one has an entry in
// testdata/projection-table.json describing what the corpus does with
// it (projected, preserved, or intentionally raw-only). A new fixture
// or a source-format addition fails this test until the field is
// classified, which surfaces silent data-shape changes that would
// otherwise pass through unnoticed via entries.raw_json.
func TestKeyPathCoverage(t *testing.T) {
	table := loadProjectionTable(t, "testdata/projection-table.json")
	fixtures := append(discoverFixturePaths(t, "testdata", fixtureBasenames()), corpusJSONLPaths(t)...)
	observed := map[string]string{}
	for _, fx := range fixtures {
		for _, path := range observedKeyPaths(t, fx) {
			if _, seen := observed[path]; !seen {
				observed[path] = fx
			}
		}
	}
	var missing []string
	for path, fx := range observed {
		if _, ok := table[path]; !ok {
			missing = append(missing, path+"   (first seen in "+fx+")")
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("%d key-path(s) observed in fixtures but missing from testdata/projection-table.json — classify each as projected/preserved/raw_only and add it:\n  %s", len(missing), strings.Join(missing, "\n  "))
	}
}

// TestProjectionTableHasNoStaleEntries fails if the table lists paths
// that no fixture actually carries. Keeps the table honest as fixtures
// rotate; stale entries become invisible documentation otherwise.
func TestProjectionTableHasNoStaleEntries(t *testing.T) {
	table := loadProjectionTable(t, "testdata/projection-table.json")
	fixtures := append(discoverFixturePaths(t, "testdata", fixtureBasenames()), corpusJSONLPaths(t)...)
	observed := map[string]struct{}{}
	for _, fx := range fixtures {
		for _, path := range observedKeyPaths(t, fx) {
			observed[path] = struct{}{}
		}
	}
	var stale []string
	for path := range table {
		if _, ok := observed[path]; !ok {
			stale = append(stale, path)
		}
	}
	sort.Strings(stale)
	if len(stale) > 0 {
		t.Fatalf("%d projection-table path(s) not observed in any fixture — either add a fixture that exercises the path, or remove it from the table:\n  %s", len(stale), strings.Join(stale, "\n  "))
	}
}

func TestProjectionTableClassifiesImplementedColumns(t *testing.T) {
	table := loadProjectionTable(t, "testdata/projection-table.json")
	wantProjected := []string{
		"firstKeptEntryId",
		"tokensBefore",
		"modelId",
		"provider",
		"thinkingLevel",
		"message.excludeFromContext",
		"message.content[].mimeType",
		"message.content[].data",
	}
	for _, path := range wantProjected {
		classification, ok := table[path]
		if !ok {
			t.Fatalf("projection table missing %s", path)
		}
		if strings.HasPrefix(classification, "raw_only:") {
			t.Fatalf("projection table says %s is raw_only after implementation: %s", path, classification)
		}
	}
}

func fixtureBasenames() []string {
	return []string{
		"pi_realish.jsonl",
		"claude_realish.jsonl",
		"codex_realish.jsonl",
		"coverage_pi.jsonl",
		"coverage_claude.jsonl",
		"coverage_codex.jsonl",
	}
}

// corpusJSONLPaths returns every .jsonl file under testdata/corpora/.
// These come from vendored real-world sessions (pi-mono today, others
// later) and exercise the projection surface far beyond what hand-
// crafted fixtures cover.
func corpusJSONLPaths(t *testing.T) []string {
	t.Helper()
	var out []string
	root := filepath.Join("testdata", "corpora")
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return filepath.SkipDir
			}
			return err
		}
		if info.IsDir() || !strings.HasSuffix(info.Name(), ".jsonl") {
			return nil
		}
		out = append(out, path)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	return out
}

func loadProjectionTable(t *testing.T, path string) map[string]string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Version int               `json:"version"`
		Paths   map[string]string `json:"paths"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	if doc.Version == 0 {
		t.Fatalf("%s missing version", path)
	}
	if len(doc.Paths) == 0 {
		t.Fatalf("%s has no paths", path)
	}
	return doc.Paths
}

func discoverFixturePaths(t *testing.T, dir string, basenames []string) []string {
	t.Helper()
	var out []string
	for _, name := range basenames {
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("missing fixture %s: %v", p, err)
		}
		out = append(out, p)
	}
	return out
}

// observedKeyPaths returns every distinct dotted key-path observed in
// the JSONL file at path. Array steps are marked '[]'.
func observedKeyPaths(t *testing.T, path string) []string {
	t.Helper()
	lines := readNonEmptyLines(t, path)
	seen := map[string]struct{}{}
	for i, l := range lines {
		var v any
		if err := json.Unmarshal([]byte(l), &v); err != nil {
			t.Fatalf("%s:%d: %v", path, i+1, err)
		}
		walkKeyPaths(v, "", seen)
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func walkKeyPaths(v any, prefix string, out map[string]struct{}) {
	switch x := v.(type) {
	case map[string]any:
		if len(x) == 0 {
			if prefix != "" {
				out[prefix] = struct{}{}
			}
			return
		}
		for k, child := range x {
			next := k
			if prefix != "" {
				next = prefix + "." + k
			}
			walkKeyPaths(child, next, out)
		}
	case []any:
		if len(x) == 0 {
			if prefix != "" {
				out[prefix+"[]"] = struct{}{}
			}
			return
		}
		for _, child := range x {
			walkKeyPaths(child, prefix+"[]", out)
		}
	default:
		if prefix != "" {
			out[prefix] = struct{}{}
		}
	}
}

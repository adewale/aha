// Drift test for docs/interactive/architecture.html.
//
// The interactive explorer hard-codes facts about the codebase: the count
// of production-code Go lines, the set of packages under internal/, the
// set of corpus schema tables, and the package import graph. None of
// these are derived; all of them will silently rot when the code
// changes. This test re-derives each fact from the source files and
// asserts the HTML still names them.
//
// When this test fails, EITHER fix the HTML by editing the
// corresponding data-drift anchor / table card / PKG_GRAPH entry,
// OR if the code change is what's intended, update the expected counts
// here and in the HTML in the same commit.
package cli_test

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

const archHTMLPath = "../../docs/interactive/architecture.html"

func readArchHTML(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(archHTMLPath)
	if err != nil {
		t.Fatalf("read %s: %v", archHTMLPath, err)
	}
	return string(b)
}

// TestArchitectureHTMLProductionLineCount derives the production Go line
// count from the source tree and asserts the HTML sidebar still claims it.
// Production = all *.go files that don't end in _test.go, excluding
// vendored / build-artifact directories.
func TestArchitectureHTMLProductionLineCount(t *testing.T) {
	got, err := countProductionGoLines("../..")
	if err != nil {
		t.Fatal(err)
	}
	html := readArchHTML(t)
	claim := extractDriftAnchor(t, html, "prod-lines")
	if claim != formatThousands(got) {
		t.Fatalf("architecture.html claims %q production Go lines; source has %s.\n"+
			"Fix: update the data-drift=\"prod-lines\" span in the sidebar to %s.",
			claim, formatThousands(got), formatThousands(got))
	}
}

// TestArchitectureHTMLInternalPackageCount asserts the HTML's package
// count matches the number of production-shipping packages under
// internal/. testutil is excluded (it's only imported from _test.go).
func TestArchitectureHTMLInternalPackageCount(t *testing.T) {
	pkgs, err := listProductionInternalPackages("../..")
	if err != nil {
		t.Fatal(err)
	}
	html := readArchHTML(t)
	claim := extractDriftAnchor(t, html, "prod-pkgs")
	want := fmt.Sprintf("%d", len(pkgs))
	if claim != want {
		t.Fatalf("architecture.html claims %s packages in internal/; source has %d (%s).\n"+
			"Fix: update the data-drift=\"prod-pkgs\" span in the sidebar to %s.",
			claim, len(pkgs), strings.Join(pkgs, ", "), want)
	}
}

// TestArchitectureHTMLPackageGraphCoversAllPackages asserts every
// production package under internal/ is named in the architecture
// explorer's PKG_GRAPH (the layered dependency diagram). Missing one
// would leave a hole in the diagram.
func TestArchitectureHTMLPackageGraphCoversAllPackages(t *testing.T) {
	pkgs, err := listProductionInternalPackages("../..")
	if err != nil {
		t.Fatal(err)
	}
	html := readArchHTML(t)
	graphKeys := extractPkgGraphKeys(html)
	missing := setDiff(pkgs, graphKeys)
	if len(missing) > 0 {
		t.Fatalf("architecture.html PKG_GRAPH is missing entries for: %s.\n"+
			"Fix: add a PKG_GRAPH[\"%s\"] = { deps: [...], role: \"...\" } entry and place it in a LAYERS layer.",
			strings.Join(missing, ", "), missing[0])
	}
	extra := setDiff(graphKeys, pkgs)
	if len(extra) > 0 {
		t.Fatalf("architecture.html PKG_GRAPH names packages that no longer exist in internal/: %s.\n"+
			"Fix: remove the stale entries from PKG_GRAPH and the corresponding LAYERS row.",
			strings.Join(extra, ", "))
	}
}

// TestArchitectureHTMLSchemaTablesMatch asserts the schema ER diagram
// names every corpus table from corpus DDL sources and doesn't name any that
// don't exist. The two virtual FTS tables (fts_messages, fts_artifacts)
// and the meta schema_migrations row are intentionally excluded from
// the visual schema — they're called out elsewhere or aren't first
// class — so the test treats them as optional.
func TestArchitectureHTMLSchemaTablesMatch(t *testing.T) {
	tables, err := listSchemaTables("../corpus")
	if err != nil {
		t.Fatal(err)
	}
	const meta = "schema_migrations"
	filtered := make([]string, 0, len(tables))
	for _, t := range tables {
		if t == meta {
			continue
		}
		filtered = append(filtered, t)
	}
	html := readArchHTML(t)
	named := extractERTableNames(html)
	missing := setDiff(filtered, named)
	if len(missing) > 0 {
		t.Fatalf("architecture.html schema ER is missing cards for tables: %s.\n"+
			"Fix: add a <div class=\"er-table\"> card for %q under the correct family in #schema-er.",
			strings.Join(missing, ", "), missing[0])
	}
	extra := setDiff(named, filtered)
	if len(extra) > 0 {
		t.Fatalf("architecture.html schema ER names tables that no longer exist in schema.go: %s.\n"+
			"Fix: remove the stale card or update the table name.",
			strings.Join(extra, ", "))
	}
}

// ---- helpers ---------------------------------------------------------

// countProductionGoLines walks the source tree rooted at root and
// returns the total line count across all *.go files that don't end in
// _test.go. .git, node_modules, and dot-prefixed top-level dirs are
// skipped.
func countProductionGoLines(root string) (int, error) {
	total := 0
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			base := filepath.Base(path)
			if base == ".git" || base == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		name := info.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		n, err := countLines(path)
		if err != nil {
			return err
		}
		total += n
		return nil
	})
	if err != nil {
		return 0, err
	}
	return total, nil
}

func countLines(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 64*1024), 1024*1024)
	n := 0
	for s.Scan() {
		n++
	}
	return n, s.Err()
}

// listProductionInternalPackages returns sorted package directory names
// under internal/, excluding testutil (which is test-only — no
// production .go file in any package imports it).
func listProductionInternalPackages(root string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(root, "internal"))
	if err != nil {
		return nil, err
	}
	var pkgs []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if e.Name() == "testutil" {
			continue // imported only from _test.go files
		}
		pkgs = append(pkgs, e.Name())
	}
	sort.Strings(pkgs)
	return pkgs, nil
}

// listSchemaTables greps internal/corpus production Go files for
// table-creation statements (both ordinary and FTS5 virtual) and returns their
// names in sorted order, deduplicated. DDL constants may live next to their
// migrations, not only in schema.go.
var schemaTableRE = regexp.MustCompile(`create (?:virtual )?table if not exists ([a-z_]+)`)

func listSchemaTables(schemaDir string) ([]string, error) {
	entries, err := os.ReadDir(schemaDir)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(schemaDir, name))
		if err != nil {
			return nil, err
		}
		for _, m := range schemaTableRE.FindAllStringSubmatch(string(b), -1) {
			seen[m[1]] = true
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, nil
}

// extractDriftAnchor returns the inner text of <span data-drift="key">…</span>
// from the HTML. The data-drift attribute marks values the drift test
// re-derives from source.
func extractDriftAnchor(t *testing.T, html, key string) string {
	t.Helper()
	re := regexp.MustCompile(`data-drift="` + regexp.QuoteMeta(key) + `">([^<]*)</span>`)
	m := re.FindStringSubmatch(html)
	if m == nil {
		t.Fatalf("architecture.html has no <span data-drift=%q>…</span>; the anchor was removed", key)
	}
	return strings.TrimSpace(m[1])
}

// extractPkgGraphKeys finds the package identifiers used as keys in the
// HTML's PKG_GRAPH JS object literal. The literal looks like:
//
//	const PKG_GRAPH = {
//	  'model':     { deps: [...], role: '...' },
//	  ...
//	};
var pkgGraphKeyRE = regexp.MustCompile(`(?m)^\s+'([a-z]+)':\s*\{\s*deps:`)

func extractPkgGraphKeys(html string) []string {
	matches := pkgGraphKeyRE.FindAllStringSubmatch(html, -1)
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		out = append(out, m[1])
	}
	sort.Strings(out)
	return out
}

// extractERTableNames pulls every <div class="er-name">NAME</div> from
// the HTML. These are the schema ER cards.
var erNameRE = regexp.MustCompile(`<div class="er-name">([a-z_]+)</div>`)

func extractERTableNames(html string) []string {
	matches := erNameRE.FindAllStringSubmatch(html, -1)
	seen := map[string]bool{}
	for _, m := range matches {
		seen[m[1]] = true
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// setDiff returns the elements of a not in b.
func setDiff(a, b []string) []string {
	bset := map[string]bool{}
	for _, x := range b {
		bset[x] = true
	}
	var out []string
	for _, x := range a {
		if !bset[x] {
			out = append(out, x)
		}
	}
	return out
}

// formatThousands renders an integer with comma separators, matching
// the way the HTML sidebar formats numbers (e.g. "8,399").
func formatThousands(n int) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	pre := len(s) % 3
	if pre > 0 {
		b.WriteString(s[:pre])
		if len(s) > pre {
			b.WriteByte(',')
		}
	}
	for i := pre; i < len(s); i += 3 {
		b.WriteString(s[i : i+3])
		if i+3 < len(s) {
			b.WriteByte(',')
		}
	}
	return b.String()
}

package adapters_test

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/adewale/aha/internal/adapters/fixtureaudit"
)

const projectionTablePath = "testdata/projection-table.json"

const fixtureRoot = "testdata"

// TestKeyPathCoverage walks every JSON key-path observed in committed adapter
// fixtures and asserts each one has an entry in
// testdata/projection-table.json describing what the corpus does with it
// (projected, preserved, or intentionally raw-only). A new fixture or a
// source-format addition fails this test until the field is classified, which
// surfaces silent data-shape changes that would otherwise pass through
// unnoticed via entries.raw_json.
//
// Classification is keyed by (source, path), not by path alone: two producers
// can spell a field the same way and mean different things, and one shared
// entry would silently attribute one producer's decision to the other.
func TestKeyPathCoverage(t *testing.T) {
	table := loadProjectionTable(t)
	observed := observedSourcePaths(t)
	var missing []string
	for pair, label := range observed {
		if _, ok := table.Classification(pair.Source, pair.Path); !ok {
			missing = append(missing, pair.Source+"  "+pair.Path+"   (first seen in "+label+")")
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("%d (source, key-path) pair(s) observed in fixtures but missing from %s — classify each as projected/preserved/raw_only under its source and add it:\n  %s", len(missing), projectionTablePath, strings.Join(missing, "\n  "))
	}
}

// TestProjectionTableSeparatesVersionAcrossSources is the regression test for
// the collision the (source, path) key removes. Pi's `version` is an on-disk
// schema revision (integer 3); Claude Code's `version` is a producer version
// (string 2.1.92). Both fixtures carry a field spelled `version`, so a table
// keyed by path alone gives one of them the other's classification. They are
// different fields with different meanings and each needs its own reviewed
// entry.
func TestProjectionTableSeparatesVersionAcrossSources(t *testing.T) {
	observed := observedSourcePaths(t)
	for _, source := range []string{"pi", "claude-code"} {
		if _, ok := observed[fixtureaudit.SourcePath{Source: source, Path: "version"}]; !ok {
			t.Fatalf("no %s fixture carries `version`, so this regression test proves nothing", source)
		}
	}
	table := loadProjectionTable(t)
	pi, okPi := table.Classification("pi", "version")
	claude, okClaude := table.Classification("claude-code", "version")
	if !okPi || !okClaude {
		t.Fatalf("%s must classify `version` under both pi (%t) and claude-code (%t)", projectionTablePath, okPi, okClaude)
	}
	if pi == claude {
		t.Fatalf("%s gives pi and claude-code the same `version` classification %q; Pi's on-disk schema revision and Claude Code's producer version are different fields and must be classified separately", projectionTablePath, pi)
	}
	if !strings.Contains(pi, "schema") {
		t.Fatalf("pi `version` is an on-disk schema revision but is classified %q", pi)
	}
	if !strings.Contains(claude, "producer") {
		t.Fatalf("claude-code `version` is a producer version but is classified %q", claude)
	}
}

// TestProjectionTableLookupDoesNotLeakAcrossSources is the construction-level
// half of the same invariant: classifying a path under one source must leave
// it unclassified under every other, so a fixture that introduces an existing
// path name under a different source fails the coverage gate until the new
// field is separately reviewed.
func TestProjectionTableLookupDoesNotLeakAcrossSources(t *testing.T) {
	path := filepath.Join(t.TempDir(), "one-source.json")
	if err := os.WriteFile(path, []byte(`{"version":2,"doc":"synthetic","sources":{"pi":{"version":"raw_only:Pi on-disk schema revision"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	table, err := fixtureaudit.LoadProjectionTable(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := table.Classification("pi", "version"); !ok {
		t.Fatal("pi `version` should be classified")
	}
	for _, source := range []string{"claude-code", "codex", "opencode"} {
		if classification, ok := table.Classification(source, "version"); ok {
			t.Fatalf("%s `version` resolved to %q from Pi's entry; classification must not leak across sources", source, classification)
		}
	}
}

// TestProjectionTableHasNoStaleEntries fails if the table classifies
// (source, path) pairs that no fixture actually carries. Keeps the table
// honest as fixtures rotate; stale entries become invisible documentation
// otherwise.
func TestProjectionTableHasNoStaleEntries(t *testing.T) {
	table := loadProjectionTable(t)
	observed := observedSourcePaths(t)
	var stale []string
	for _, pair := range table.Pairs() {
		if _, ok := observed[pair]; !ok {
			stale = append(stale, pair.Source+"  "+pair.Path)
		}
	}
	if len(stale) > 0 {
		t.Fatalf("%d projection-table (source, key-path) pair(s) not observed in any fixture — either add a fixture that exercises the path, or remove it from the table:\n  %s", len(stale), strings.Join(stale, "\n  "))
	}
}

func TestProjectionTableClassifiesImplementedColumns(t *testing.T) {
	table := loadProjectionTable(t)
	// Every one of these is a Pi-native shape that projectPiNativeShapes now
	// fills into a typed column, so none may still be recorded as raw_only.
	wantProjected := []fixtureaudit.SourcePath{
		{Source: "pi", Path: "firstKeptEntryId"},
		{Source: "pi", Path: "tokensBefore"},
		{Source: "pi", Path: "modelId"},
		{Source: "pi", Path: "provider"},
		{Source: "pi", Path: "thinkingLevel"},
		{Source: "pi", Path: "message.excludeFromContext"},
		{Source: "pi", Path: "message.content[].mimeType"},
		{Source: "pi", Path: "message.content[].data"},
	}
	for _, pair := range wantProjected {
		classification, ok := table.Classification(pair.Source, pair.Path)
		if !ok {
			t.Fatalf("projection table missing %s %s", pair.Source, pair.Path)
		}
		if strings.HasPrefix(classification, "raw_only:") {
			t.Fatalf("projection table says %s %s is raw_only after implementation: %s", pair.Source, pair.Path, classification)
		}
	}
}

// TestFixtureInventoryAttributesEveryCommittedFixture pins the precondition
// the whole coverage gate rests on: every committed fixture is attributed to
// exactly one adapter, and every adapter that owns fixtures is represented.
func TestFixtureInventoryAttributesEveryCommittedFixture(t *testing.T) {
	fixtures, err := fixtureaudit.Inventory(fixtureRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(fixtures) == 0 {
		t.Fatal("fixture inventory is empty")
	}
	bySource := map[string]int{}
	for _, fx := range fixtures {
		if fx.Source == "" {
			t.Fatalf("fixture %s has no source", fx.Label)
		}
		bySource[fx.Source]++
	}
	for _, source := range fixtureaudit.Sources() {
		if bySource[source] == 0 {
			t.Fatalf("source %s is declared but owns no fixture", source)
		}
	}
}

func loadProjectionTable(t *testing.T) fixtureaudit.ProjectionTable {
	t.Helper()
	table, err := fixtureaudit.LoadProjectionTable(projectionTablePath)
	if err != nil {
		t.Fatal(err)
	}
	return table
}

// observedSourcePaths returns every (source, key-path) the committed fixtures
// carry, mapped to the label of the first fixture that carried it.
func observedSourcePaths(t *testing.T) map[fixtureaudit.SourcePath]string {
	t.Helper()
	fixtures, err := fixtureaudit.Inventory(fixtureRoot)
	if err != nil {
		t.Fatal(err)
	}
	observed := map[fixtureaudit.SourcePath]string{}
	for _, fx := range fixtures {
		records, err := fixtureaudit.ReadRecords(fx.Path)
		if err != nil {
			t.Fatal(err)
		}
		for _, rec := range records {
			for _, path := range fixtureaudit.KeyPaths(rec.Value) {
				pair := fixtureaudit.SourcePath{Source: fx.Source, Path: path}
				if _, seen := observed[pair]; !seen {
					observed[pair] = fx.Label
				}
			}
		}
	}
	return observed
}

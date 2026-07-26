package testquality_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The source-version audit is the only record of how much of each producer's
// release history the fixture corpus covers. Its gates live in
// internal/adapters and compare the committed artefact against a regeneration,
// so deleting the artefact is the one edit that turns a failing gate into a
// passing one. These static checks make that route fail too.

func auditArtefact(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join("..", "..", name)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("%s must exist: %v", name, err)
	}
	if info.Size() == 0 {
		t.Fatalf("%s is empty", name)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSourceVersionAuditArtefactsExist(t *testing.T) {
	jsonBody := auditArtefact(t, filepath.Join("internal", "adapters", "testdata", "source-version-audit.json"))
	for _, want := range []string{`"sources"`, `"version_kind"`, `"band"`, `"key_paths"`} {
		if !strings.Contains(jsonBody, want) {
			t.Fatalf("source-version-audit.json is missing %s; it is no longer the generated audit", want)
		}
	}
	markdown := auditArtefact(t, filepath.Join("docs", "source-version-audit.md"))
	for _, want := range []string{"# Source version audit", "Do not edit; regenerate", "## Evidence base", "## Observed versions", "## Key paths by version band"} {
		if !strings.Contains(markdown, want) {
			t.Fatalf("docs/source-version-audit.md is missing %q; it is no longer the generated audit", want)
		}
	}
}

// TestProjectionTableIsKeyedBySource keeps the committed table on the
// (source, path) layout. A revert to the flat version-1 layout would give
// same-named fields from different producers one shared classification again.
func TestProjectionTableIsKeyedBySource(t *testing.T) {
	body := auditArtefact(t, filepath.Join("internal", "adapters", "testdata", "projection-table.json"))
	if !strings.Contains(body, `"version": 2`) {
		t.Fatal("projection-table.json must be version 2, keyed by (source, path)")
	}
	if !strings.Contains(body, `"sources"`) {
		t.Fatal("projection-table.json must group classifications under a per-source key")
	}
	if strings.Contains(body, `"paths": {`) {
		t.Fatal("projection-table.json still carries the flat version-1 \"paths\" map, which shares one classification across every source")
	}
}

// TestVersionAuditIsRegenerableFromTheMakefile keeps the generator reachable
// by the documented command. A gate telling the reader to run a target that
// does not exist is a gate that gets edited away instead of satisfied.
func TestVersionAuditIsRegenerableFromTheMakefile(t *testing.T) {
	makefile := repoFile(t, "Makefile")
	for _, required := range []string{
		"gen-version-audit:",
		"./cmd/aha-gen-version-audit",
		"-json internal/adapters/testdata/source-version-audit.json",
		"-markdown docs/source-version-audit.md",
	} {
		if !strings.Contains(makefile, required) {
			t.Fatalf("Makefile must contain %q so `make gen-version-audit` regenerates the committed audit", required)
		}
	}
}

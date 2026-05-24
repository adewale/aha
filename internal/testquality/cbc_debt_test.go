package testquality_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

func TestAmbientTimeDebtInventoryDoesNotGrow(t *testing.T) {
	want := map[string]int{
		"internal/archive/archive.go:Capture:Now":                       1,
		"internal/cli/cli.go:writeSnapshot:Now":                         1,
		"internal/cli/command_snapshot.go:finalizeSnapshotMetadata:Now": 1,
		"internal/clock/clock.go:Now:Now":                               1,
		"internal/clock/clock.go:Sleep:Sleep":                           1,
		"internal/corpus/ingest.go:IngestBundle:Sleep":                  1,
		"internal/corpus/ingest.go:insertBundleMetadata:Now":            1,
		"internal/corpus/ingest.go:recordBundleAttempt:Now":             3,
		"internal/depot/depot.go:newMarker:Now":                         2,
	}
	got := ambientTimeCalls(t)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ambient time debt inventory changed\ngot:  %#v\nwant: %#v\nIf this is new debt, do not add it. If a refactor removed debt, shrink the allowlist.", got, want)
	}
}

func TestManualFTSDebtInventoryDoesNotGrow(t *testing.T) {
	want := map[string]int{
		"internal/corpus/ingest.go:insert:fts_artifacts": 1,
		"internal/corpus/ingest.go:insert:fts_messages":  1,
	}
	got := manualFTSWrites(t)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("manual FTS write debt inventory changed\ngot:  %#v\nwant: %#v\nMove FTS writes behind schema triggers/reconcilers instead of adding direct writes.", got, want)
	}
}

func TestNoDirectAppendOnlyTableMutationDebt(t *testing.T) {
	mutations := directAppendOnlyMutations(t)
	if len(mutations) > 0 {
		t.Fatalf("append-only table mutations must go through explicit migration/repair mechanisms, offenders: %#v", mutations)
	}
}

func TestRawIdentityConcatDebtInventoryDoesNotGrow(t *testing.T) {
	known := map[string][]string{
		"internal/corpus/ingest.go": {
			`sessionKey := mf.Source + ":" + manifest.MachineID + ":" + sessionID`,
			`parent = mf.Source + ":" + manifest.MachineID + ":" + mf.ParentHint`,
		},
	}
	got := rawIdentityDebt(t, known)
	want := map[string]int{
		"internal/corpus/ingest.go": 2,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("raw identity construction debt inventory changed\ngot:  %#v\nwant: %#v\nUse a typed/canonical identity constructor instead of adding string-built keys.", got, want)
	}
}

func ambientTimeCalls(t *testing.T) map[string]int {
	t.Helper()
	out := map[string]int{}
	walkProductionGo(t, func(rel, path string, b []byte) {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, b, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", rel, err)
		}
		timeNames := map[string]bool{}
		for _, imp := range file.Imports {
			if strings.Trim(imp.Path.Value, `"`) != "time" {
				continue
			}
			name := "time"
			if imp.Name != nil {
				name = imp.Name.Name
			}
			if name != "_" && name != "." {
				timeNames[name] = true
			}
		}
		if len(timeNames) == 0 {
			return
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				ident, ok := sel.X.(*ast.Ident)
				if !ok || !timeNames[ident.Name] {
					return true
				}
				switch sel.Sel.Name {
				case "Now", "Sleep", "Since", "After":
					out[rel+":"+fn.Name.Name+":"+sel.Sel.Name]++
				}
				return true
			})
		}
	})
	return out
}

func manualFTSWrites(t *testing.T) map[string]int {
	t.Helper()
	out := map[string]int{}
	re := regexp.MustCompile(`(?is)\b(insert|update|delete)\s+(?:or\s+\w+\s+)?(?:(?:into|from)\s+)?(fts_(?:messages|artifacts))\b`)
	walkProductionGo(t, func(rel, _ string, b []byte) {
		for _, m := range re.FindAllSubmatch(b, -1) {
			out[rel+":"+strings.ToLower(string(m[1]))+":"+strings.ToLower(string(m[2]))]++
		}
	})
	return out
}

func directAppendOnlyMutations(t *testing.T) map[string]int {
	t.Helper()
	out := map[string]int{}
	re := regexp.MustCompile(`(?is)\b(delete\s+from|update)\s+(entries|messages|artifacts|conflicts)\b`)
	walkProductionGo(t, func(rel, _ string, b []byte) {
		for _, m := range re.FindAllSubmatch(b, -1) {
			out[rel+":"+strings.ToLower(string(m[1]))+":"+strings.ToLower(string(m[2]))]++
		}
	})
	return out
}

func rawIdentityDebt(t *testing.T, known map[string][]string) map[string]int {
	t.Helper()
	out := map[string]int{}
	joinRe := regexp.MustCompile(`strings\.Join\s*\([^\n]+,\s*":"\s*\)`)
	walkProductionGo(t, func(rel, _ string, b []byte) {
		text := string(b)
		for _, snippet := range known[rel] {
			count := strings.Count(text, snippet)
			if count != 1 {
				t.Fatalf("known identity debt snippet count for %s = %d, want 1: %s", rel, count, snippet)
			}
			out[rel]++
			text = strings.Replace(text, snippet, "", 1)
		}
		if strings.Contains(text, ` + ":" + `) || joinRe.MatchString(text) {
			out[rel] += 1000
		}
	})
	return out
}

func walkProductionGo(t *testing.T, fn func(rel, path string, b []byte)) {
	t.Helper()
	root := filepath.Join("..", "..")
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "testdata", "testutil":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		fn(rel, path, b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

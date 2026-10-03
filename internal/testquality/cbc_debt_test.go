package testquality_test

import (
	"fmt"
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
		"internal/clock/clock.go:Now:Now":     1,
		"internal/clock/clock.go:Sleep:Sleep": 1,
		// Context-cancellable waits. clock.Sleeper has no context, so these
		// timers are recorded debt rather than routed through the clock.
		"internal/corpus/lifecycle_lock_unix.go:waitForLifecycleLock:NewTimer": 1,
		"internal/depot/depot_v2.go:waitForConditionalRetry:NewTimer":          1,
	}
	got := ambientTimeCalls(t)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ambient time debt inventory changed\ngot:  %#v\nwant: %#v\nIf this is new debt, do not add it. If a refactor removed debt, shrink the allowlist.", got, want)
	}
}

// TestAmbientTimeScannerReportsEveryAmbientTimeShape gives the inventory above
// teeth: each known-bad source must be reported, and each known-good source
// must not be.
func TestAmbientTimeScannerReportsEveryAmbientTimeShape(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want map[string]int
	}{
		{"call inside a function", "package p\nimport \"time\"\nfunc f() { _ = time.Now() }\n", map[string]int{"p.go:f:Now": 1}},
		{"package-level function value", "package p\nimport \"time\"\nvar now = time.Now\n", map[string]int{"p.go:<package>:Now": 1}},
		{"package-level call", "package p\nimport \"time\"\nvar started = time.Now()\n", map[string]int{"p.go:<package>:Now": 1}},
		{"renamed import", "package p\nimport clk \"time\"\nfunc f() { clk.Sleep(1) }\n", map[string]int{"p.go:f:Sleep": 1}},
		{"timer", "package p\nimport \"time\"\nfunc f() { _ = time.NewTimer(1) }\n", map[string]int{"p.go:f:NewTimer": 1}},
		{"ticker", "package p\nimport \"time\"\nfunc f() { _ = time.NewTicker(1) }\n", map[string]int{"p.go:f:NewTicker": 1}},
		{"tick", "package p\nimport \"time\"\nfunc f() { _ = time.Tick(1) }\n", map[string]int{"p.go:f:Tick": 1}},
		{"after func", "package p\nimport \"time\"\nfunc f() { time.AfterFunc(1, func() {}) }\n", map[string]int{"p.go:f:AfterFunc": 1}},
		{"until", "package p\nimport \"time\"\nfunc f(d time.Time) { _ = time.Until(d) }\n", map[string]int{"p.go:f:Until": 1}},
		{"function literal in a package-level var", "package p\nimport \"time\"\nvar f = func() { _ = time.Since(time.Time{}) }\n", map[string]int{"p.go:<package>:Since": 1}},
		{"dot import", "package p\nimport . \"time\"\nfunc f() { _ = Now() }\n", map[string]int{"p.go:<import>:dot": 1}},
		{"injected clock and durations are fine", "package p\nimport \"time\"\ntype clock interface{ Now() time.Time }\nfunc f(c clock) time.Duration { _ = c.Now(); return 2 * time.Second }\n", map[string]int{}},
		{"no time import", "package p\nfunc Now() int { return 1 }\nfunc f() { _ = Now() }\n", map[string]int{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := map[string]int{}
			if err := addAmbientTimeUses(got, "p.go", []byte(tc.src)); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestManualFTSDebtInventoryDoesNotGrow(t *testing.T) {
	want := map[string]int{
		"internal/corpus/fts_reconcile.go:delete:fts_artifacts": 1,
		"internal/corpus/fts_reconcile.go:delete:fts_messages":  1,
		"internal/corpus/fts_reconcile.go:insert:fts_artifacts": 1,
		"internal/corpus/fts_reconcile.go:insert:fts_messages":  1,
		"internal/corpus/schema.go:delete:fts_artifacts":        1,
		"internal/corpus/schema.go:delete:fts_messages":         1,
		"internal/corpus/schema.go:insert:fts_artifacts":        3,
		"internal/corpus/schema.go:insert:fts_messages":         3,
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
	got := rawIdentityDebt(t, nil)
	if len(got) > 0 {
		t.Fatalf("raw identity construction debt must stay inside model constructors; offenders: %#v", got)
	}
}

func ambientTimeCalls(t *testing.T) map[string]int {
	t.Helper()
	out := map[string]int{}
	walkProductionGo(t, func(rel, _ string, b []byte) {
		if err := addAmbientTimeUses(out, rel, b); err != nil {
			t.Fatal(err)
		}
	})
	return out
}

// ambientTimeNames are the package time identifiers that read the wall clock
// or wait on it. A reference counts even without a call, so `var now =
// time.Now` is reported.
var ambientTimeNames = map[string]bool{
	"Now": true, "Since": true, "Until": true,
	"Sleep": true, "After": true, "AfterFunc": true, "Tick": true, "NewTicker": true, "NewTimer": true,
}

// addAmbientTimeUses counts ambient time references in one Go source file,
// keyed by file, enclosing top-level function ("<package>" for package-level
// declarations) and identifier. A dot import of time is reported on its own,
// because its uses cannot be told apart from local identifiers.
func addAmbientTimeUses(out map[string]int, rel string, src []byte) error {
	file, err := parser.ParseFile(token.NewFileSet(), rel, src, 0)
	if err != nil {
		return fmt.Errorf("parse %s: %w", rel, err)
	}
	timeNames := map[string]bool{}
	for _, imp := range file.Imports {
		if strings.Trim(imp.Path.Value, `"`) != "time" {
			continue
		}
		switch {
		case imp.Name == nil:
			timeNames["time"] = true
		case imp.Name.Name == ".":
			out[rel+":<import>:dot"]++
		case imp.Name.Name != "_":
			timeNames[imp.Name.Name] = true
		}
	}
	if len(timeNames) == 0 {
		return nil
	}
	for _, decl := range file.Decls {
		scope := "<package>"
		var node ast.Node = decl
		if fn, ok := decl.(*ast.FuncDecl); ok {
			if fn.Body == nil {
				continue
			}
			scope, node = fn.Name.Name, fn.Body
		}
		ast.Inspect(node, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			ident, ok := sel.X.(*ast.Ident)
			if ok && timeNames[ident.Name] && ambientTimeNames[sel.Sel.Name] {
				out[rel+":"+scope+":"+sel.Sel.Name]++
			}
			return true
		})
	}
	return nil
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
	re := regexp.MustCompile(`(?is)\b(delete\s+from|update)\s+(entries|messages|artifacts|conflicts|tool_invocations)\b`)
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
		if rel == "internal/model/identity.go" || rel == "internal/model/ref.go" {
			return
		}
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

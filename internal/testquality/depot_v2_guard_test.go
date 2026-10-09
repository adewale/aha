package testquality_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestDepotV2NeverDeletes enforces invariant I5 of docs/depot-v2-spec.md:
// the depot v2 layout is append-only forever — no GC, no retention, no
// compaction deletes. The objectStore interface already omits a delete
// primitive (construction-time guarantee), and the v2 state-machine test
// checks I5 against a shadow copy of the object tree for the paths it drives.
// This guard pins the residual risk that depot code reaches for the SDK or
// the filesystem directly. It scans every production file in internal/depot,
// so a new file cannot fall outside it.
func TestDepotV2NeverDeletes(t *testing.T) {
	// Removal of local temporary staging directories, which never hold depot
	// objects.
	want := map[string]int{
		"internal/depot/depot_v2.go:ensureBlob:os.RemoveAll(tmpDir)":      1,
		"internal/depot/materialisation_plan.go:Close:os.RemoveAll(root)": 1,
	}
	got := map[string]int{}
	dir := filepath.Join("..", "depot")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	scanned := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := addDepotDeletions(got, "internal/depot/"+name, src); err != nil {
			t.Fatal(err)
		}
		scanned++
	}
	if scanned == 0 {
		t.Fatal("found no production files in internal/depot; the scan is broken")
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("depot deletion inventory changed (I5: depot v2 never deletes)\ngot:  %#v\nwant: %#v\nDepot objects must never be deleted. Only removal of local temporary staging belongs in this list.", got, want)
	}
}

// TestDepotDeletionScannerReportsEveryDeleteShape gives the guard above teeth:
// each known-bad source must be reported, and each known-good source must not
// be.
func TestDepotDeletionScannerReportsEveryDeleteShape(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want map[string]int
	}{
		{"S3 DeleteObject", "package p\nfunc (s *store) drop(k string) { s.client.DeleteObject(nil, nil) }\n", map[string]int{"p.go:drop:DeleteObject": 1}},
		{"S3 DeleteObjects", "package p\nfunc drop(c client) { c.DeleteObjects(nil, nil) }\n", map[string]int{"p.go:drop:DeleteObjects": 1}},
		{"os.Remove", "package p\nimport \"os\"\nfunc drop(p string) { _ = os.Remove(p) }\n", map[string]int{"p.go:drop:os.Remove(p)": 1}},
		{"os.RemoveAll even with tmpDir on the line", "package p\nimport \"os\"\nfunc drop(root string) { _ = os.RemoveAll(root) } // tmpDir\n", map[string]int{"p.go:drop:os.RemoveAll(root)": 1}},
		{"renamed os import", "package p\nimport stdos \"os\"\nfunc drop(p string) { _ = stdos.Remove(p) }\n", map[string]int{"p.go:drop:os.Remove(p)": 1}},
		{"deferred removal in a function literal", "package p\nimport \"os\"\nfunc f(p string) { defer func() { _ = os.RemoveAll(p) }() }\n", map[string]int{"p.go:f:os.RemoveAll(p)": 1}},
		{"package-level function value", "package p\nimport \"os\"\nvar drop = os.Remove\n", map[string]int{"p.go:<package>:os.Remove": 1}},
		{"presigned delete", "package p\nfunc drop(c presigner, k string) { c.PresignDeleteObject(k) }\n", map[string]int{"p.go:drop:PresignDeleteObject": 1}},
		{"delete input built for a generic sender", "package p\nimport \"github.com/aws/aws-sdk-go-v2/service/s3\"\nvar in = &s3.DeleteObjectInput{}\n", map[string]int{"p.go:<package>:DeleteObjectInput": 1}},
		{"retargeted removal changes the key", "package p\nimport \"os\"\nfunc f(tmpDir, srcPath string) { defer os.RemoveAll(srcPath) }\n", map[string]int{"p.go:f:os.RemoveAll(srcPath)": 1}},
		{"reads and writes are fine", "package p\nimport \"os\"\nfunc f(p string) { _, _ = os.ReadFile(p); _ = os.WriteFile(p, nil, 0o600); _ = os.Rename(p, p) }\n", map[string]int{}},
		{"a local Remove method is fine", "package p\nfunc f(s set) { s.Remove(1) }\n", map[string]int{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := map[string]int{}
			if err := addDepotDeletions(got, "p.go", []byte(tc.src)); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %#v, want %#v", got, tc.want)
			}
		})
	}
}

// addDepotDeletions counts deletion references in one Go source file, keyed by
// file, enclosing top-level function ("<package>" for package-level
// declarations) and the deleting identifier. Any selector naming a Delete
// (DeleteObject, PresignDeleteObject, s3.DeleteObjectInput, ...) counts, and an
// os.Remove or os.RemoveAll call is keyed with its argument, so pointing an
// allowed removal at a different path changes the inventory.
func addDepotDeletions(out map[string]int, rel string, src []byte) error {
	file, err := parser.ParseFile(token.NewFileSet(), rel, src, 0)
	if err != nil {
		return fmt.Errorf("parse %s: %w", rel, err)
	}
	osNames := map[string]bool{}
	for _, imp := range file.Imports {
		if strings.Trim(imp.Path.Value, `"`) != "os" {
			continue
		}
		if imp.Name == nil {
			osNames["os"] = true
		} else if imp.Name.Name != "_" {
			osNames[imp.Name.Name] = true
		}
	}
	removals := map[*ast.SelectorExpr]*ast.CallExpr{}
	ast.Inspect(file, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
				removals[sel] = call
			}
		}
		return true
	})
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
			name := sel.Sel.Name
			if strings.Contains(name, "Delete") {
				out[rel+":"+scope+":"+name]++
				return true
			}
			if ident, ok := sel.X.(*ast.Ident); ok && osNames[ident.Name] && (name == "Remove" || name == "RemoveAll") {
				target := ""
				if call, ok := removals[sel]; ok && len(call.Args) == 1 {
					target = "(" + types.ExprString(call.Args[0]) + ")"
				}
				out[rel+":"+scope+":os."+name+target]++
			}
			return true
		})
	}
	return nil
}

package testquality_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBoundedFuzzSuiteRunsEveryNativeTarget(t *testing.T) {
	root := filepath.Join("..", "..")
	verify := repoFile(t, filepath.Join("scripts", "verify.sh"))

	var missing []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}

		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || !strings.HasPrefix(fn.Name.Name, "Fuzz") || fn.Recv != nil || fn.Type.Params.NumFields() != 1 {
				continue
			}
			star, ok := fn.Type.Params.List[0].Type.(*ast.StarExpr)
			if !ok {
				continue
			}
			selector, okSelector := star.X.(*ast.SelectorExpr)
			if !okSelector {
				continue
			}
			pkg, okPackage := selector.X.(*ast.Ident)
			if !okPackage || pkg.Name != "testing" || selector.Sel.Name != "F" {
				continue
			}

			dir, err := filepath.Rel(root, filepath.Dir(path))
			if err != nil {
				return err
			}
			packageArg := "./" + filepath.ToSlash(dir)
			if !fuzzCommandExists(verify, packageArg, fn.Name.Name) {
				missing = append(missing, packageArg+":"+fn.Name.Name)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) > 0 {
		t.Fatalf("scripts/verify.sh fuzz must run every native target; missing: %s", strings.Join(missing, ", "))
	}
}

func fuzzCommandExists(script, packageArg, target string) bool {
	for _, line := range strings.Split(script, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 || fields[0] != "run" || fields[1] != "go" || fields[2] != "test" || fields[3] != packageArg {
			continue
		}
		for _, field := range fields[4:] {
			if strings.Trim(field, `"'`) == "-fuzz="+target {
				return true
			}
		}
	}
	return false
}

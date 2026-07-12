package testquality_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDepotExportsNoRawArchivePublicationFunction(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate test source")
	}
	depotDir := filepath.Join(filepath.Dir(thisFile), "..", "depot")
	packages, err := parser.ParseDir(token.NewFileSet(), depotDir, func(info os.FileInfo) bool { return !strings.HasSuffix(info.Name(), "_test.go") }, 0)
	if err != nil {
		t.Fatal(err)
	}
	pkg := packages["depot"]
	if pkg == nil {
		t.Fatal("depot package not found")
	}
	for _, file := range pkg.Files {
		for _, declaration := range file.Decls {
			fn, ok := declaration.(*ast.FuncDecl)
			if !ok || !ast.IsExported(fn.Name.Name) {
				continue
			}
			forbidden := map[string]bool{"PushV2": true, "PushV2WithOptions": true, "ForMachine": true, "EnsureBlob": true, "CarriedBlob": true, "PublishSnapshot": true, "SetLatest": true}
			if forbidden[fn.Name.Name] {
				t.Fatalf("raw publication function or method %s is exported; publication must require PreparedUpload or UploadPlan", fn.Name.Name)
			}
		}
	}
}

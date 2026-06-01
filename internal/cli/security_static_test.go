package cli_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// networkAllowedPaths are the files/directories permitted to import Go
// network packages. internal/depot is the only place that initiates outbound
// network traffic (R2/S3). internal/server hosts the read-only loopback
// dashboard (inbound only, refuses non-loopback binds without an explicit
// opt-in); command_serve.go is the CLI wrapper that constructs it. Any new
// addition here must be reflected in docs/trust.md.
var networkAllowedPaths = []string{
	"/internal/depot/",
	"/internal/server/",
	"/internal/cli/command_serve.go",
}

func TestNoNetworkImportsOutsideDepot(t *testing.T) {
	roots := []string{"../../cmd", "../../internal"}
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			slash := filepath.ToSlash(path)
			for _, allowed := range networkAllowedPaths {
				if strings.Contains(slash, allowed) {
					return nil
				}
			}
			f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if err != nil {
				return err
			}
			for _, imp := range f.Imports {
				p := strings.Trim(imp.Path.Value, `"`)
				if p == "net" || strings.HasPrefix(p, "net/") {
					t.Fatalf("%s imports network API %q outside allowed paths; update docs/trust.md and networkAllowedPaths if core network behavior changes", path, p)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

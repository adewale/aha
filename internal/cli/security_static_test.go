package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNoNetworkImportsInApplicationPackages(t *testing.T) {
	roots := []string{"../../cmd", "../../internal"}
	forbidden := []string{
		`"net"`,
		`"net/http"`,
		`"net/url"`,
		`"net/rpc"`,
	}
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			text := string(b)
			for _, token := range forbidden {
				if strings.Contains(text, token) {
					t.Fatalf("%s imports network API %s; update docs/trust.md if v1 network behavior changes", path, token)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

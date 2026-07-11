package testquality_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// User-facing boundaries must normalize causal errors through internal/usererror.
// Raw err.Error() output makes dependency strings, SQL, paths, endpoints, and
// potentially secrets part of aha's public contract again.
func TestUserFacingBoundariesDoNotRenderRawErrors(t *testing.T) {
	checks := map[string][]string{
		filepath.Join("..", "cli", "cli.go"): {
			`fmt.Fprintln(stderr, "error:",`,
			`Message: err.Error()`,
		},
		filepath.Join("..", "cli", "command_doctor.go"): {
			`["error"] = err.Error()`,
			`["error"] = cfgErr.Error()`,
		},
		filepath.Join("..", "mcp", "tools.go"): {
			`Text: err.Error()`,
		},
		filepath.Join("..", "server", "server.go"): {
			`http.Error(w, err.Error()`,
			`writeError(w, http.StatusBadRequest, err.Error())`,
		},
	}
	for path, forbidden := range checks {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, needle := range forbidden {
			if strings.Contains(string(body), needle) {
				t.Fatalf("%s renders raw causal errors via %q", path, needle)
			}
		}
	}
}

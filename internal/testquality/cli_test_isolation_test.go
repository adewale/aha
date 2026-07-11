package testquality_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Depot init/use persist the selected depot. A CLI test that omits --config
// writes the real user's default config, as TestExportRequiresExistingSnapshot
// once did. Keep that failure mode out of the suite structurally.
func TestMutatingCLITestsAlwaysUseExplicitConfig(t *testing.T) {
	root := filepath.Join("..", "cli")
	invocation := regexp.MustCompile(`(?s)cli\.Run\(\[\]string\{"depot",\s*"(?:init|use)".*?\}`)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(path, "_test.go") {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, match := range invocation.FindAllString(string(body), -1) {
			if !strings.Contains(match, `"--config"`) {
				t.Errorf("%s has depot init/use without --config: %s", path, match)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

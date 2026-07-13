package testquality_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var removedCLIInvocation = regexp.MustCompile("\\baha (snapshot|refresh|ingest|depot|corpus|doctor|read|incidents|conflicts|serve|verify|export)(?:\\s|`|$)|--(depot|corpus|repo|accept-secrets)\\b")

func TestActiveJourneysExposeOnlyV02Vocabulary(t *testing.T) {
	for _, root := range []string{"../.."} {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			clean := filepath.ToSlash(path)
			if entry.IsDir() {
				if strings.Contains(clean, "/.git") || strings.Contains(clean, "/.pi-subagents") || strings.Contains(clean, "/node_modules") || strings.Contains(clean, "/docs/research") {
					return filepath.SkipDir
				}
				return nil
			}
			if !(strings.HasSuffix(path, ".md") || strings.HasSuffix(path, ".sh")) {
				return nil
			}
			if strings.HasSuffix(clean, "/docs/command-state-machine-v0.2-plan.md") || strings.HasSuffix(clean, "/docs/mcp-spec.md") || strings.HasSuffix(clean, "/docs/language-style.md") || strings.HasSuffix(clean, "/CHANGELOG.md") {
				return nil // normative removal table or explicitly documented legacy wire/history
			}
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if match := removedCLIInvocation.Find(body); match != nil {
				t.Errorf("%s exposes removed 0.1 vocabulary %q", path, match)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

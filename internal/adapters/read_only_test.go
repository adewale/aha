package adapters

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAdaptersStayReadOnly(t *testing.T) {
	forbidden := []string{
		"os.WriteFile",
		"os.Create(",
		"os.CreateTemp",
		"os.OpenFile",
		"os.Remove",
		"os.RemoveAll",
		"os.Rename",
		"os.Mkdir",
		"os.MkdirAll",
		"ioutil.WriteFile",
	}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		text := string(b)
		for _, token := range forbidden {
			if strings.Contains(text, token) {
				t.Fatalf("adapter file %s contains forbidden source-mutation call %q", file, token)
			}
		}
	}
}

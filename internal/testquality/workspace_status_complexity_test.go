package testquality_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspaceStatusIsVectorBoundedAndOpensSQLiteOnce(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "cli", "command_workspace.go"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	start := strings.Index(text, "func inspectWorkspace(")
	end := strings.Index(text[start:], "func workspaceNext(")
	if start < 0 || end < 0 {
		t.Fatal("cannot locate inspectWorkspace")
	}
	implementation := text[start : start+end]
	if strings.Count(implementation, "OpenExistingReadOnly(") != 1 {
		t.Fatalf("workspace status must open SQLite exactly once")
	}
	for _, forbidden := range []string{"VerifyContext(", "corpus.Status("} {
		if strings.Contains(implementation, forbidden) {
			t.Fatalf("workspace status performs unbounded scan via %s", forbidden)
		}
	}
}

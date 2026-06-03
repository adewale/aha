package testquality_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The agent instruction docs encode the two non-negotiable implementation
// principles for this repo. We test them the same way we test every other
// convention here: a static check that freezes the principle in place so it
// cannot silently disappear. The docs themselves mandate red-green-refactor
// TDD, so this test was written before the docs existed.

func repoFile(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join("..", "..", name)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(b)
}

func TestAgentsDocStatesImplementationPrinciples(t *testing.T) {
	agents := repoFile(t, "agents.md")
	for _, want := range []string{
		"red-green-refactor",
		"TDD",
		"correctness-by-construction",
	} {
		if !strings.Contains(strings.ToLower(agents), strings.ToLower(want)) {
			t.Fatalf("agents.md must state the principle %q", want)
		}
	}
}

func TestClaudeDocLoadsAgentsDoc(t *testing.T) {
	// The file must be named CLAUDE.md (case-sensitive) so Claude Code auto-loads
	// it, and it must use the @import syntax so agents.md is pulled into context
	// automatically rather than only mentioned in prose.
	claude := repoFile(t, "CLAUDE.md")
	if !strings.Contains(claude, "@agents.md") {
		t.Fatal("CLAUDE.md must @import agents.md so it auto-loads")
	}
}

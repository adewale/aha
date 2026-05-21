package cli_test

import (
	"os"
	"strings"
	"testing"

	"github.com/adewale/aha/internal/cli"
)

func TestSpecAndLessonsCycleLedgerStayInSync(t *testing.T) {
	specBytes, err := os.ReadFile("../../docs/agent-history-aggregator-spec.md")
	if err != nil {
		t.Fatal(err)
	}
	lessonsBytes, err := os.ReadFile("../../docs/lessons-learned.md")
	if err != nil {
		t.Fatal(err)
	}
	spec := string(specBytes)
	lessons := string(lessonsBytes)
	for _, want := range []string{
		"| 10 | Ledger-synchronized release redo",
		"Implementation attempts built: 7",
		"Full implementation rollbacks committed: 6",
		"Lesson/spec-update cycles recorded: 10",
	} {
		if !strings.Contains(spec, want) {
			t.Fatalf("spec cycle ledger missing %q", want)
		}
	}
	for _, want := range []string{
		"| 10 | Process accounting needs regression tests too.",
		"Implementation attempts built: 7",
		"Full implementation rollbacks committed: 6",
		"Lesson/spec-update cycles recorded: 10",
	} {
		if !strings.Contains(lessons, want) {
			t.Fatalf("lessons cycle ledger missing %q", want)
		}
	}
}

func TestRegisteredCommandsHaveAgentMetadata(t *testing.T) {
	for _, name := range cli.CommandNames() {
		cmd := cli.Registry()[name]
		if cmd.Usage == "" || cmd.Docs == "" || cmd.JSONSchema == "" || len(cmd.Examples) == 0 {
			t.Fatalf("command %s missing metadata: %+v", name, cmd)
		}
		if len(cmd.Flags) == 0 && name != "doctor" {
			t.Fatalf("command %s missing flag metadata", name)
		}
	}
}

func TestReadmeDocumentsRegisteredCommandsAndPrivacy(t *testing.T) {
	b, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	for _, name := range cli.CommandNames() {
		if !strings.Contains(text, "aha "+name) {
			t.Fatalf("README does not document command %s", name)
		}
	}
	if !strings.Contains(text, "does **not** redact secrets") {
		t.Fatalf("README privacy warning missing")
	}
	for _, doc := range []string{"docs/commands.md", "docs/trust.md", "docs/user-journeys.md", "docs/comparisons/claude-history-explorer.md", "docs/lessons-learned.md"} {
		if !strings.Contains(text, doc) {
			t.Fatalf("README missing link to %s", doc)
		}
	}
}

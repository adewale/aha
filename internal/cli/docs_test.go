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

func TestTrustDocScopesErrorNormalizationToApplicationBoundaries(t *testing.T) {
	b, err := os.ReadFile("../../docs/trust.md")
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	for _, want := range []string{"application/tool failures", "SDK-owned protocol", "not normalised by aha"} {
		if !strings.Contains(text, want) {
			t.Fatalf("trust document does not scope MCP error guarantee with %q", want)
		}
	}
}

func TestRegisteredCommandsHaveAgentMetadata(t *testing.T) {
	for _, name := range cli.CommandNames() {
		cmd := cli.Registry()[name]
		if cmd.Usage == "" || cmd.Docs == "" || cmd.JSONSchema == "" || len(cmd.Examples) == 0 {
			t.Fatalf("command %s missing metadata: %+v", name, cmd)
		}
		if len(cmd.Flags) == 0 {
			t.Fatalf("command %s missing flag metadata", name)
		}
		for _, flag := range cmd.Flags {
			if flag == "--json" && !strings.Contains(cmd.Usage, "--json") {
				t.Fatalf("command %s supports --json but usage omits it: %s", name, cmd.Usage)
			}
		}
	}
}

func TestReadmeDocumentsRegisteredCommandsAndPrivacy(t *testing.T) {
	b, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	for _, want := range []string{"aha archive upload", "aha archive download", "aha search", "aha show", "aha analyse failures", "aha dashboard", "aha mcp"} {
		if !strings.Contains(text, want) {
			t.Fatalf("README missing daily command %q", want)
		}
	}
	if !strings.Contains(text, "Redaction") || !strings.Contains(text, "Archive preserves raw history") {
		t.Fatalf("README privacy/redaction warning missing")
	}
	for _, doc := range []string{"docs/command-state-machine-v0.2-plan.md", "docs/command-inventory.md", "docs/commands.md", "docs/trust.md", "docs/user-journeys.md"} {
		if !strings.Contains(text, doc) {
			t.Fatalf("README missing link to %s", doc)
		}
	}
}

func TestCommandInventoryDocumentsRegisteredCommands(t *testing.T) {
	b, err := os.ReadFile("../../docs/command-inventory.md")
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	for _, name := range cli.CommandNames() {
		if !strings.Contains(text, "aha "+name) {
			t.Fatalf("command inventory does not document command %s", name)
		}
	}
	if !strings.Contains(text, "docs/commands.md") {
		t.Fatal("command inventory must link to generated commands reference")
	}
}

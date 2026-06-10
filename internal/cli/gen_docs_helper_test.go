package cli_test

import (
	"os"
	"testing"

	"github.com/adewale/aha/internal/cli"
)

// TestRegenerateCommandsMarkdown rewrites docs/commands.md from command
// metadata when AHA_REGEN_DOCS=1, keeping the generated reference and the
// registry in lockstep without a separate generator binary.
func TestRegenerateCommandsMarkdown(t *testing.T) {
	if os.Getenv("AHA_REGEN_DOCS") != "1" {
		t.Skip("set AHA_REGEN_DOCS=1 to regenerate docs/commands.md")
	}
	if err := os.WriteFile("../../docs/commands.md", []byte(cli.GenerateCommandsMarkdown()), 0o644); err != nil {
		t.Fatal(err)
	}
}

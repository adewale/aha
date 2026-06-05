package codegen_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adewale/aha/internal/mcp"
	"github.com/adewale/aha/internal/mcp/codegen"
)

// TestGeneratedTSFileIsUpToDate guards drift between the Go types and the
// checked-in TS surface. When you change a Go struct in internal/corpus or
// internal/search, re-run `go run ./cmd/aha-gen-ts` and commit the diff.
func TestGeneratedTSFileIsUpToDate(t *testing.T) {
	generated := codegen.Generate()
	path := filepath.Join("..", "..", "..", "clients", "typescript", "aha-mcp.ts")
	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if !bytes.Equal(generated, onDisk) {
		t.Fatalf("clients/typescript/aha-mcp.ts is stale; re-run `go run ./cmd/aha-gen-ts`")
	}
}

// TestGeneratedTSExposesEveryReadOnlyTool fails if a new MCP tool is added
// without a corresponding TS binding. The list mirrors readOnlyTools in
// internal/mcp/tools.go.
func TestGeneratedTSExposesEveryReadOnlyTool(t *testing.T) {
	body := string(codegen.Generate())
	for _, tool := range mcp.ToolNames {
		if !strings.Contains(body, tool+":") {
			t.Fatalf("TS surface missing binding for %q", tool)
		}
		if !strings.Contains(body, `"`+tool+`"`) {
			t.Fatalf("TS surface missing string literal %q (expected in TOOLS or transport.call)", tool)
		}
	}
}

func TestGeneratedTSUsesWireRefStringsOutsideSearchResult(t *testing.T) {
	body := string(codegen.Generate())
	for _, want := range []string{
		"  ref: Ref;\n  ref_text: string;",
		"export interface TrajectoryStep {\n  family: string;\n  ref: string;",
		"export interface IncidentTrajectoryArgs {\n  /**\n   * Resolving-success ref (msg:v1:...), e.g. an incident path sample_ref\n   */\n  ref: string;",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("generated TS ref type contract missing %q", want)
		}
	}
}

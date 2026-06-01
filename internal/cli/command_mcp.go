package cli

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/adewale/aha/internal/mcp"
)

// cmdMcp runs the read-only stdio MCP server. The MCP wire format (JSON-RPC
// 2.0 over NDJSON-framed stdio) is owned by the SDK; stdin/stdout carry
// the protocol, stderr carries any human-facing diagnostics. See
// docs/mcp-spec.md.
func cmdMcp(args []string, _, stderr io.Writer) error {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cf := registerCorpusFlags(fs)
	dryRun := fs.Bool("dry-run", false, "open the corpus, register tools, print a one-line summary to stderr, then exit without serving stdio")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := cf.loadConfig()
	if err != nil {
		return err
	}
	store, err := openCorpusForCommand(cfg, false)
	if err != nil {
		return err
	}
	defer store.Close()
	if *dryRun {
		// Build the server (which registers every tool) and exit
		// without reading stdin. Hosts use this to smoke-test that
		// `aha mcp` can open its corpus and that the registered tool
		// set is what they expect.
		_ = mcp.NewServer(mcp.NewCorpusBackend(store, cfg))
		fmt.Fprintf(stderr, "aha mcp dry-run ok: %d tools (%s)\n", len(mcp.ToolNames), strings.Join(mcp.ToolNames, ", "))
		return nil
	}
	return mcp.Serve(mcp.NewCorpusBackend(store, cfg))
}

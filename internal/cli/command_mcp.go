package cli

import (
	"flag"
	"io"
	"os"

	"github.com/adewale/aha/internal/mcp"
)

// cmdMcp runs the read-only stdio MCP server. Stdin/stdout carry the JSON-RPC
// protocol; stderr carries any human-facing diagnostics. See docs/mcp-spec.md.
func cmdMcp(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cf := registerCorpusFlags(fs)
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
	backend := mcp.NewCorpusBackend(store, cfg)
	// stdout owns the protocol channel even when callers piped stdout
	// elsewhere; explicitly route to os.Stdout so framed bytes always land
	// on the inherited fd1.
	if stdout == nil {
		stdout = os.Stdout
	}
	return mcp.Serve(backend, os.Stdin, stdout, stderr)
}

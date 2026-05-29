package cli

import (
	"flag"
	"io"

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
	return mcp.Serve(mcp.NewCorpusBackend(store, cfg))
}

package cli

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/adewale/aha/internal/mcp"
)

// cmdMcp exposes explicit check and serve operations. Stdio protocol output
// remains isolated on stdout; human diagnostics use stderr.
func cmdMcp(args []string, stdout io.Writer, stderr io.Writer) error {
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h" || args[0] == "help") {
		fmt.Fprintln(stdout, "Usage: aha mcp <check|serve> [--workspace PATH]")
		return nil
	}
	if len(args) == 0 {
		return fmt.Errorf("mcp requires subcommand: check or serve")
	}
	sub := args[0]
	if sub != "check" && sub != "serve" {
		return fmt.Errorf("unknown mcp subcommand %q", sub)
	}
	fs := flag.NewFlagSet("mcp "+sub, flag.ContinueOnError)
	fs.SetOutput(flagOutput(args, stderr))
	wf := registerWorkspaceFlags(fs)
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	cfg, err := wf.loadConfig()
	if err != nil {
		return err
	}
	store, err := openCorpusForCommand(cfg, false)
	if err != nil {
		return err
	}
	defer store.Close()
	if sub == "check" {
		_ = mcp.NewServer(mcp.NewCorpusBackend(store, cfg))
		fmt.Fprintf(stderr, "aha mcp check ok: %d tools (%s)\n", len(mcp.ToolNames), strings.Join(mcp.ToolNames, ", "))
		return nil
	}
	return mcp.Serve(mcp.NewCorpusBackend(store, cfg))
}

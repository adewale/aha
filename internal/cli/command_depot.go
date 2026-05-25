package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/adewale/aha/internal/config"
	"github.com/adewale/aha/internal/safety"
)

func cmdDepot(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("depot requires subcommand: init, ls, verify")
	}
	if args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		fmt.Fprintln(stdout, "Usage of aha depot: aha depot <init|ls|verify> [DEPOT] [--json] [--repair]")
		return nil
	}
	sub := args[0]
	fs := flag.NewFlagSet("depot "+sub, flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "config path")
	jsonOut := fs.Bool("json", false, "JSON output")
	repair := fs.Bool("repair", false, "repair catalog from bundle objects")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	addr := ""
	if fs.NArg() > 0 {
		addr = fs.Arg(0)
	}
	drv, err := depotDriverForConfig(cfg, addr)
	if err != nil {
		return err
	}
	ctx := context.Background()
	switch sub {
	case "init":
		if drv.Address().Type == "local" {
			if err := safety.ValidateWriteOutsideSources(cfg, drv.Address().Location, "depot"); err != nil {
				return err
			}
		}
		if err := drv.Init(ctx); err != nil {
			return err
		}
		configWritten := ""
		if addr != "" {
			cfg.Depot.Type = drv.Address().Type
			cfg.Depot.Location = drv.Address().Location
			path, err := config.Write(*configPath, cfg, "// aha config (JSONC)\n// Updated by `aha depot init`.\n")
			if err != nil {
				return err
			}
			configWritten = path
		}
		if *jsonOut {
			return writeJSON(stdout, map[string]any{"depot": drv.Address(), "created": true, "config": configWritten})
		}
		fmt.Fprintf(stdout, "depot %s:%s ready\n", drv.Address().Type, drv.Address().Location)
		if configWritten != "" {
			fmt.Fprintf(stdout, "config:%s\n", configWritten)
		}
		return nil
	case "ls":
		refs, err := drv.List(ctx)
		if err != nil {
			return err
		}
		if *jsonOut {
			return writeJSON(stdout, refs)
		}
		for _, ref := range refs {
			fmt.Fprintf(stdout, "%s\t%s\t%s\t%s\n", ref.BundleSHA256, ref.MachineID, ref.CapturedAt, ref.Key)
		}
		return nil
	case "verify":
		report, err := drv.Verify(ctx, *repair)
		if err != nil {
			return err
		}
		if *jsonOut {
			return writeJSON(stdout, report)
		}
		fmt.Fprintf(stdout, "bundles=%d catalogs=%d repaired=%v problems=%d\n", report.Bundles, report.Catalogs, report.Repaired, len(report.Problems))
		return nil
	default:
		return fmt.Errorf("unknown depot subcommand %q", sub)
	}
}

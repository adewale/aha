package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/adewale/aha/internal/config"
	"github.com/adewale/aha/internal/depot"
	"github.com/adewale/aha/internal/model"
	"github.com/adewale/aha/internal/safety"
)

func cmdDepot(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("depot requires subcommand: init, use, ls, verify, compact")
	}
	if args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		fmt.Fprintln(stdout, "Usage of aha depot: aha depot <init|use|ls|verify|compact> [DEPOT] [--json] [--repair] [--deep]")
		return nil
	}
	sub := args[0]
	fs := flag.NewFlagSet("depot "+sub, flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "config path")
	jsonOut := fs.Bool("json", false, "JSON output")
	repair := fs.Bool("repair", false, "repair catalog from bundle objects")
	deep := fs.Bool("deep", false, "deep verify bundle bytes/manifests")
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
			if err := captureDepotR2Config(&cfg); err != nil {
				return err
			}
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
		report, err := depot.VerifyWithOptions(ctx, drv, depot.VerifyOptions{Repair: *repair, Deep: *deep})
		if err != nil {
			return err
		}
		if *jsonOut {
			return writeJSON(stdout, report)
		}
		fmt.Fprintf(stdout, "bundles=%d catalogs=%d repaired=%v problems=%d\n", report.Bundles, report.Catalogs, report.Repaired, len(report.Problems))
		return nil
	case "compact":
		report, err := depot.Compact(ctx, drv)
		if err != nil {
			return err
		}
		if *jsonOut {
			return writeJSON(stdout, report)
		}
		fmt.Fprintf(stdout, "catalogs=%d refs_before=%d refs_after=%d duplicate_refs=%d catalogs_written=%d\n", report.Catalogs, report.RefsBefore, report.RefsAfter, report.DuplicateRefs, report.CatalogsWritten)
		return nil
	case "use":
		if addr == "" {
			return errors.New("depot use requires a depot address, e.g. `aha depot use r2:aha-depot` or `aha depot use local:~/.aha/depot`")
		}
		report, err := drv.Verify(ctx, false)
		if err != nil {
			for _, h := range depotErrorHints(err) {
				fmt.Fprintln(stderr, "hint:", h)
			}
			return err
		}
		if depotUninitialized(report) {
			return fmt.Errorf("depot %s:%s is reachable but not initialized; run `aha depot init %s` first", drv.Address().Type, drv.Address().Location, addr)
		}
		if len(report.Problems) > 0 {
			return fmt.Errorf("depot %s:%s has problems: %s; run `aha depot verify %s --repair`", drv.Address().Type, drv.Address().Location, strings.Join(report.Problems, "; "), addr)
		}
		cfg.Depot.Type = drv.Address().Type
		cfg.Depot.Location = drv.Address().Location
		if err := captureDepotR2Config(&cfg); err != nil {
			return err
		}
		path, err := config.Write(*configPath, cfg, "// aha config (JSONC)\n// Updated by `aha depot use`.\n")
		if err != nil {
			return err
		}
		if *jsonOut {
			return writeJSON(stdout, map[string]any{"depot": drv.Address(), "switched": true, "config": path})
		}
		fmt.Fprintf(stdout, "depot default set to %s:%s\n", drv.Address().Type, drv.Address().Location)
		fmt.Fprintf(stdout, "config:%s\n", path)
		return nil
	default:
		return fmt.Errorf("unknown depot subcommand %q", sub)
	}
}

// captureDepotR2Config persists the non-secret R2 settings into config so a
// configured R2 default keeps working in a new shell with only the two secret
// keys exported. The access key id and secret are never written to config.
func captureDepotR2Config(cfg *model.Config) error {
	if cfg.Depot.Type != "r2" {
		return nil
	}
	rc, err := depot.ResolveR2Config(cfg.Depot.R2)
	if err != nil {
		return err
	}
	cfg.Depot.R2.AccountID = rc.AccountID
	// Persist an endpoint only when explicitly provided (jurisdiction or fake-S3);
	// the account-derived endpoint stays implicit so it can't drift.
	if endpoint := firstNonEmpty(os.Getenv("AHA_R2_ENDPOINT"), os.Getenv("R2_ENDPOINT"), cfg.Depot.R2.Endpoint); endpoint != "" {
		cfg.Depot.R2.Endpoint = endpoint
	}
	if rc.Region != "" && rc.Region != "auto" {
		cfg.Depot.R2.Region = rc.Region
	}
	return nil
}

// depotUninitialized reports whether a successful verify means the depot is
// reachable but not yet provisioned (its depot.json marker is absent), as
// opposed to one with real problems (an invalid marker, missing bundles, ...).
func depotUninitialized(report depot.VerifyReport) bool {
	return len(report.Problems) == 1 && report.Problems[0] == "missing depot marker"
}

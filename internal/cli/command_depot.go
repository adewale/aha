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
		return errors.New("depot requires subcommand: init, use, ls, verify")
	}
	if args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		fmt.Fprintln(stdout, "Usage of aha depot: aha depot <init|use|ls|verify> [DEPOT] [--json] [--deep]")
		return nil
	}
	sub := args[0]
	fs := flag.NewFlagSet("depot "+sub, flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "config path")
	jsonOut := fs.Bool("json", false, "JSON output")
	deep := fs.Bool("deep", false, "deep verify blob contents and historical manifests")
	if err := fs.Parse(interspersedDepotFlagArgs(args[1:])); err != nil {
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
	if sub == "use" && addr == "" {
		return errors.New("depot use requires a depot address, e.g. `aha depot use r2:aha-depot` or `aha depot use local:~/.aha/depot`")
	}
	v2, err := depotV2ForConfig(cfg, addr)
	if err != nil {
		return err
	}
	ctx := context.Background()
	switch sub {
	case "init":
		if v2.Address().Type == "local" {
			if err := safety.ValidateWriteOutsideSources(cfg, v2.Address().Location, "depot"); err != nil {
				return err
			}
		}
		if err := v2.Init(ctx); err != nil {
			return err
		}
		configWritten := ""
		if addr != "" {
			cfg.Depot.Type = v2.Address().Type
			cfg.Depot.Location = v2.Address().Location
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
			return writeJSON(stdout, map[string]any{"depot": v2.Address(), "created": true, "config": configWritten})
		}
		fmt.Fprintf(stdout, "depot %s:%s ready\n", v2.Address().Type, v2.Address().Location)
		if configWritten != "" {
			fmt.Fprintf(stdout, "config:%s\n", configWritten)
		}
		return nil
	case "ls":
		items, err := depotListV2(ctx, v2)
		if err != nil {
			return err
		}
		if *jsonOut {
			return writeJSON(stdout, items)
		}
		for _, item := range items {
			fmt.Fprintf(stdout, "%s\t%s\t%s\tfiles=%d\n", item["manifest_sha256"], item["machine"], item["captured_at"], item["files"])
		}
		return nil
	case "verify":
		report, err := v2.Verify(ctx, *deep)
		if err != nil {
			return err
		}
		if *jsonOut {
			return writeJSON(stdout, report)
		}
		fmt.Fprintf(stdout, "manifests=%d machines=%d deep=%v problems=%d\n", report.Manifests, report.Machines, report.Deep, len(report.Problems))
		for _, p := range report.Problems {
			fmt.Fprintf(stdout, "problem: %s\n", p)
		}
		return nil
	case "use":
		report, err := v2.Verify(ctx, false)
		if err != nil {
			for _, h := range depotErrorHints(err) {
				fmt.Fprintln(stderr, "hint:", h)
			}
			return err
		}
		if depotUninitialized(report) {
			return fmt.Errorf("depot %s:%s is reachable but not initialized; run `aha depot init %s` first", v2.Address().Type, v2.Address().Location, addr)
		}
		if len(report.Problems) > 0 {
			return fmt.Errorf("depot %s:%s has problems: %s; run `aha depot verify %s --deep`", v2.Address().Type, v2.Address().Location, strings.Join(report.Problems, "; "), addr)
		}
		cfg.Depot.Type = v2.Address().Type
		cfg.Depot.Location = v2.Address().Location
		if err := captureDepotR2Config(&cfg); err != nil {
			return err
		}
		path, err := config.Write(*configPath, cfg, "// aha config (JSONC)\n// Updated by `aha depot use`.\n")
		if err != nil {
			return err
		}
		if *jsonOut {
			return writeJSON(stdout, map[string]any{"depot": v2.Address(), "switched": true, "config": path})
		}
		fmt.Fprintf(stdout, "depot default set to %s:%s\n", v2.Address().Type, v2.Address().Location)
		fmt.Fprintf(stdout, "config:%s\n", path)
		return nil
	default:
		return fmt.Errorf("unknown depot subcommand %q", sub)
	}
}

// depotListV2 lists every machine's latest snapshot: one index GET, one
// pointer GET and one manifest GET per machine.
func depotListV2(ctx context.Context, v2 *depot.V2) ([]map[string]any, error) {
	machines, err := v2.Machines(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(machines))
	for _, machine := range machines {
		sha, ok, err := v2.Latest(ctx, machine)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		manifest, err := v2.Manifest(ctx, machine, sha)
		if err != nil {
			return nil, err
		}
		items = append(items, map[string]any{"machine": machine, "manifest_sha256": sha.String(), "captured_at": manifest.CapturedAt, "files": len(manifest.Files)})
	}
	return items, nil
}

// captureDepotR2Config persists the non-secret R2 settings into config so a
// configured R2 default keeps working in a new shell with only the two secret
// keys exported. The access key id and secret are never written to config.
func interspersedDepotFlagArgs(args []string) []string {
	var flags []string
	var positionals []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "-") {
			flags = append(flags, arg)
			if (arg == "--config" || arg == "-config") && i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
			continue
		}
		positionals = append(positionals, arg)
	}
	return append(flags, positionals...)
}

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
	if rc.Region == "" || rc.Region == "auto" {
		cfg.Depot.R2.Region = ""
	} else {
		cfg.Depot.R2.Region = rc.Region
	}
	return nil
}

// depotUninitialized reports whether a successful verify means the depot is
// reachable but not yet provisioned (its marker is absent), as opposed to
// one with real problems.
func depotUninitialized(report depot.VerifyReport) bool {
	return report.Manifests == 0 && report.Machines == 0 && len(report.Problems) == 1 && report.Problems[0] == "missing depot marker"
}

package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/adewale/aha/internal/config"
	"github.com/adewale/aha/internal/paths"
	"github.com/adewale/aha/internal/safety"
)

func cmdInit(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(flagOutput(args, stderr))
	configPath := fs.String("config", config.DefaultPath(), "config path")
	acknowledge := fs.Bool("acknowledge-raw-history", false, "acknowledge that raw agent histories may contain secrets")
	jsonOut := fs.Bool("json", false, "JSON output")
	dryRun := fs.Bool("dry-run", false, "inspect and plan without mutation")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !*acknowledge {
		return errPrivacyAcknowledgement
	}
	path, err := paths.Expand(*configPath)
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("config already exists: %s", path)
	} else if !os.IsNotExist(err) {
		return err
	}
	cfg := config.Default()
	cfg.AcknowledgedRawHistory = true
	if err := safety.ValidateWriteOutsideSources(cfg, cfg.Archive.Location, "Archive"); err != nil {
		return err
	}
	if _, err := safety.PrepareWorkspaceDestination(cfg, cfg.WorkspaceDir); err != nil {
		return err
	}
	archive, err := depotV2ForConfig(cfg, "")
	if err != nil {
		return err
	}
	if *dryRun {
		out := map[string]any{"state": "planned", "dry_run": true, "config": path, "archive": archive.Address().String(), "workspace": cfg.WorkspaceDir}
		if *jsonOut {
			return writeJSON(stdout, out)
		}
		return renderMap(stdout, out)
	}
	if err := archive.Init(context.Background()); err != nil {
		return err
	}
	written, err := config.Write(path, cfg, "// aha config (JSONC)\n// Raw-history privacy warning acknowledged by `aha init --acknowledge-raw-history`.\n")
	if err != nil {
		return err
	}
	out := map[string]any{"config": written, "acknowledged_raw_history": true, "archive": archive.Address().String(), "workspace": cfg.WorkspaceDir}
	if *jsonOut {
		return writeJSON(stdout, out)
	}
	return renderMap(stdout, out)
}

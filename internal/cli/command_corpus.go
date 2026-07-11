package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/adewale/aha/internal/corpus"
	"github.com/adewale/aha/internal/depot"
	"github.com/adewale/aha/internal/safety"
)

func cmdCorpus(args []string, stdout, stderr io.Writer) error {
	return runCorpusContext(context.Background(), args, stdout, stderr)
}

func runCorpusContext(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("corpus requires subcommand: size, vacuum, prune-orphans, rebuild")
	}
	if args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		fmt.Fprintln(stdout, "Usage of aha corpus <size|vacuum|prune-orphans|rebuild> [--repo DIR] [--progress MODE] [--json] [--force|--backup]")
		return nil
	}
	sub := args[0]
	fs := flag.NewFlagSet("corpus "+sub, flag.ContinueOnError)
	fs.SetOutput(stderr)
	cf := registerCorpusFlags(fs)
	jsonOut := fs.Bool("json", false, "JSON output")
	force := fs.Bool("force", false, "perform destructive maintenance; default is dry run")
	backup := fs.Bool("backup", false, "preserve a pre-v2 corpus while atomically rebuilding it")
	progressSetting := fs.String("progress", "auto", "progress mode: auto, off, plain, tty, or json (stderr only)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	cfg, err := cf.loadConfig()
	if err != nil {
		return err
	}
	progress, err := newProgressSession(stderr, *progressSetting, *jsonOut)
	if err != nil {
		return err
	}
	defer progress.Close()
	switch sub {
	case "rebuild":
		if !*backup {
			return errors.New("corpus rebuild requires --backup; an unsafe no-backup mode is not supported")
		}
		if err := safety.ValidateWriteOutsideSources(cfg, cfg.CorpusDir, "corpus"); err != nil {
			return err
		}
		v2, err := depotV2ForConfig(cfg, "")
		if err != nil {
			return err
		}
		depotReport, err := v2.VerifyWithOptions(ctx, false, depot.VerifyOptions{Progress: progress.Tracker})
		if err != nil {
			return err
		}
		if depotUninitialized(depotReport) {
			return errors.New("default depot is not initialized; initialize it before rebuilding the corpus")
		}
		if len(depotReport.Problems) > 0 {
			return fmt.Errorf("default depot has problems: %s", strings.Join(depotReport.Problems, "; "))
		}
		report, err := corpus.RebuildWithBackupOptions(cfg.CorpusDir, func(staging string) error {
			stagingCfg := cfg
			stagingCfg.CorpusDir = staging
			store, err := openCorpusForCommand(stagingCfg, true)
			if err != nil {
				return err
			}
			ing, err := ingestorForConfig(store, stagingCfg)
			if err != nil {
				return errors.Join(err, store.Close())
			}
			ing.Context = ctx
			_, pullErr := pullFromDepotV2(ctx, io.Discard, ing, v2, true, progress.Tracker)
			return errors.Join(pullErr, store.Close())
		}, corpus.RebuildOptions{Context: ctx, Progress: progress.Tracker})
		if err != nil {
			return err
		}
		actionArgs := []string{"refresh", "--max-sessions", "1"}
		if *cf.config != "" {
			actionArgs = append(actionArgs, "--config", *cf.config)
		}
		if *cf.repoDir != "" {
			actionArgs = append(actionArgs, "--repo", *cf.repoDir)
		} else if *cf.corpusDir != "" {
			actionArgs = append(actionArgs, "--corpus", *cf.corpusDir)
		}
		action := nextAction{Command: "aha", Args: actionArgs}
		next, structured := actionOutput(action)
		out := struct {
			corpus.RebuildReport
			Next       []string   `json:"next"`
			NextAction nextAction `json:"next_action"`
		}{RebuildReport: report, Next: next, NextAction: structured}
		if *jsonOut {
			return writeJSON(stdout, out)
		}
		fmt.Fprintf(stdout, "corpus rebuilt at %s\nbackup preserved at %s\nnext: %s\n", report.Root, report.Backup, action.String())
		return nil
	case "size":
		store, err := openCorpusForCommand(cfg, false)
		if err != nil {
			return err
		}
		defer store.Close()
		report, err := corpus.Size(store)
		if err != nil {
			return err
		}
		if *jsonOut {
			return writeJSON(stdout, report)
		}
		return renderMap(stdout, map[string]any{"root": report.Root, "total_bytes": report.TotalBytes, "database_bytes": report.DatabaseBytes, "file_blob_bytes": report.FileBlobBytes, "image_blob_bytes": report.ImageBlobBytes, "other_bytes": report.OtherBytes, "files": report.Files})
	case "vacuum":
		store, err := openCorpusForCommand(cfg, false)
		if err != nil {
			return err
		}
		defer store.Close()
		before, err := corpus.Size(store)
		if err != nil {
			return err
		}
		if err := corpus.Vacuum(store.DB); err != nil {
			return err
		}
		after, err := corpus.Size(store)
		if err != nil {
			return err
		}
		reclaimed := before.TotalBytes - after.TotalBytes
		if reclaimed < 0 {
			reclaimed = 0
		}
		out := map[string]any{"root": after.Root, "before_bytes": before.TotalBytes, "after_bytes": after.TotalBytes, "reclaimed_bytes": reclaimed}
		if *jsonOut {
			return writeJSON(stdout, out)
		}
		return renderMap(stdout, out)
	case "prune-orphans":
		store, err := openCorpusForCommand(cfg, false)
		if err != nil {
			return err
		}
		defer store.Close()
		report, err := corpus.PruneOrphanBlobs(store, *force)
		if err != nil {
			return err
		}
		if *jsonOut {
			return writeJSON(stdout, report)
		}
		return renderMap(stdout, map[string]any{"root": report.Root, "dry_run": report.DryRun, "orphans": len(report.Orphans), "orphan_bytes": report.OrphanBytes, "deleted_files": report.DeletedFiles, "deleted_bytes": report.DeletedBytes})
	default:
		return fmt.Errorf("unknown corpus subcommand %q", sub)
	}
}

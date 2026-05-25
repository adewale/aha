package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/adewale/aha/internal/corpus"
)

func cmdCorpus(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("corpus requires subcommand: size, vacuum, prune-orphans")
	}
	if args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		fmt.Fprintln(stdout, "Usage of aha corpus <size|vacuum|prune-orphans> [--repo DIR] [--json] [--force]")
		return nil
	}
	sub := args[0]
	fs := flag.NewFlagSet("corpus "+sub, flag.ContinueOnError)
	fs.SetOutput(stderr)
	cf := registerCorpusFlags(fs)
	jsonOut := fs.Bool("json", false, "JSON output")
	force := fs.Bool("force", false, "perform destructive maintenance; default is dry run")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	cfg, err := cf.loadConfig()
	if err != nil {
		return err
	}
	switch sub {
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
		return renderMap(stdout, map[string]any{"root": report.Root, "total_bytes": report.TotalBytes, "database_bytes": report.DatabaseBytes, "bundle_blob_bytes": report.BundleBlobBytes, "file_blob_bytes": report.FileBlobBytes, "image_blob_bytes": report.ImageBlobBytes, "other_bytes": report.OtherBytes, "files": report.Files})
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

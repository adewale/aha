package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"path/filepath"

	ahaprogress "github.com/adewale/aha/internal/progress"
)

func cmdIngest(args []string, stdout, stderr io.Writer) error {
	return runIngestContext(context.Background(), args, stdout, stderr)
}

func runIngestContext(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("ingest", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cf := registerCorpusFlags(fs)
	depotAddr := fs.String("depot", "", "depot address")
	jsonOut := fs.Bool("json", false, "JSON output")
	progressSetting := fs.String("progress", "auto", "progress mode: auto, off, plain, tty, or json (stderr only)")
	if err := fs.Parse(args); err != nil {
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
	bundles := fs.Args()
	store, err := openCorpusForCommand(cfg, true)
	if err != nil {
		return err
	}
	defer store.Close()
	ing, err := ingestorForConfig(store, cfg)
	if err != nil {
		return err
	}
	ing.Context = ctx
	var reports []map[string]any
	if len(bundles) == 0 {
		v2, err := depotV2ForConfig(cfg, *depotAddr)
		if err != nil {
			return err
		}
		reports, err = pullFromDepotV2(ctx, stdout, ing, v2, *jsonOut, progress.Tracker)
		if err != nil {
			return err
		}
		if *jsonOut {
			return writeJSON(stdout, reports)
		}
		return nil
	}
	var bundlePaths []string
	for _, pattern := range bundles {
		matches, _ := filepath.Glob(pattern)
		if len(matches) == 0 {
			matches = []string{pattern}
		}
		bundlePaths = append(bundlePaths, matches...)
	}
	bundleTotal := ahaprogress.KnownTotal(uint64(len(bundlePaths)))
	progress.Tracker.Start(ahaprogress.PhaseIngest, bundleTotal, ahaprogress.UnitFiles)
	for i, path := range bundlePaths {
		if err := ctx.Err(); err != nil {
			progress.Tracker.Cancel(ahaprogress.PhaseIngest, uint64(i), bundleTotal, ahaprogress.UnitFiles)
			return err
		}
		rep, err := ing.IngestBundle(path)
		if err != nil {
			if ctx.Err() != nil {
				progress.Tracker.Cancel(ahaprogress.PhaseIngest, uint64(i), bundleTotal, ahaprogress.UnitFiles)
			} else {
				progress.Tracker.Fail(ahaprogress.PhaseIngest, uint64(i), bundleTotal, ahaprogress.UnitFiles)
			}
			return err
		}
		item := map[string]any{"bundle": path, "sessions": rep.Sessions, "entries": rep.Entries, "messages": rep.Messages, "images": rep.Images, "artifacts": rep.Artifacts, "duplicate": rep.Duplicate}
		if *jsonOut {
			reports = append(reports, item)
		} else {
			fmt.Fprintf(stdout, "%s: sessions=%d entries=%d messages=%d images=%d artifacts=%d duplicate=%v\n", path, rep.Sessions, rep.Entries, rep.Messages, rep.Images, rep.Artifacts, rep.Duplicate)
		}
		progress.Tracker.Advance(ahaprogress.PhaseIngest, uint64(i+1), bundleTotal, ahaprogress.UnitFiles)
	}
	progress.Tracker.Complete(ahaprogress.PhaseIngest, uint64(len(bundlePaths)), bundleTotal, ahaprogress.UnitFiles)
	if *jsonOut {
		return writeJSON(stdout, reports)
	}
	return nil
}

package cli

import (
	"flag"
	"fmt"
	"io"
	"path/filepath"
)

func cmdIngest(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("ingest", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cf := registerCorpusFlags(fs)
	depotAddr := fs.String("depot", "", "depot address")
	jsonOut := fs.Bool("json", false, "JSON output")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := cf.loadConfig()
	if err != nil {
		return err
	}
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
	var reports []map[string]any
	if len(bundles) == 0 {
		drv, err := depotDriverForConfig(cfg, *depotAddr)
		if err != nil {
			return err
		}
		reports, err = ingestFromDepotWith(stdout, ing, drv, *jsonOut)
		if err != nil {
			return err
		}
		if *jsonOut {
			return writeJSON(stdout, reports)
		}
		return nil
	}
	for _, pattern := range bundles {
		matches, _ := filepath.Glob(pattern)
		if len(matches) == 0 {
			matches = []string{pattern}
		}
		for _, path := range matches {
			rep, err := ing.IngestBundle(path)
			if err != nil {
				return err
			}
			item := map[string]any{"bundle": path, "sessions": rep.Sessions, "entries": rep.Entries, "messages": rep.Messages, "images": rep.Images, "artifacts": rep.Artifacts, "duplicate": rep.Duplicate}
			if *jsonOut {
				reports = append(reports, item)
			} else {
				fmt.Fprintf(stdout, "%s: sessions=%d entries=%d messages=%d images=%d artifacts=%d duplicate=%v\n", path, rep.Sessions, rep.Entries, rep.Messages, rep.Images, rep.Artifacts, rep.Duplicate)
			}
		}
	}
	if *jsonOut {
		return writeJSON(stdout, reports)
	}
	return nil
}

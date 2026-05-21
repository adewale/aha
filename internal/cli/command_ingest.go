package cli

import (
	"flag"
	"fmt"
	"io"
	"path/filepath"

	"github.com/adewale/aha/internal/adapters"
	"github.com/adewale/aha/internal/corpus"
	"github.com/adewale/aha/internal/paths"
)

func cmdIngest(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("ingest", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cf := registerCorpusFlags(fs)
	jsonOut := fs.Bool("json", false, "JSON output")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := cf.loadConfig()
	if err != nil {
		return err
	}
	bundles := fs.Args()
	if len(bundles) == 0 {
		out, err := paths.Expand(cfg.BundleOutDir)
		if err != nil {
			return err
		}
		bundles = []string{filepath.Join(out, "*.tar.zst")}
	}
	store, err := openCorpusForCommand(cfg, true)
	if err != nil {
		return err
	}
	defer store.Close()
	var reports []map[string]any
	for _, pattern := range bundles {
		matches, _ := filepath.Glob(pattern)
		if len(matches) == 0 {
			if len(fs.Args()) == 0 {
				return fmt.Errorf("no bundles found for %s", pattern)
			}
			matches = []string{pattern}
		}
		for _, path := range matches {
			rep, err := corpus.IngestBundle(store, adapters.Builtins(), path)
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

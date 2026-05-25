package cli

import (
	"context"
	"flag"
	"io"

	"github.com/adewale/aha/internal/corpus"
	"github.com/adewale/aha/internal/depot"
	"github.com/adewale/aha/internal/model"
)

func cmdStatus(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cf := registerCorpusFlags(fs)
	depotAddr := fs.String("depot", "", "compare corpus against depot")
	jsonOut := fs.Bool("json", false, "JSON output")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := cf.loadConfig()
	if err != nil {
		return err
	}
	store, err := openCorpusForCommand(cfg, false)
	if err != nil {
		return err
	}
	defer store.Close()
	stats, err := corpus.Status(store.DB, store.Root)
	if err != nil {
		return err
	}
	if *depotAddr != "" {
		behind, err := depotBehindCount(store, cfg, *depotAddr)
		if err != nil {
			return err
		}
		stats["depot_behind_bundles"] = behind
	}
	stats["next"] = statusNext(stats)
	if *jsonOut {
		return writeJSON(stdout, stats)
	}
	return renderMap(stdout, stats)
}

func depotBehindCount(store *corpus.Store, cfg model.Config, depotAddr string) (int, error) {
	drv, err := depotDriverForConfig(cfg, depotAddr)
	if err != nil {
		return 0, err
	}
	return depotBehindCountFromDriver(store, drv)
}

func depotBehindCountFromDriver(store *corpus.Store, drv depot.Driver) (int, error) {
	refs, err := drv.List(context.Background())
	if err != nil {
		return 0, err
	}
	ingested, err := corpus.BundleSHAs(store.DB)
	if err != nil {
		return 0, err
	}
	return depotBehindFromRefs(refs, ingested), nil
}

func depotBehindFromRefs(refs []depot.BundleRef, ingested map[string]bool) int {
	behind := 0
	seen := map[string]bool{}
	for _, ref := range refs {
		sha := ref.BundleSHA256
		if sha == "" || seen[sha] || ingested[sha] {
			continue
		}
		seen[sha] = true
		behind++
	}
	return behind
}

func statusNext(stats map[string]any) []string {
	sessions, _ := stats["sessions"].(int)
	if sessions == 0 {
		return []string{"aha refresh", "aha ingest <bundle.tar.zst>", "aha doctor"}
	}
	conflicts, _ := stats["conflicts"].(int)
	next := []string{"aha search <query>", "aha read <ref>"}
	if conflicts > 0 {
		next = append(next, "aha conflicts")
	}
	return next
}

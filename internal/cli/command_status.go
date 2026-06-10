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
		report, err := depotBehindReportForConfig(store, cfg, *depotAddr)
		if err != nil {
			return err
		}
		stats["depot_behind_bundles"] = report.Behind
		stats["depot_catalog_refs_listed"] = report.CatalogRefs
		stats["depot_unique_refs_listed"] = report.UniqueCatalogRefs
		stats["depot_fetches"] = 0
	}
	stats["next"] = statusNext(stats)
	if *jsonOut {
		return writeJSON(stdout, stats)
	}
	return renderMap(stdout, stats)
}

type depotBehindReport struct {
	Behind            int
	CatalogRefs       int
	UniqueCatalogRefs int
}

func depotBehindCount(store *corpus.Store, cfg model.Config, depotAddr string) (int, error) {
	report, err := depotBehindReportForConfig(store, cfg, depotAddr)
	return report.Behind, err
}

func depotBehindReportForConfig(store *corpus.Store, cfg model.Config, depotAddr string) (depotBehindReport, error) {
	drv, err := depotDriverForConfig(cfg, depotAddr)
	if err != nil {
		return depotBehindReport{}, err
	}
	return depotBehindReportFromDriver(store, drv)
}

func depotBehindCountFromDriver(store *corpus.Store, drv depot.Driver) (int, error) {
	report, err := depotBehindReportFromDriver(store, drv)
	return report.Behind, err
}

func depotBehindReportFromDriver(store *corpus.Store, drv depot.Driver) (depotBehindReport, error) {
	refs, err := drv.List(context.Background())
	if err != nil {
		return depotBehindReport{}, err
	}
	ingested, err := corpus.SnapshotManifestSHAs(store.DB)
	if err != nil {
		return depotBehindReport{}, err
	}
	return depotBehindReport{Behind: depotBehindFromRefs(refs, ingested), CatalogRefs: len(refs), UniqueCatalogRefs: uniqueDepotRefs(refs)}, nil
}

func uniqueDepotRefs(refs []depot.BundleRef) int {
	seen := map[string]bool{}
	for _, ref := range refs {
		if ref.BundleSHA256 == "" || seen[ref.BundleSHA256] {
			continue
		}
		seen[ref.BundleSHA256] = true
	}
	return len(seen)
}

// depotBehindFromRefs counts catalog refs whose snapshot identity the
// corpus has not ingested (metadata-only; zero fetches).
func depotBehindFromRefs(refs []depot.BundleRef, ingested map[string]bool) int {
	behind := 0
	seen := map[string]bool{}
	for _, ref := range refs {
		sha := ref.BundleSHA256
		if sha == "" || seen[sha] {
			continue
		}
		seen[sha] = true
		if ref.ManifestSHA256 != "" && ingested[ref.ManifestSHA256] {
			continue
		}
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

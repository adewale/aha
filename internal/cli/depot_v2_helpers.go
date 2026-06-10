package cli

import (
	"context"
	"fmt"
	"io"
	"path/filepath"

	"github.com/adewale/aha/internal/adapters"
	"github.com/adewale/aha/internal/archive"
	ahaclock "github.com/adewale/aha/internal/clock"
	"github.com/adewale/aha/internal/corpus"
	"github.com/adewale/aha/internal/depot"
	"github.com/adewale/aha/internal/model"
	"github.com/adewale/aha/internal/paths"
)

// depotV2ForConfig opens the configured (or overridden) depot in the v2
// content-addressed layout.
func depotV2ForConfig(cfg model.Config, override string) (*depot.V2, error) {
	addr := depot.AddressFromConfig(cfg.Depot)
	if override != "" {
		parsed, err := depot.ParseAddress(override)
		if err != nil {
			return nil, err
		}
		addr = parsed
	}
	switch addr.Type {
	case "local":
		return depot.NewLocalV2(addr.Location)
	case "r2":
		rc, err := depot.ResolveR2Config(cfg.Depot.R2)
		if err != nil {
			return nil, err
		}
		return depot.NewV2FromR2(depot.NewR2(addr.Location, rc)), nil
	default:
		return nil, fmt.Errorf("unsupported depot type %q", addr.Type)
	}
}

// captureCachePath places the advisory scan cache next to the corpus.
func captureCachePath(cfg model.Config) (string, error) {
	dir := cfg.CorpusDir
	if dir == "" {
		dir = "~/.aha"
	}
	expanded, err := paths.Expand(dir)
	if err != nil {
		return "", err
	}
	return filepath.Join(expanded, "capture-cache.json"), nil
}

// pushSnapshotV2 captures the configured sources (scan cache permitting)
// and publishes the state to the depot via the typestate push flow.
func pushSnapshotV2(req snapshotRequest) (depot.PushResult, error) {
	ctx := context.Background()
	v2, err := depotV2ForConfig(req.Config, req.DepotOverride)
	if err != nil {
		return depot.PushResult{}, err
	}
	if err := v2.Init(ctx); err != nil {
		return depot.PushResult{}, err
	}
	var cache *archive.CaptureCache
	if !req.Force {
		cachePath, err := captureCachePath(req.Config)
		if err != nil {
			return depot.PushResult{}, err
		}
		cache = archive.LoadCaptureCache(cachePath, ahaclock.RealClock{})
	}
	sc, err := archive.CaptureState(ctx, req.Config, adapters.Builtins(), archive.StateOptions{CapturedAt: req.CapturedAt, SessionFilters: req.SessionFilters, MaxSessions: req.MaxSessions, Clock: ahaclock.RealClock{}, Cache: cache})
	if err != nil {
		return depot.PushResult{}, err
	}
	defer sc.Close()
	res, err := depot.PushV2(ctx, v2, sc.Manifest, sc)
	if err != nil {
		return res, err
	}
	if cache != nil {
		// Best-effort: the cache is advisory; failing to persist it only
		// costs the next run a re-hash.
		_ = cache.Save()
	}
	return res, nil
}

// pullFromDepotV2 anti-entropies the corpus against every machine's
// latest snapshot: pointer + manifest GETs to find what's new, then only
// unknown blobs are fetched (inside IngestSnapshot).
func pullFromDepotV2(stdout io.Writer, ing corpus.Ingestor, v2 *depot.V2, jsonOut bool) ([]map[string]any, error) {
	ctx := context.Background()
	machines, err := v2.Machines(ctx)
	if err != nil {
		return nil, err
	}
	ingested, err := corpus.SnapshotManifestSHAs(ing.Store.DB)
	if err != nil {
		return nil, err
	}
	var reports []map[string]any
	for _, machine := range machines {
		sha, ok, err := v2.Latest(ctx, machine)
		if err != nil {
			return nil, err
		}
		if !ok || ingested[sha.String()] {
			continue
		}
		manifest, err := v2.Manifest(ctx, machine, sha)
		if err != nil {
			return nil, err
		}
		rep, err := ing.IngestSnapshot(manifest, func(key model.BlobKey) (io.ReadCloser, error) {
			return v2.OpenBlob(ctx, key)
		})
		if err != nil {
			return nil, err
		}
		item := map[string]any{"machine": machine, "manifest_sha256": sha.String(), "sessions": rep.Sessions, "entries": rep.Entries, "messages": rep.Messages, "images": rep.Images, "artifacts": rep.Artifacts, "duplicate": rep.Duplicate}
		if jsonOut {
			reports = append(reports, item)
		} else {
			fmt.Fprintf(stdout, "%s@%s: sessions=%d entries=%d messages=%d images=%d artifacts=%d duplicate=%v\n", machine, sha.String()[:12], rep.Sessions, rep.Entries, rep.Messages, rep.Images, rep.Artifacts, rep.Duplicate)
		}
	}
	return reports, nil
}

// depotBehindV2Report compares every machine's latest snapshot identity
// against the corpus — metadata-only: one index GET plus one pointer GET
// per machine, zero manifest or blob fetches.
type depotBehindV2Report struct {
	Behind   int
	Machines int
}

func depotBehindV2(store *corpus.Store, cfg model.Config, depotAddr string) (depotBehindV2Report, error) {
	v2, err := depotV2ForConfig(cfg, depotAddr)
	if err != nil {
		return depotBehindV2Report{}, err
	}
	return depotBehindV2FromDepot(store, v2)
}

func depotBehindV2FromDepot(store *corpus.Store, v2 *depot.V2) (depotBehindV2Report, error) {
	ctx := context.Background()
	machines, err := v2.Machines(ctx)
	if err != nil {
		return depotBehindV2Report{}, err
	}
	ingested, err := corpus.SnapshotManifestSHAs(store.DB)
	if err != nil {
		return depotBehindV2Report{}, err
	}
	report := depotBehindV2Report{Machines: len(machines)}
	for _, machine := range machines {
		sha, ok, err := v2.Latest(ctx, machine)
		if err != nil {
			return report, err
		}
		if ok && !ingested[sha.String()] {
			report.Behind++
		}
	}
	return report, nil
}

package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/adewale/aha/internal/adapters"
	"github.com/adewale/aha/internal/archive"
	ahaclock "github.com/adewale/aha/internal/clock"
	"github.com/adewale/aha/internal/corpus"
	"github.com/adewale/aha/internal/depot"
	"github.com/adewale/aha/internal/model"
	"github.com/adewale/aha/internal/paths"
	ahaprogress "github.com/adewale/aha/internal/progress"
)

// depotV2ForConfig opens the configured (or overridden) depot in the v2
// content-addressed layout.
func depotV2ForConfig(cfg model.Config, override string) (*depot.V2, error) {
	var addr depot.Address
	var err error
	if override != "" {
		addr, err = depot.ParseExplicitAddress(override)
	} else {
		addr, err = depot.AddressFromConfig(cfg.Archive)
	}
	if err != nil {
		return nil, err
	}
	switch addr.Type {
	case "local":
		return depot.NewLocalV2(addr.Location)
	case "r2":
		rc, err := depot.ResolveR2Config(cfg.Archive.R2)
		if err != nil {
			return nil, err
		}
		bucket, err := depot.ParseR2Bucket(addr.Location)
		if err != nil {
			return nil, err
		}
		r2, err := depot.NewR2(bucket, rc)
		if err != nil {
			return nil, err
		}
		return depot.NewV2FromR2(r2), nil
	default:
		return nil, fmt.Errorf("unsupported Archive type %q", addr.Type)
	}
}

// captureCachePath keeps upload's advisory scan cache outside the Workspace,
// which archive upload is forbidden to open or mutate.
func captureCachePath(cfg model.Config) (string, error) {
	cacheRoot, err := os.UserCacheDir()
	if err != nil || cacheRoot == "" {
		cacheRoot, err = paths.Expand("~/.cache")
		if err != nil {
			return "", err
		}
	}
	return filepath.Join(cacheRoot, "aha", "capture-"+safeName(cfg.MachineID)+".json"), nil
}

// pushSnapshotV2 captures the configured sources (scan cache permitting)
// and publishes the state to the depot via the typestate push flow.
func pushSnapshotV2(req snapshotRequest, writer depot.PreparedUpload) (depot.PushResult, error) {
	ctx := req.Context
	if ctx == nil {
		ctx = context.Background()
	}
	var cache *archive.CaptureCache
	if !req.Force {
		cachePath, err := captureCachePath(req.Config)
		if err != nil {
			return depot.PushResult{}, err
		}
		cache = archive.LoadCaptureCache(cachePath, ahaclock.RealClock{})
	}
	sc, err := archive.CaptureState(ctx, req.Config, adapters.Builtins(), archive.StateOptions{CapturedAt: req.CapturedAt, SessionFilters: req.SessionFilters, MaxSessions: req.MaxSessions, Clock: ahaclock.RealClock{}, Cache: cache, Progress: req.Progress})
	if err != nil {
		return depot.PushResult{}, err
	}
	defer sc.Close()
	res, err := writer.Push(ctx, sc.Manifest, sc, depot.PushOptions{Progress: req.Progress})
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
func pullFromDepotV2(ctx context.Context, stdout io.Writer, ing corpus.Ingestor, plan *depot.MaterialisationPlan, jsonOut bool, tracker *ahaprogress.Tracker) ([]map[string]any, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	summary, err := plan.Summary()
	if err != nil {
		return nil, err
	}
	binding, err := plan.ArchiveBinding()
	if err != nil {
		return nil, err
	}
	if err := corpus.BindWorkspaceStore(ing.Store, binding); err != nil {
		return nil, err
	}
	latestVector, err := plan.LatestVector()
	if err != nil {
		return nil, err
	}
	machineTotal := ahaprogress.KnownTotal(uint64(summary.Machines))
	tracker.Start(ahaprogress.PhasePull, machineTotal, ahaprogress.UnitMachines)
	processedMachines := uint64(0)
	ingestedSessions := uint64(0)
	ingestStarted := false
	success := false
	defer func() {
		if success {
			return
		}
		if ctx.Err() != nil {
			tracker.Cancel(ahaprogress.PhasePull, processedMachines, machineTotal, ahaprogress.UnitMachines)
			if ingestStarted {
				tracker.Cancel(ahaprogress.PhaseIngest, ingestedSessions, ahaprogress.UnknownTotal(), ahaprogress.UnitSessions)
			}
		} else {
			tracker.Fail(ahaprogress.PhasePull, processedMachines, machineTotal, ahaprogress.UnitMachines)
			if ingestStarted {
				tracker.Fail(ahaprogress.PhaseIngest, ingestedSessions, ahaprogress.UnknownTotal(), ahaprogress.UnitSessions)
			}
		}
	}()
	var reports []map[string]any
	if err := plan.ForEachManifest(ctx, func(machine string, prepared model.PreparedSnapshot, open func(model.BlobKey) (io.ReadCloser, error)) error {
		sha := prepared.SHA()
		if !ingestStarted {
			tracker.Start(ahaprogress.PhaseIngest, ahaprogress.UnknownTotal(), ahaprogress.UnitSessions)
			ingestStarted = true
		}
		rep, err := ing.IngestPreparedSnapshot(prepared, open)
		if err != nil {
			return err
		}
		ingestedSessions += uint64(rep.Sessions)
		tracker.Advance(ahaprogress.PhaseIngest, ingestedSessions, ahaprogress.UnknownTotal(), ahaprogress.UnitSessions)
		item := map[string]any{"machine": machine, "manifest_sha256": sha.String(), "sessions": rep.Sessions, "entries": rep.Entries, "messages": rep.Messages, "images": rep.Images, "artifacts": rep.Artifacts, "duplicate": rep.Duplicate}
		if jsonOut {
			reports = append(reports, item)
		} else {
			fmt.Fprintf(stdout, "%s@%s: sessions=%d entries=%d messages=%d images=%d artifacts=%d duplicate=%v\n", machine, sha.String()[:12], rep.Sessions, rep.Entries, rep.Messages, rep.Images, rep.Artifacts, rep.Duplicate)
		}
		processedMachines++
		tracker.Advance(ahaprogress.PhasePull, processedMachines, machineTotal, ahaprogress.UnitMachines)
		return nil
	}); err != nil {
		return nil, err
	}
	processedMachines = uint64(summary.Machines)
	if err := corpus.RecordMaterialisedVector(ing.Store.DB, latestVector); err != nil {
		return nil, err
	}
	tracker.Complete(ahaprogress.PhasePull, processedMachines, machineTotal, ahaprogress.UnitMachines)
	if ingestStarted {
		tracker.Complete(ahaprogress.PhaseIngest, ingestedSessions, ahaprogress.UnknownTotal(), ahaprogress.UnitSessions)
	}
	success = true
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
	reader, err := v2.PreparePull(ctx)
	if err != nil {
		return depotBehindV2Report{}, err
	}
	plan, err := reader.PlanDownload(ctx)
	if err != nil {
		return depotBehindV2Report{}, err
	}
	machines, err := plan.Machines()
	if err != nil {
		return depotBehindV2Report{}, err
	}
	report := depotBehindV2Report{Machines: len(machines)}
	for _, machine := range machines {
		sha, ok, err := plan.Latest(machine)
		if err != nil {
			return report, err
		}
		if !ok {
			continue
		}
		known, err := corpus.HasSnapshot(store.DB, sha.String())
		if err != nil {
			return report, err
		}
		if !known {
			report.Behind++
		}
	}
	return report, nil
}

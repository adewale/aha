package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/adewale/aha/internal/adapters"
	"github.com/adewale/aha/internal/archive"
	ahaclock "github.com/adewale/aha/internal/clock"
	"github.com/adewale/aha/internal/config"
	"github.com/adewale/aha/internal/corpus"
	"github.com/adewale/aha/internal/depot"
	"github.com/adewale/aha/internal/model"
	"github.com/adewale/aha/internal/safety"
)

type archiveLatestView struct {
	Machine        string `json:"machine"`
	ManifestSHA256 string `json:"manifest_sha256"`
	CapturedAt     string `json:"captured_at"`
	Files          int    `json:"files"`
}

type archiveStatusView struct {
	Schema                  string              `json:"schema"`
	State                   model.ArchiveState  `json:"state"`
	Kind                    string              `json:"kind,omitempty"`
	Address                 string              `json:"address,omitempty"`
	Identity                string              `json:"identity,omitempty"`
	Selected                bool                `json:"selected"`
	Machines                int                 `json:"machines"`
	LatestSnapshots         int                 `json:"latest_snapshots"`
	HistoricalManifests     int                 `json:"historical_manifests,omitempty"`
	HistoricalCountComplete bool                `json:"historical_count_complete"`
	Latest                  []archiveLatestView `json:"latest,omitempty"`
	Problems                []string            `json:"problems,omitempty"`
	NextAction              *nextAction         `json:"next_action"`
}

func cmdArchive(args []string, stdout, stderr io.Writer) error {
	return runArchiveContext(context.Background(), args, stdout, stderr)
}

func runArchiveContext(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("archive requires subcommand: init, set-default, status, upload, download, verify")
	}
	if args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprintln(stdout, "Usage: aha archive <init|set-default|status|upload|download|verify> [ARCHIVE]")
		return nil
	}
	sub := args[0]
	fs := flag.NewFlagSet("archive "+sub, flag.ContinueOnError)
	fs.SetOutput(flagOutput(args, stderr))
	configPath := fs.String("config", "", "config path")
	workspacePath := fs.String("workspace", "", "Workspace directory")
	jsonOut := fs.Bool("json", false, "JSON output")
	deep := fs.Bool("deep", false, "download and hash all durable content")
	dryRun := fs.Bool("dry-run", false, "inspect and plan without mutation")
	force := fs.Bool("force", false, "bypass the upload capture cache")
	progressSetting := fs.String("progress", "auto", "progress mode: auto, off, plain, tty, or json")
	if err := fs.Parse(interspersedArchiveFlagArgs(args[1:])); err != nil {
		return err
	}
	if fs.NArg() > 1 {
		return errors.New("archive accepts at most one Archive address")
	}
	address := ""
	if fs.NArg() == 1 {
		address = fs.Arg(0)
	}
	cfg, cfgErr := config.Load(*configPath)
	if cfgErr != nil {
		if sub == "status" {
			return renderArchiveStatus(stdout, archiveStatusView{Schema: "aha.archive.status.v2", State: model.ArchiveInvalidConfiguration, NextAction: archiveNext(model.ArchiveInvalidConfiguration)}, *jsonOut)
		}
		return cfgErr
	}
	progress, err := newProgressSession(stderr, *progressSetting, *jsonOut)
	if err != nil {
		return err
	}
	defer progress.Close()

	switch sub {
	case "status":
		view := inspectArchive(ctx, cfg, address)
		return renderArchiveStatus(stdout, view, *jsonOut)
	case "init":
		before := inspectArchive(ctx, cfg, address)
		if before.State != model.ArchiveUninitialised {
			if archiveHasProblem(before, "unsupported v1 Archive layout") {
				return &depot.LegacyArchiveError{}
			}
			if before.State == model.ArchiveEmpty || before.State == model.ArchivePopulated {
				return errors.New("Archive is already initialised")
			}
			return fmt.Errorf("Archive cannot be initialised while state is %s", before.State)
		}
		v2, err := depotV2ForConfig(cfg, address)
		if err != nil {
			return err
		}
		if v2.Address().Type == "local" {
			if err := safety.ValidateWriteOutsideSources(cfg, v2.Address().Location, "Archive"); err != nil {
				return err
			}
		}
		if *dryRun {
			return writeArchiveMutation(stdout, *jsonOut, map[string]any{"state": model.ArchiveUninitialised, "planned_state": model.ArchiveEmpty, "dry_run": true})
		}
		if err := v2.Init(ctx); err != nil {
			return err
		}
		return writeArchiveMutation(stdout, *jsonOut, map[string]any{"state": model.ArchiveEmpty, "created": true, "address": v2.Address().String()})
	case "set-default":
		if address == "" {
			return errors.New("archive set-default requires an explicit r2: or local: address")
		}
		view := inspectArchive(ctx, cfg, address)
		if view.State != model.ArchiveEmpty && view.State != model.ArchivePopulated {
			return fmt.Errorf("Archive is %s; run %s", view.State, archiveNext(view.State).String())
		}
		v2, err := depotV2ForConfig(cfg, address)
		if err != nil {
			return err
		}
		cfg.Archive = depot.ConfigFromAddress(v2.Address())
		if err := captureDepotR2Config(&cfg); err != nil {
			return err
		}
		if *dryRun {
			return writeArchiveMutation(stdout, *jsonOut, map[string]any{"state": view.State, "selected": false, "dry_run": true})
		}
		path, err := config.Write(*configPath, cfg, "// aha config (JSONC)\n// Updated by `aha archive set-default`.\n")
		if err != nil {
			return err
		}
		return writeArchiveMutation(stdout, *jsonOut, map[string]any{"state": view.State, "selected": true, "config": path, "address": v2.Address().String()})
	case "upload":
		if !cfg.AcknowledgedRawHistory {
			return errPrivacyAcknowledgement
		}
		v2, err := depotV2ForConfig(cfg, address)
		if err != nil {
			return err
		}
		writer, err := v2.PrepareUpload(ctx)
		if err != nil {
			return err
		}
		req := snapshotRequest{Context: ctx, Config: cfg, DepotOverride: address, JSON: *jsonOut, Progress: progress.Tracker, ProgressSetting: *progressSetting, Force: *force}
		if *dryRun {
			sc, err := archive.CaptureState(ctx, cfg, adapters.Builtins(), archive.StateOptions{Clock: ahaclock.RealClock{}, Progress: progress.Tracker})
			if err != nil {
				return err
			}
			defer sc.Close()
			plan, err := writer.PlanUpload(ctx, sc.Manifest, sc)
			if err != nil {
				return err
			}
			summary, err := plan.Summary()
			if err != nil {
				return err
			}
			return writeArchiveMutation(stdout, *jsonOut, map[string]any{"state": "planned", "dry_run": true, "machine": cfg.MachineID, "files": summary.Files, "blobs_upload": summary.BlobsUpload, "blobs_existing": summary.BlobsExisting, "blobs_carried": summary.BlobsCarried, "address": v2.Address().String()})
		}
		result, err := pushSnapshotV2(req)
		if err != nil {
			return err
		}
		out := pushResultJSON(result)
		out["state"] = model.ArchivePopulated
		out["address"] = v2.Address().String()
		return writeArchiveMutation(stdout, *jsonOut, out)
	case "download":
		if *workspacePath != "" {
			cfg.WorkspaceDir = *workspacePath
		}
		v2, err := depotV2ForConfig(cfg, address)
		if err != nil {
			return err
		}
		reader, err := v2.PreparePull(ctx)
		if err != nil {
			return err
		}
		plan, err := reader.PlanDownload(ctx)
		if err != nil {
			return err
		}
		binding, err := plan.ArchiveBinding()
		if err != nil {
			return err
		}
		vector, err := plan.LatestVector()
		if err != nil {
			return err
		}
		destination, err := prepareWritableCorpus(cfg)
		if err != nil {
			return err
		}
		state, err := corpus.InspectWorkspaceState(cfg.WorkspaceDir, binding, vector)
		if err != nil {
			return err
		}
		if state == model.WorkspaceArchiveMismatch {
			return corpus.ErrArchiveMismatch
		}
		if state == model.WorkspaceDamaged {
			return errors.New("Workspace is damaged; run `aha workspace repair --backup`")
		}
		if state == model.WorkspaceCurrent {
			return writeArchiveMutation(stdout, *jsonOut, map[string]any{"state": state, "planned_state": model.WorkspaceCurrent, "dry_run": *dryRun, "no_op": true, "machines": len(vector), "latest_snapshots": len(vector), "unknown_blobs": 0, "unknown_bytes": 0, "historical_manifests_excluded": true})
		}
		blobLookup, err := corpus.OpenWorkspaceBlobLookup(cfg.WorkspaceDir)
		if err != nil {
			return err
		}
		materialised, err := blobLookup.MaterialisedVector()
		if err != nil {
			_ = blobLookup.Close()
			return err
		}
		summary, err := plan.SummaryDelta(ctx, materialised, blobLookup.Has)
		closeErr := blobLookup.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		if *dryRun {
			return writeArchiveMutation(stdout, *jsonOut, map[string]any{"state": state, "planned_state": model.WorkspaceCurrent, "dry_run": true, "no_op": false, "machines": summary.Machines, "latest_snapshots": summary.LatestSnapshots, "unknown_blobs": summary.UnknownBlobs, "unknown_bytes": summary.UnknownBytes, "historical_manifests_excluded": true})
		}
		store, err := openPreparedCorpus(destination)
		if err != nil {
			return err
		}
		defer store.Close()
		ing, err := ingestorForConfig(store, cfg)
		if err != nil {
			return err
		}
		ing.Context = ctx
		reports, err := pullFromDepotV2(ctx, stdout, ing, plan, *jsonOut, progress.Tracker)
		if err != nil {
			return err
		}
		return writeArchiveMutation(stdout, *jsonOut, map[string]any{"state": model.WorkspaceCurrent, "machines": len(vector), "reports": reports, "historical_manifests_excluded": true, "no_op": len(reports) == 0})
	case "verify":
		v2, err := depotV2ForConfig(cfg, address)
		if err != nil {
			return err
		}
		report, err := v2.VerifyWithOptions(ctx, *deep, depot.VerifyOptions{Progress: progress.Tracker})
		if err != nil {
			return err
		}
		if *jsonOut {
			return writeJSON(stdout, report)
		}
		return renderMap(stdout, map[string]any{"machines": report.Machines, "manifests": report.Manifests, "deep": report.Deep, "problems": len(report.Problems), "bytes_downloaded": report.BytesDownloaded})
	default:
		return fmt.Errorf("unknown archive subcommand %q", sub)
	}
}

func inspectArchive(ctx context.Context, cfg model.Config, address string) archiveStatusView {
	view := archiveStatusView{Schema: "aha.archive.status.v2"}
	v2, err := depotV2ForConfig(cfg, address)
	if err != nil {
		var explicit *depot.ExplicitAddressError
		var r2Config *depot.R2ConfigError
		if errors.As(err, &explicit) {
			view.State = model.ArchiveInvalidAddress
		} else if errors.As(err, &r2Config) {
			view.State = model.ArchiveInvalidConfiguration
		} else {
			view.State = model.ArchiveInvalidConfiguration
		}
		view.NextAction = archiveNext(view.State)
		return view
	}
	view.Kind = v2.Address().Type
	view.Address = v2.Address().String()
	if configured, err := depotV2ForConfig(cfg, ""); err == nil {
		view.Selected = configured.Address() == v2.Address()
	}
	report, err := v2.InspectMetadata(ctx)
	if err != nil {
		view.State = model.ArchiveUnreachable
		view.NextAction = archiveNext(view.State)
		return view
	}
	view.Machines = report.Machines
	view.LatestSnapshots = len(report.Latest)
	view.Problems = report.Problems
	if report.Binding.Valid() {
		view.Identity = report.Binding.Identity()
	}
	for _, latest := range report.Latest {
		view.Latest = append(view.Latest, archiveLatestView{Machine: latest.Machine, ManifestSHA256: latest.ManifestSHA256.String(), CapturedAt: latest.Manifest.CapturedAt, Files: len(latest.Manifest.Files)})
	}
	switch {
	case len(report.Problems) > 0:
		view.State = model.ArchiveDamaged
	case !report.Initialised:
		view.State = model.ArchiveUninitialised
	case report.Machines == 0:
		view.State = model.ArchiveEmpty
	default:
		view.State = model.ArchivePopulated
	}
	view.NextAction = archiveNext(view.State)
	if archiveHasProblem(view, "unsupported v1 Archive layout") {
		view.NextAction = &nextAction{Command: "aha", Args: []string{"archive", "init", "local:~/.aha/archive-v2"}}
	}
	return view
}

func archiveHasProblem(view archiveStatusView, problem string) bool {
	for _, candidate := range view.Problems {
		if candidate == problem {
			return true
		}
	}
	return false
}

func archiveNext(state model.ArchiveState) *nextAction {
	var args []string
	switch state {
	case model.ArchiveInvalidAddress, model.ArchiveInvalidConfiguration, model.ArchiveUnreachable:
		args = []string{"archive", "status"}
	case model.ArchiveUninitialised:
		args = []string{"archive", "init"}
	case model.ArchiveEmpty:
		args = []string{"archive", "upload"}
	case model.ArchiveDamaged:
		args = []string{"archive", "verify", "--deep"}
	default:
		return nil
	}
	return &nextAction{Command: "aha", Args: args}
}

func renderArchiveStatus(stdout io.Writer, view archiveStatusView, jsonOut bool) error {
	if jsonOut {
		return writeJSON(stdout, view)
	}
	fields := map[string]any{"state": view.State, "kind": view.Kind, "address": view.Address, "selected": view.Selected, "machines": view.Machines, "latest_snapshots": view.LatestSnapshots}
	if view.NextAction != nil {
		fields["next"] = view.NextAction.String()
	}
	return renderMap(stdout, fields)
}

func writeArchiveMutation(stdout io.Writer, jsonOut bool, value map[string]any) error {
	if jsonOut {
		return writeJSON(stdout, value)
	}
	return renderMap(stdout, value)
}

func interspersedArchiveFlagArgs(args []string) []string {
	valueFlags := map[string]bool{"--config": true, "-config": true, "--workspace": true, "-workspace": true, "--progress": true, "-progress": true}
	var flags, positionals []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		name := strings.SplitN(arg, "=", 2)[0]
		if strings.HasPrefix(arg, "-") {
			flags = append(flags, arg)
			if valueFlags[name] && !strings.Contains(arg, "=") && i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
			continue
		}
		positionals = append(positionals, arg)
	}
	return append(flags, positionals...)
}

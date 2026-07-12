package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/adewale/aha/internal/config"
	"github.com/adewale/aha/internal/corpus"
	"github.com/adewale/aha/internal/model"
	ahaprogress "github.com/adewale/aha/internal/progress"
	"github.com/adewale/aha/internal/safety"
)

type workspaceStatusView struct {
	Schema          string                 `json:"schema"`
	State           model.WorkspaceState   `json:"state"`
	Path            string                 `json:"path"`
	ArchiveMatches  bool                   `json:"archive_matches"`
	MachinesCurrent int                    `json:"machines_current"`
	MachinesBehind  int                    `json:"machines_behind"`
	Snapshots       int                    `json:"snapshots"`
	Sessions        int                    `json:"sessions"`
	Entries         int                    `json:"entries"`
	Messages        int                    `json:"messages"`
	FTSMessages     int                    `json:"fts_messages"`
	Problems        []corpus.VerifyProblem `json:"problems,omitempty"`
	NextAction      *nextAction            `json:"next_action"`
}

func cmdWorkspace(args []string, stdout, stderr io.Writer) error {
	return runWorkspaceContext(context.Background(), args, stdout, stderr)
}

func runWorkspaceContext(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("workspace requires subcommand: set-default, status, verify, repair, conflicts")
	}
	if args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprintln(stdout, "Usage: aha workspace <set-default|status|verify|repair|conflicts> [PATH]")
		return nil
	}
	sub := args[0]
	fs := flag.NewFlagSet("workspace "+sub, flag.ContinueOnError)
	fs.SetOutput(flagOutput(args, stderr))
	configPath := fs.String("config", "", "config path")
	jsonOut := fs.Bool("json", false, "JSON output")
	repairFTS := fs.Bool("repair-fts", false, "repair derived FTS rows")
	backup := fs.Bool("backup", false, "preserve the current Workspace as a backup")
	dryRun := fs.Bool("dry-run", false, "inspect and plan without mutation")
	progressSetting := fs.String("progress", "auto", "progress mode: auto, off, plain, tty, or json")
	if err := fs.Parse(interspersedArchiveFlagArgs(args[1:])); err != nil {
		return err
	}
	if fs.NArg() > 1 {
		return errors.New("workspace accepts at most one path")
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if fs.NArg() == 1 {
		cfg.WorkspaceDir = fs.Arg(0)
	}
	progress, err := newProgressSession(stderr, *progressSetting, *jsonOut)
	if err != nil {
		return err
	}
	defer progress.Close()

	switch sub {
	case "set-default":
		if fs.NArg() != 1 {
			return errors.New("workspace set-default requires PATH")
		}
		if _, err := safety.PrepareWorkspaceDestination(cfg, cfg.WorkspaceDir); err != nil {
			return err
		}
		if *dryRun {
			return writeWorkspaceValue(stdout, *jsonOut, map[string]any{"state": "planned", "path": cfg.WorkspaceDir, "dry_run": true})
		}
		path, err := config.Write(*configPath, cfg, "// aha config (JSONC)\n// Updated by `aha workspace set-default`.\n")
		if err != nil {
			return err
		}
		return writeWorkspaceValue(stdout, *jsonOut, map[string]any{"selected": true, "path": cfg.WorkspaceDir, "config": path})
	case "status":
		view := inspectWorkspace(ctx, cfg)
		if *jsonOut {
			return writeJSON(stdout, view)
		}
		fields := map[string]any{"state": view.State, "path": view.Path, "archive_matches": view.ArchiveMatches, "snapshots": view.Snapshots, "sessions": view.Sessions}
		if view.NextAction != nil {
			fields["next"] = view.NextAction.String()
		}
		return renderMap(stdout, fields)
	case "verify":
		verifyTotal := uint64(1)
		if *repairFTS {
			verifyTotal = 2
		}
		total := ahaprogress.KnownTotal(verifyTotal)
		progress.Tracker.Start(ahaprogress.PhaseVerify, total, ahaprogress.UnitSteps)
		verifyComplete := false
		defer func() {
			if verifyComplete {
				return
			}
			if ctx.Err() != nil {
				progress.Tracker.Cancel(ahaprogress.PhaseVerify, 0, total, ahaprogress.UnitSteps)
			} else {
				progress.Tracker.Fail(ahaprogress.PhaseVerify, 0, total, ahaprogress.UnitSteps)
			}
		}()
		var store *corpus.Store
		if *repairFTS {
			if err := safety.ValidateWriteOutsideSources(cfg, cfg.WorkspaceDir, "Workspace"); err != nil {
				return err
			}
			store, err = corpus.OpenExisting(cfg.WorkspaceDir)
		} else {
			store, err = openCorpusForCommand(cfg, false)
		}
		if err != nil {
			return err
		}
		defer store.Close()
		repaired := false
		if *repairFTS {
			if _, err := corpus.ReconcileFTSWithReportContext(ctx, store); err != nil {
				return err
			}
			repaired = true
			progress.Tracker.Advance(ahaprogress.PhaseVerify, 1, total, ahaprogress.UnitSteps)
		}
		report, err := corpus.VerifyContext(ctx, store)
		if err != nil {
			return err
		}
		progress.Tracker.Complete(ahaprogress.PhaseVerify, verifyTotal, total, ahaprogress.UnitSteps)
		verifyComplete = true
		if *jsonOut {
			return writeJSON(stdout, struct {
				Report      corpus.VerifyReport `json:"report"`
				RepairedFTS bool                `json:"repaired_fts"`
			}{Report: report, RepairedFTS: repaired})
		}
		return renderMap(stdout, map[string]any{"root": report.Root, "problems": len(report.Problems), "repaired_fts": repaired})
	case "conflicts":
		store, err := openCorpusForCommand(cfg, false)
		if err != nil {
			return err
		}
		defer store.Close()
		items, err := corpus.Conflicts(store.DB)
		if err != nil {
			return err
		}
		if *jsonOut {
			return writeJSON(stdout, items)
		}
		for _, item := range items {
			fmt.Fprintf(stdout, "%d\t%s\t%s\t%s\t%s\n", item.ID, item.SessionKey, item.EntryID, item.First, item.Second)
		}
		return nil
	case "repair":
		if !*backup {
			return errors.New("workspace repair requires --backup")
		}
		v2, err := depotV2ForConfig(cfg, "")
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
		state, err := corpus.InspectWorkspaceState(cfg.WorkspaceDir, binding, vector)
		if err != nil {
			return err
		}
		if state == model.WorkspaceArchiveMismatch {
			return corpus.ErrArchiveMismatch
		}
		if state == model.WorkspaceAbsent {
			return errors.New("Workspace is absent; run `aha archive download` first")
		}
		if state != model.WorkspaceDamaged {
			return fmt.Errorf("Workspace repair requires damaged state; current state is %s", state)
		}
		if *dryRun {
			return writeWorkspaceValue(stdout, *jsonOut, map[string]any{"state": state, "planned_state": model.WorkspaceCurrent, "dry_run": true, "backup": true})
		}
		report, err := corpus.RepairWithBackup(cfg.WorkspaceDir, func(staging string) error {
			stagingCfg := cfg
			stagingCfg.WorkspaceDir = staging
			destination, err := prepareWritableCorpus(stagingCfg)
			if err != nil {
				return err
			}
			store, err := openPreparedCorpus(destination)
			if err != nil {
				return err
			}
			ing, err := ingestorForConfig(store, stagingCfg)
			if err != nil {
				return errors.Join(err, store.Close())
			}
			ing.Context = ctx
			_, pullErr := pullFromDepotV2(ctx, io.Discard, ing, plan, true, progress.Tracker)
			return errors.Join(pullErr, store.Close())
		}, corpus.RebuildOptions{Context: ctx, Progress: progress.Tracker, ValidateStaging: func(path string) error {
			return safety.ValidateWriteOutsideSources(cfg, path, "Workspace repair staging")
		}})
		if err != nil {
			return err
		}
		return writeWorkspaceValue(stdout, *jsonOut, map[string]any{"state": model.WorkspaceCurrent, "root": report.Root, "backup": report.Backup})
	default:
		return fmt.Errorf("unknown workspace subcommand %q", sub)
	}
}

func inspectWorkspace(ctx context.Context, cfg model.Config) workspaceStatusView {
	view := workspaceStatusView{Schema: "aha.workspace.status.v2", Path: cfg.WorkspaceDir}
	if _, err := safety.PrepareWorkspaceDestination(cfg, cfg.WorkspaceDir); err != nil {
		view.State = model.WorkspaceInvalidDestination
		view.NextAction = workspaceNext(view.State)
		return view
	}
	local, err := corpus.OpenExistingReadOnly(cfg.WorkspaceDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			view.State = model.WorkspaceAbsent
		} else {
			view.State = model.WorkspaceDamaged
		}
		view.NextAction = workspaceNext(view.State)
		return view
	}
	_ = local.Close()
	v2, err := depotV2ForConfig(cfg, "")
	if err != nil {
		view.State = model.WorkspaceDamaged
		view.NextAction = workspaceNext(view.State)
		return view
	}
	reader, err := v2.PreparePull(ctx)
	if err != nil {
		view.State = model.WorkspaceDamaged
		view.NextAction = workspaceNext(view.State)
		return view
	}
	plan, err := reader.PlanDownload(ctx)
	if err != nil {
		view.State = model.WorkspaceDamaged
		view.NextAction = workspaceNext(view.State)
		return view
	}
	binding, _ := plan.ArchiveBinding()
	vector, _ := plan.LatestVector()
	view.State, err = corpus.InspectWorkspaceState(cfg.WorkspaceDir, binding, vector)
	if err != nil {
		view.State = model.WorkspaceDamaged
	}
	view.ArchiveMatches = view.State != model.WorkspaceArchiveMismatch
	if view.State != model.WorkspaceAbsent && view.State != model.WorkspaceInvalidDestination {
		if store, openErr := corpus.OpenExistingReadOnly(cfg.WorkspaceDir); openErr == nil {
			if report, verifyErr := corpus.VerifyContext(ctx, store); verifyErr != nil || len(report.Problems) > 0 {
				view.State = model.WorkspaceDamaged
				if verifyErr == nil {
					view.Problems = report.Problems
				}
			}
			if stats, statusErr := corpus.Status(store.DB, store.Root); statusErr == nil {
				view.Snapshots, _ = stats["snapshots"].(int)
				view.Sessions, _ = stats["sessions"].(int)
				view.Entries, _ = stats["entries"].(int)
				view.Messages, _ = stats["messages"].(int)
				view.FTSMessages, _ = stats["fts_messages"].(int)
			}
			materialised, _ := corpus.MaterialisedVector(store.DB)
			for machine, sha := range vector {
				if materialised[machine] == sha {
					view.MachinesCurrent++
				} else {
					view.MachinesBehind++
				}
			}
			_ = store.Close()
		}
	}
	view.NextAction = workspaceNext(view.State)
	return view
}

func workspaceNext(state model.WorkspaceState) *nextAction {
	var args []string
	switch state {
	case model.WorkspaceAbsent, model.WorkspaceBehind:
		args = []string{"archive", "download"}
	case model.WorkspaceDamaged:
		args = []string{"workspace", "repair", "--backup"}
	case model.WorkspaceArchiveMismatch, model.WorkspaceInvalidDestination:
		args = []string{"workspace", "status"}
	default:
		return nil
	}
	return &nextAction{Command: "aha", Args: args}
}

func writeWorkspaceValue(stdout io.Writer, jsonOut bool, value map[string]any) error {
	if jsonOut {
		return writeJSON(stdout, value)
	}
	return renderMap(stdout, value)
}

package cli

import (
	"context"
	"flag"
	"io"

	"github.com/adewale/aha/internal/adapters"
	"github.com/adewale/aha/internal/archive"
	ahaclock "github.com/adewale/aha/internal/clock"
	"github.com/adewale/aha/internal/config"
	"github.com/adewale/aha/internal/depot"
	"github.com/adewale/aha/internal/model"
)

type agentHistoryStatusView struct {
	State   string `json:"state"`
	Machine string `json:"machine"`
	Files   int    `json:"files"`
}

type systemStatusView struct {
	Schema       string                 `json:"schema"`
	Version      string                 `json:"version"`
	AgentHistory agentHistoryStatusView `json:"agent_history"`
	Archive      archiveStatusView      `json:"archive"`
	Workspace    workspaceStatusView    `json:"workspace"`
	SystemState  string                 `json:"system_state"`
	NextAction   *nextAction            `json:"next_action"`
}

func cmdStatus(args []string, stdout, stderr io.Writer) error {
	return runStatusContext(context.Background(), args, stdout, stderr)
}

func runStatusContext(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(flagOutput(args, stderr))
	configPath := fs.String("config", "", "config path")
	archiveAddress := fs.String("archive", "", "Archive address")
	workspacePath := fs.String("workspace", "", "Workspace directory")
	jsonOut := fs.Bool("json", false, "JSON output")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if *workspacePath != "" {
		cfg.WorkspaceDir = *workspacePath
	}
	archiveView := inspectArchive(ctx, cfg, *archiveAddress)
	workspaceView := inspectWorkspaceAgainst(ctx, cfg, *archiveAddress)
	archiveView.NextAction = nil
	workspaceView.NextAction = nil
	agent := inspectAgentHistory(ctx, cfg, *archiveAddress, archiveView.State)
	systemState, next := deriveSystemState(agent.State, archiveView.State, workspaceView.State)
	view := systemStatusView{Schema: "aha.status.v2", Version: model.Version, AgentHistory: agent, Archive: archiveView, Workspace: workspaceView, SystemState: systemState, NextAction: next}
	if *jsonOut {
		return writeJSON(stdout, view)
	}
	fields := map[string]any{"system_state": view.SystemState, "agent_history": view.AgentHistory.State, "archive": view.Archive.State, "workspace": view.Workspace.State}
	if next != nil {
		fields["next"] = next.String()
	}
	return renderMap(stdout, fields)
}

func inspectAgentHistory(ctx context.Context, cfg model.Config, address string, archiveState model.ArchiveState) agentHistoryStatusView {
	view := agentHistoryStatusView{State: "unknown", Machine: cfg.MachineID}
	if archiveState != model.ArchiveEmpty && archiveState != model.ArchivePopulated {
		return view
	}
	sc, err := archive.CaptureState(ctx, cfg, adapters.Builtins(), archive.StateOptions{Clock: ahaclock.RealClock{}})
	if err != nil {
		return view
	}
	defer sc.Close()
	view.Files = len(sc.Manifest.Files)
	if archiveState == model.ArchiveEmpty {
		view.State = "upload_needed"
		return view
	}
	v2, err := depotV2ForConfig(cfg, address)
	if err != nil {
		return view
	}
	sha, ok, err := v2.Latest(ctx, cfg.MachineID)
	if err != nil || !ok {
		view.State = "upload_needed"
		return view
	}
	latest, err := v2.Manifest(ctx, cfg.MachineID, sha)
	if err != nil {
		return view
	}
	localState, err := model.SnapshotStateSHA256(sc.Manifest)
	if err != nil {
		return view
	}
	remoteState, err := model.SnapshotStateSHA256(latest)
	if err != nil {
		return view
	}
	if localState == remoteState {
		view.State = "current"
	} else {
		view.State = "upload_needed"
	}
	return view
}

func inspectWorkspaceAgainst(ctx context.Context, cfg model.Config, address string) workspaceStatusView {
	if address == "" {
		return inspectWorkspace(ctx, cfg)
	}
	original := cfg.Archive
	v2, err := depotV2ForConfig(cfg, address)
	if err != nil {
		return workspaceStatusView{Schema: "aha.workspace.status.v2", State: model.WorkspaceDamaged, Path: cfg.WorkspaceDir}
	}
	cfg.Archive = depot.ConfigFromAddress(v2.Address())
	view := inspectWorkspace(ctx, cfg)
	cfg.Archive = original
	return view
}

func deriveSystemState(agentState string, archiveState model.ArchiveState, workspaceState model.WorkspaceState) (string, *nextAction) {
	if archiveState == model.ArchiveUninitialised {
		return "archive_uninitialised", &nextAction{Command: "aha", Args: []string{"archive", "init"}}
	}
	if archiveState == model.ArchiveDamaged || workspaceState == model.WorkspaceDamaged {
		if archiveState == model.ArchiveDamaged {
			return "damaged", &nextAction{Command: "aha", Args: []string{"archive", "verify", "--deep"}}
		}
		return "damaged", &nextAction{Command: "aha", Args: []string{"workspace", "repair", "--backup"}}
	}
	upload := agentState == "upload_needed"
	download := workspaceState == model.WorkspaceBehind || workspaceState == model.WorkspaceAbsent
	switch {
	case upload && download:
		return "upload_and_download_needed", &nextAction{Command: "aha", Args: []string{"archive", "upload"}}
	case upload:
		return "upload_needed", &nextAction{Command: "aha", Args: []string{"archive", "upload"}}
	case download:
		return "download_needed", &nextAction{Command: "aha", Args: []string{"archive", "download"}}
	default:
		return "current", nil
	}
}

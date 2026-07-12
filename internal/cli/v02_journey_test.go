package cli_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adewale/aha/internal/cli"
	"github.com/adewale/aha/internal/config"
	"github.com/adewale/aha/internal/corpus"
	"github.com/adewale/aha/internal/model"
	"github.com/adewale/aha/internal/testutil"
)

func writeV02JourneyConfig(t *testing.T) (string, string, string) {
	t.Helper()
	root := t.TempDir()
	fixture := testutil.WriteAgentFixtures(t, root)
	archivePath := filepath.Join(root, "archive")
	workspacePath := filepath.Join(root, "workspace")
	configPath := filepath.Join(root, "config.jsonc")
	cfg := config.Default()
	cfg.MachineID = "journey-machine"
	cfg.Sources = []model.SourceConfig{{Type: "pi", Root: fixture.PiRoot, Enabled: true}}
	cfg.Archive = model.ArchiveConfig{Type: "local", Location: archivePath}
	cfg.WorkspaceDir = workspacePath
	cfg.AcknowledgedRawHistory = true
	if _, err := config.Write(configPath, cfg, ""); err != nil {
		t.Fatal(err)
	}
	return configPath, archivePath, workspacePath
}

func runV02(t *testing.T, args ...string) string {
	t.Helper()
	var out bytes.Buffer
	if err := cli.Run(args, &out, io.Discard); err != nil {
		t.Fatalf("aha %v: %v", args, err)
	}
	return out.String()
}

func TestV02DryRunReportsObservableTransferWork(t *testing.T) {
	configPath, _, workspacePath := writeV02JourneyConfig(t)
	runV02(t, "archive", "init", "--config", configPath)
	upload := runV02(t, "archive", "upload", "--config", configPath, "--dry-run", "--json")
	var uploadPlan struct {
		Files         int `json:"files"`
		BlobsUpload   int `json:"blobs_upload"`
		BlobsExisting int `json:"blobs_existing"`
		BlobsCarried  int `json:"blobs_carried"`
	}
	if err := json.Unmarshal([]byte(upload), &uploadPlan); err != nil || uploadPlan.Files == 0 || uploadPlan.BlobsUpload == 0 {
		t.Fatalf("upload plan=%s err=%v", upload, err)
	}
	runV02(t, "archive", "upload", "--config", configPath)
	download := runV02(t, "archive", "download", "--config", configPath, "--dry-run", "--json")
	var downloadPlan struct {
		UnknownBlobs int64 `json:"unknown_blobs"`
		UnknownBytes int64 `json:"unknown_bytes"`
	}
	if err := json.Unmarshal([]byte(download), &downloadPlan); err != nil || downloadPlan.UnknownBlobs == 0 || downloadPlan.UnknownBytes == 0 {
		t.Fatalf("download plan=%s err=%v", download, err)
	}
	if _, err := os.Stat(workspacePath); !os.IsNotExist(err) {
		t.Fatalf("dry-run created Workspace: %v", err)
	}
}

func TestV02ArchiveWorkspaceSearchShowJourney(t *testing.T) {
	configPath, _, workspacePath := writeV02JourneyConfig(t)
	runV02(t, "archive", "init", "--config", configPath, "--json")
	if _, err := os.Stat(workspacePath); !os.IsNotExist(err) {
		t.Fatalf("archive init created Workspace: %v", err)
	}
	runV02(t, "archive", "upload", "--config", configPath, "--json")
	if _, err := os.Stat(workspacePath); !os.IsNotExist(err) {
		t.Fatalf("archive upload mutated Workspace: %v", err)
	}
	runV02(t, "archive", "download", "--config", configPath, "--json")

	searchOutput := runV02(t, "search", "needle", "--config", configPath, "--json")
	var results []struct {
		RefText string `json:"ref_text"`
	}
	if err := json.Unmarshal([]byte(searchOutput), &results); err != nil || len(results) == 0 || results[0].RefText == "" {
		t.Fatalf("search output=%s err=%v", searchOutput, err)
	}
	showOutput := runV02(t, "show", results[0].RefText, "--config", configPath, "--json")
	if !bytes.Contains([]byte(showOutput), []byte("needle")) {
		t.Fatalf("show output=%s", showOutput)
	}

	statusOutput := runV02(t, "status", "--config", configPath, "--json")
	var status struct {
		Schema      string `json:"schema"`
		SystemState string `json:"system_state"`
		Archive     struct {
			State string `json:"state"`
		} `json:"archive"`
		Workspace struct {
			State          string `json:"state"`
			ArchiveMatches bool   `json:"archive_matches"`
		} `json:"workspace"`
	}
	if err := json.Unmarshal([]byte(statusOutput), &status); err != nil {
		t.Fatal(err)
	}
	if status.Schema != "aha.status.v2" || status.Archive.State != "populated" || status.Workspace.State != "current" || !status.Workspace.ArchiveMatches {
		t.Fatalf("status=%+v", status)
	}
}

func TestV02StatusCreatesNoWorkspaceLifecycleLock(t *testing.T) {
	configPath, _, workspacePath := writeV02JourneyConfig(t)
	runV02(t, "archive", "init", "--config", configPath)
	runV02(t, "archive", "upload", "--config", configPath)
	runV02(t, "archive", "download", "--config", configPath)
	lockPath := filepath.Join(filepath.Dir(workspacePath), "."+filepath.Base(workspacePath)+".lifecycle.lock")
	if err := os.Remove(lockPath); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	runV02(t, "status", "--config", configPath, "--json")
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Fatalf("read-only status created lifecycle lock: %v", err)
	}
}

func workspaceTreeDigest(t *testing.T, root string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	root = resolved
	h := sha256.New()
	if err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		fmt.Fprintf(h, "%s:%s:%d:%d\n", rel, info.Mode(), info.Size(), info.ModTime().UnixNano())
		if info.Mode().IsRegular() {
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			_, _ = h.Write(b)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

func TestV02RepeatedDownloadIsAnExplicitNoOp(t *testing.T) {
	configPath, _, workspacePath := writeV02JourneyConfig(t)
	runV02(t, "archive", "init", "--config", configPath)
	runV02(t, "archive", "upload", "--config", configPath)
	runV02(t, "archive", "download", "--config", configPath)
	lockPath := filepath.Join(filepath.Dir(workspacePath), "."+filepath.Base(workspacePath)+".lifecycle.lock")
	if err := os.Remove(lockPath); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	before := workspaceTreeDigest(t, workspacePath)
	output := runV02(t, "archive", "download", "--config", configPath, "--json")
	var got struct {
		State string `json:"state"`
		NoOp  bool   `json:"no_op"`
	}
	if err := json.Unmarshal([]byte(output), &got); err != nil || got.State != "current" || !got.NoOp {
		t.Fatalf("download no-op=%s err=%v", output, err)
	}
	after := workspaceTreeDigest(t, workspacePath)
	if before != after {
		t.Fatalf("no-op download mutated Workspace tree: before=%s after=%s", before, after)
	}
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Fatalf("no-op download created lifecycle lock: %v", err)
	}
}

func TestV02SystemStatusPreservesArchiveUpgradeRequired(t *testing.T) {
	configPath, archivePath, _ := writeV02JourneyConfig(t)
	runV02(t, "archive", "init", "--config", configPath)
	markerPath := filepath.Join(archivePath, "aha-depot.json")
	body, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatal(err)
	}
	var marker map[string]any
	if err := json.Unmarshal(body, &marker); err != nil {
		t.Fatal(err)
	}
	marker["required_features"] = append(marker["required_features"].([]any), "future-required-v9")
	body, _ = json.Marshal(marker)
	if err := os.WriteFile(markerPath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	output := runV02(t, "status", "--config", configPath, "--json")
	var got struct {
		SystemState string `json:"system_state"`
	}
	if err := json.Unmarshal([]byte(output), &got); err != nil || got.SystemState != "upgrade_required" {
		t.Fatalf("system status=%s err=%v, want upgrade_required", output, err)
	}
}

func TestV02WorkspaceStatusPreservesUpgradeRequired(t *testing.T) {
	configPath, _, workspacePath := writeV02JourneyConfig(t)
	runV02(t, "archive", "init", "--config", configPath)
	runV02(t, "archive", "upload", "--config", configPath)
	runV02(t, "archive", "download", "--config", configPath)
	store, err := corpus.OpenExisting(workspacePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.Exec(`insert into schema_migrations(version, applied_at) values(?, datetime('now'))`, corpus.CurrentWorkspaceSchemaVersion+1); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	output := runV02(t, "workspace", "status", "--config", configPath, "--json")
	var got struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal([]byte(output), &got); err != nil || got.State != string(model.WorkspaceUpgradeRequired) {
		t.Fatalf("Workspace status=%s err=%v, want upgrade_required", output, err)
	}
	systemOutput := runV02(t, "status", "--config", configPath, "--json")
	var system struct {
		SystemState string `json:"system_state"`
		NextAction  struct {
			Args []string `json:"args"`
		} `json:"next_action"`
	}
	if err := json.Unmarshal([]byte(systemOutput), &system); err != nil || system.SystemState != "upgrade_required" || strings.Join(system.NextAction.Args, " ") != "version --json" {
		t.Fatalf("system status=%s err=%v, want upgrade_required", systemOutput, err)
	}
}

func TestV02WorkspaceConflictsRejectsUnboundedPage(t *testing.T) {
	configPath, _, _ := writeV02JourneyConfig(t)
	runV02(t, "archive", "init", "--config", configPath)
	runV02(t, "archive", "upload", "--config", configPath)
	runV02(t, "archive", "download", "--config", configPath)
	if err := cli.Run([]string{"workspace", "conflicts", "--config", configPath, "--limit", "201", "--json"}, io.Discard, io.Discard); err == nil || !strings.Contains(err.Error(), "between 1 and 200") {
		t.Fatalf("workspace conflicts error=%v", err)
	}
}

func TestV02WorkspaceStatusReportsAbsentWithoutInitialisedArchive(t *testing.T) {
	configPath, _, _ := writeV02JourneyConfig(t)
	output := runV02(t, "workspace", "status", "--config", configPath, "--json")
	var got struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal([]byte(output), &got); err != nil || got.State != "absent" {
		t.Fatalf("Workspace status=%s err=%v", output, err)
	}
}

func TestV02ArchiveVerifyRejectsUninitialisedTransition(t *testing.T) {
	configPath, archivePath, workspacePath := writeV02JourneyConfig(t)
	err := cli.Run([]string{"archive", "verify", "--config", configPath}, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "archive init") {
		t.Fatalf("archive verify error=%v, want archive init transition", err)
	}
	for _, path := range []string{workspacePath, filepath.Join(archivePath, "aha-depot.json")} {
		if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
			t.Fatalf("rejected verify created %s: %v", path, statErr)
		}
	}
}

func TestV02RejectedPreflightCreatesNoWorkspaceOrArchiveMarker(t *testing.T) {
	configPath, archivePath, workspacePath := writeV02JourneyConfig(t)
	err := cli.Run([]string{"archive", "upload", "--config", configPath}, io.Discard, io.Discard)
	if err == nil {
		t.Fatal("upload to uninitialised Archive succeeded")
	}
	for _, path := range []string{workspacePath, filepath.Join(archivePath, "aha-depot.json"), filepath.Join(filepath.Dir(workspacePath), "."+filepath.Base(workspacePath)+".lifecycle.lock")} {
		if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
			t.Fatalf("failed preflight created %s: %v", path, statErr)
		}
	}
}

func TestV02DownloadRejectsUnrelatedDestinationBeforeMutation(t *testing.T) {
	configPath, _, _ := writeV02JourneyConfig(t)
	runV02(t, "archive", "init", "--config", configPath)
	runV02(t, "archive", "upload", "--config", configPath)
	unrelated := filepath.Join(t.TempDir(), "unrelated")
	if err := os.Mkdir(unrelated, 0o700); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(unrelated, "keep.txt")
	if err := os.WriteFile(keep, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := cli.Run([]string{"archive", "download", "--workspace", unrelated, "--config", configPath}, io.Discard, io.Discard)
	if err == nil {
		t.Fatal("download repurposed unrelated directory")
	}
	if body, readErr := os.ReadFile(keep); readErr != nil || string(body) != "keep" {
		t.Fatalf("unrelated destination changed: body=%q err=%v", body, readErr)
	}
	if _, statErr := os.Stat(filepath.Join(unrelated, model.WorkspaceDatabaseFilename)); !os.IsNotExist(statErr) {
		t.Fatalf("failed preflight created database: %v", statErr)
	}
}

func TestV02WorkspaceRepairPreservesBackup(t *testing.T) {
	configPath, _, workspacePath := writeV02JourneyConfig(t)
	runV02(t, "archive", "init", "--config", configPath)
	runV02(t, "archive", "upload", "--config", configPath)
	runV02(t, "archive", "download", "--config", configPath)
	if err := cli.Run([]string{"workspace", "repair", workspacePath, "--backup", "--config", configPath}, io.Discard, io.Discard); err == nil {
		t.Fatal("repair accepted a healthy Workspace")
	}
	store, err := corpus.OpenExisting(workspacePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.Exec(`delete from fts_messages where rowid=(select rowid from fts_messages limit 1)`); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	output := runV02(t, "workspace", "repair", workspacePath, "--backup", "--config", configPath, "--json")
	var got struct {
		State  string `json:"state"`
		Backup string `json:"backup"`
	}
	if err := json.Unmarshal([]byte(output), &got); err != nil || got.State != "current" || got.Backup == "" {
		t.Fatalf("repair=%s err=%v", output, err)
	}
	if info, err := os.Stat(got.Backup); err != nil || !info.IsDir() {
		t.Fatalf("backup=%q info=%v err=%v", got.Backup, info, err)
	}
	runV02(t, "workspace", "verify", workspacePath, "--config", configPath, "--json")
}

func TestV02V1ArchiveIsExplicitlyUnsupportedAndUntouched(t *testing.T) {
	configPath, archivePath, _ := writeV02JourneyConfig(t)
	if err := os.MkdirAll(archivePath, 0o700); err != nil {
		t.Fatal(err)
	}
	legacyMarker := filepath.Join(archivePath, "depot.json")
	if err := os.WriteFile(legacyMarker, []byte(`{"schema":"legacy/v1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	output := runV02(t, "archive", "status", "--config", configPath, "--json")
	if !strings.Contains(output, `"state": "damaged"`) || !strings.Contains(output, "unsupported v1 Archive layout") || !strings.Contains(output, "archive-v2") {
		t.Fatalf("v1 Archive status=%s", output)
	}
	if err := cli.Run([]string{"archive", "init", "--config", configPath}, io.Discard, io.Discard); err == nil {
		t.Fatal("v1 Archive was initialised in place")
	}
	if body, err := os.ReadFile(legacyMarker); err != nil || string(body) != `{"schema":"legacy/v1"}` {
		t.Fatalf("v1 marker changed: body=%q err=%v", body, err)
	}
	if _, err := os.Stat(filepath.Join(archivePath, "aha-depot.json")); !os.IsNotExist(err) {
		t.Fatalf("v2 marker created over v1 Archive: %v", err)
	}
}

func TestV02ArchiveInitRejectsAlreadyInitialisedArchive(t *testing.T) {
	configPath, _, _ := writeV02JourneyConfig(t)
	runV02(t, "archive", "init", "--config", configPath)
	if err := cli.Run([]string{"archive", "init", "--config", configPath}, io.Discard, io.Discard); err == nil {
		t.Fatal("second Archive init unexpectedly succeeded")
	}
}

func TestV02ArchiveSetDefaultChangesOnlyConfig(t *testing.T) {
	configPath, _, workspacePath := writeV02JourneyConfig(t)
	other := filepath.Join(t.TempDir(), "other-archive")
	runV02(t, "archive", "init", "local:"+other, "--config", configPath)
	runV02(t, "archive", "set-default", "local:"+other, "--config", configPath, "--json")
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Archive.Type != "local" || cfg.Archive.Location != other {
		t.Fatalf("selected Archive=%+v", cfg.Archive)
	}
	if _, err := os.Stat(workspacePath); !os.IsNotExist(err) {
		t.Fatalf("set-default changed Workspace: %v", err)
	}
}

func TestV02WorkspaceRejectsAnotherArchive(t *testing.T) {
	configPath, _, workspacePath := writeV02JourneyConfig(t)
	runV02(t, "archive", "init", "--config", configPath)
	runV02(t, "archive", "upload", "--config", configPath)
	runV02(t, "archive", "download", "--config", configPath)
	before, err := os.ReadFile(filepath.Join(workspacePath, model.WorkspaceDatabaseFilename))
	if err != nil {
		t.Fatal(err)
	}

	other := filepath.Join(t.TempDir(), "other-archive")
	runV02(t, "archive", "init", "local:"+other, "--config", configPath)
	err = cli.Run([]string{"archive", "download", "local:" + other, "--workspace", workspacePath, "--config", configPath}, io.Discard, io.Discard)
	if err == nil {
		t.Fatal("download from another Archive succeeded")
	}
	after, readErr := os.ReadFile(filepath.Join(workspacePath, model.WorkspaceDatabaseFilename))
	if readErr != nil || !bytes.Equal(before, after) {
		t.Fatalf("Archive mismatch changed Workspace: readErr=%v equal=%v", readErr, bytes.Equal(before, after))
	}
}

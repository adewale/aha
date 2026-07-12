package cli_test

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
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

func TestV02RepeatedDownloadIsAnExplicitNoOp(t *testing.T) {
	configPath, _, _ := writeV02JourneyConfig(t)
	runV02(t, "archive", "init", "--config", configPath)
	runV02(t, "archive", "upload", "--config", configPath)
	runV02(t, "archive", "download", "--config", configPath)
	output := runV02(t, "archive", "download", "--config", configPath, "--json")
	var got struct {
		State string `json:"state"`
		NoOp  bool   `json:"no_op"`
	}
	if err := json.Unmarshal([]byte(output), &got); err != nil || got.State != "current" || !got.NoOp {
		t.Fatalf("download no-op=%s err=%v", output, err)
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
	if _, statErr := os.Stat(filepath.Join(unrelated, "corpus.db")); !os.IsNotExist(statErr) {
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
	before, err := os.ReadFile(filepath.Join(workspacePath, "corpus.db"))
	if err != nil {
		t.Fatal(err)
	}

	other := filepath.Join(t.TempDir(), "other-archive")
	runV02(t, "archive", "init", "local:"+other, "--config", configPath)
	err = cli.Run([]string{"archive", "download", "local:" + other, "--workspace", workspacePath, "--config", configPath}, io.Discard, io.Discard)
	if err == nil {
		t.Fatal("download from another Archive succeeded")
	}
	after, readErr := os.ReadFile(filepath.Join(workspacePath, "corpus.db"))
	if readErr != nil || !bytes.Equal(before, after) {
		t.Fatalf("Archive mismatch changed Workspace: readErr=%v equal=%v", readErr, bytes.Equal(before, after))
	}
}

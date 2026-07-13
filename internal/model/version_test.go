package model_test

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/adewale/aha/internal/model"
)

func TestRunningBuildUsesInjectedIdentity(t *testing.T) {
	oldCommit, oldTime, oldDirty := model.BuildCommit, model.BuildTime, model.BuildDirty
	t.Cleanup(func() { model.BuildCommit, model.BuildTime, model.BuildDirty = oldCommit, oldTime, oldDirty })
	model.BuildCommit, model.BuildTime, model.BuildDirty = "abc123", "2026-07-10T12:00:00Z", "false"
	got := model.RunningBuild()
	if got.Version != model.Version || got.Commit != "abc123" || got.BuiltAt != "2026-07-10T12:00:00Z" || got.Dirty {
		t.Fatalf("build identity=%+v", got)
	}
}

func TestMakeBuildInjectsReleaseIdentity(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "aha")
	cmd := exec.Command("make", "build", "OUTPUT="+binary)
	cmd.Dir = root
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("make build: %v\n%s", err, output)
	}
	output, err := exec.Command(binary, "version", "--json").Output()
	if err != nil {
		t.Fatal(err)
	}
	var got model.BuildIdentity
	if err := json.Unmarshal(output, &got); err != nil {
		t.Fatalf("version output=%s err=%v", output, err)
	}
	if got.Version != model.Version || got.Commit == "" || got.Commit == "development" || got.BuiltAt == "" {
		t.Fatalf("release build identity=%+v", got)
	}
}

func TestProductVersionIsV020(t *testing.T) {
	if model.Version != "0.2.0" {
		t.Fatalf("model.Version=%q want 0.2.0", model.Version)
	}
}

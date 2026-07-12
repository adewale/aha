package model_test

import (
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

func TestProductVersionIsV020(t *testing.T) {
	if model.Version != "0.2.0" {
		t.Fatalf("model.Version=%q want 0.2.0", model.Version)
	}
}

package cli

import (
	"testing"

	"github.com/adewale/aha/internal/model"
)

func TestDeriveSystemStateNeverCallsUnsupportedStateCurrent(t *testing.T) {
	cases := []struct {
		name      string
		archive   model.ArchiveState
		workspace model.WorkspaceState
		want      string
		next      string
	}{
		{"Archive upgrade", model.ArchiveUpgradeRequired, model.WorkspaceCurrent, "upgrade_required", "aha version --json"},
		{"Workspace upgrade", model.ArchivePopulated, model.WorkspaceUpgradeRequired, "upgrade_required", "aha version --json"},
		{"Archive unreachable", model.ArchiveUnreachable, model.WorkspaceCurrent, "archive_unavailable", "aha archive status"},
		{"Archive invalid config", model.ArchiveInvalidConfiguration, model.WorkspaceCurrent, "archive_unavailable", "aha archive status"},
		{"Workspace mismatch", model.ArchivePopulated, model.WorkspaceArchiveMismatch, "workspace_attention_required", "aha workspace status"},
		{"Workspace invalid", model.ArchivePopulated, model.WorkspaceInvalidDestination, "workspace_attention_required", "aha workspace status"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, next := deriveSystemState("current", tc.archive, tc.workspace)
			if got != tc.want || next == nil || next.String() != tc.next {
				t.Fatalf("state=%q next=%v, want %q %q", got, next, tc.want, tc.next)
			}
		})
	}
}

package model

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestArchiveTransitionTableIsExhaustive(t *testing.T) {
	for _, state := range AllArchiveStates() {
		for _, operation := range AllArchiveOperations() {
			result := ArchiveTransition(state, operation)
			if !result.Allowed && result.NextAction.Command == "" {
				t.Fatalf("state=%s operation=%s rejection has no next action", state, operation)
			}
		}
	}
}

func TestWorkspaceTransitionTableIsExhaustive(t *testing.T) {
	for _, state := range AllWorkspaceStates() {
		for _, operation := range AllWorkspaceOperations() {
			result := WorkspaceTransition(state, operation)
			if !result.Allowed && result.NextAction.Command == "" {
				t.Fatalf("state=%s operation=%s rejection has no next action", state, operation)
			}
		}
	}
}

func TestConfigJSONUsesOnlyV02ResourceVocabulary(t *testing.T) {
	cfg := Config{MachineID: "m", WorkspaceDir: "/work", Archive: ArchiveConfig{Type: "local", Location: "/archive"}, AcknowledgedRawHistory: true}
	body, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"workspace_dir", "archive", "acknowledged_raw_history"} {
		if _, ok := got[key]; !ok {
			t.Fatalf("missing config key %q in %s", key, body)
		}
	}
	for _, old := range []string{"corpus_dir", "depot", "accept_secrets_warning"} {
		if _, ok := got[old]; ok {
			t.Fatalf("legacy config key %q remains in %s", old, body)
		}
	}
}

func TestStateSetsAreClosedAndStable(t *testing.T) {
	if got, want := AllArchiveStates(), []ArchiveState{ArchiveInvalidAddress, ArchiveInvalidConfiguration, ArchiveUnreachable, ArchiveUninitialised, ArchiveEmpty, ArchivePopulated, ArchiveDamaged}; !reflect.DeepEqual(got, want) {
		t.Fatalf("archive states=%v want %v", got, want)
	}
	if got, want := AllWorkspaceStates(), []WorkspaceState{WorkspaceAbsent, WorkspaceCurrent, WorkspaceBehind, WorkspaceDamaged, WorkspaceArchiveMismatch, WorkspaceInvalidDestination}; !reflect.DeepEqual(got, want) {
		t.Fatalf("workspace states=%v want %v", got, want)
	}
}

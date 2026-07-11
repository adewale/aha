package cli

import (
	"errors"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDoctorNextActionIsStateAwareAndPreservesConfigPath(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config with spaces.jsonc")
	cases := []struct {
		name       string
		configSeen bool
		cfgErr     error
		depot      map[string]any
		corpus     map[string]any
		wantArgs   []string
	}{
		{
			name: "missing config initializes exact path", configSeen: false,
			depot: map[string]any{}, corpus: map[string]any{},
			wantArgs: []string{"init", "--accept-secrets", "--config", configPath},
		},
		{
			name: "uninitialized depot before legacy rebuild", configSeen: true,
			depot:    map[string]any{"type": "r2", "location": "aha-depot", "initialized": false, "ok": true},
			corpus:   map[string]any{"legacy": true},
			wantArgs: []string{"depot", "init", "r2:aha-depot", "--config", configPath},
		},
		{
			name: "legacy corpus gets safe rebuild", configSeen: true,
			depot:    map[string]any{"type": "r2", "location": "aha-depot", "initialized": true, "ok": true},
			corpus:   map[string]any{"legacy": true},
			wantArgs: []string{"corpus", "rebuild", "--backup", "--config", configPath},
		},
		{
			name: "healthy empty corpus gets bounded refresh", configSeen: true,
			depot:    map[string]any{"type": "r2", "location": "aha-depot", "initialized": true, "ok": true},
			corpus:   map[string]any{"ok": true, "snapshots": 0},
			wantArgs: []string{"refresh", "--max-sessions", "1", "--config", configPath},
		},
		{
			name: "healthy populated corpus searches", configSeen: true,
			depot:    map[string]any{"type": "r2", "location": "aha-depot", "initialized": true, "ok": true},
			corpus:   map[string]any{"ok": true, "snapshots": 1},
			wantArgs: []string{"search", "migration", "--refs", "--config", configPath},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			action := doctorNextAction(configPath, tc.configSeen, tc.cfgErr, tc.depot, tc.corpus)
			if action.Command != "aha" || !reflect.DeepEqual(action.Args, tc.wantArgs) {
				t.Fatalf("action=%+v want args=%v", action, tc.wantArgs)
			}
			if got := actionOutputStrings(action); len(got) != 1 {
				t.Fatalf("next actions=%v want exactly one", got)
			}
		})
	}
}

func TestDoctorNextActionUsesSetupForDepotConfigurationErrors(t *testing.T) {
	action := doctorNextAction("/tmp/config", true, nil,
		map[string]any{"type": "r2", "location": "aha-depot", "error": "R2 credentials required"},
		map[string]any{"ok": false},
	)
	want := []string{"depot", "setup", "r2:aha-depot", "--config", "/tmp/config"}
	if action.Command != "aha" || !reflect.DeepEqual(action.Args, want) {
		t.Fatalf("action=%+v want=%v", action, want)
	}
}

func TestDoctorNextActionMalformedConfigInitializesPreservedRepairPath(t *testing.T) {
	action := doctorNextAction("/tmp/config", true, errors.New("bad JSON"), nil, nil)
	want := []string{"init", "--accept-secrets", "--config", "/tmp/config.repaired"}
	if !reflect.DeepEqual(action.Args, want) {
		t.Fatalf("action=%+v want args=%v", action, want)
	}
}

func TestDepotSetupActionHasExactlyOneSafeTransition(t *testing.T) {
	cases := []struct {
		name     string
		selected bool
		diag     map[string]any
		state    string
		wantArgs []string
	}{
		{"uninitialized", false, map[string]any{"initialized": false, "ok": true}, "uninitialized", []string{"depot", "init", "r2:aha-depot", "--config", "/tmp/cfg"}},
		{"degraded", true, map[string]any{"initialized": true, "ok": false}, "degraded", []string{"depot", "verify", "r2:aha-depot", "--deep", "--config", "/tmp/cfg"}},
		{"ready not selected", false, map[string]any{"initialized": true, "ok": true}, "ready-not-selected", []string{"depot", "use", "r2:aha-depot", "--config", "/tmp/cfg"}},
		{"ready selected", true, map[string]any{"initialized": true, "ok": true}, "ready", []string{"refresh", "--max-sessions", "1", "--config", "/tmp/cfg"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			state, action := depotSetupAction("r2:aha-depot", "/tmp/cfg", tc.selected, tc.diag)
			if state != tc.state || !reflect.DeepEqual(action.Args, tc.wantArgs) || len(actionOutputStrings(action)) != 1 {
				t.Fatalf("state=%q action=%+v", state, action)
			}
		})
	}
}

func actionOutputStrings(action nextAction) []string {
	next, _ := actionOutput(action)
	return next
}

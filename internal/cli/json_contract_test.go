package cli_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adewale/aha/internal/cli"
	"github.com/adewale/aha/internal/corpus"
	"github.com/adewale/aha/internal/testutil"
)

func TestIngestPlaceholderErrorNamesSafeFieldAndLeavesNoCorpus(t *testing.T) {
	clearR2Environment(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("AHA_R2_ACCOUNT_ID", "0123456789abcdef0123456789abcdef")
	t.Setenv("AHA_R2_ACCESS_KEY_ID", "your-access-canary")
	t.Setenv("AHA_R2_SECRET_ACCESS_KEY", "real-secret")
	corpusRoot := filepath.Join(t.TempDir(), "must-not-exist")
	var stdout, stderr bytes.Buffer
	code := cli.RunMain([]string{"ingest", "--depot", "r2:private-bucket", "--corpus", corpusRoot, "--json"}, &stdout, &stderr)
	if code == 0 {
		t.Fatal("ingest unexpectedly accepted placeholder access key")
	}
	var envelope struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(stderr.Bytes(), &envelope); err != nil {
		t.Fatalf("stderr is not JSON: %v\n%s", err, stderr.String())
	}
	if !strings.Contains(envelope.Error.Message, "access key ID") || !strings.Contains(envelope.Error.Message, "AHA_R2_ACCESS_KEY_ID") {
		t.Fatalf("message=%q want safe field-specific correction", envelope.Error.Message)
	}
	if strings.Contains(stderr.String(), "your-access-canary") {
		t.Fatalf("error leaked rejected value: %s", stderr.String())
	}
	if _, err := os.Stat(corpusRoot); !os.IsNotExist(err) {
		t.Fatalf("failed preflight created corpus root: %v", err)
	}
}

func TestPrivacyAcknowledgementFailureUsesSingleErrorBoundary(t *testing.T) {
	root := t.TempDir()
	fx := testutil.WriteAgentFixtures(t, root)
	base := []string{"snapshot", "--config", filepath.Join(root, "config.jsonc"), "--machine", "privacy-machine", "--source", "pi=" + fx.PiRoot, "--depot", "local:" + filepath.Join(root, "depot")}

	t.Run("human", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		if code := cli.RunMain(base, &stdout, &stderr); code == 0 {
			t.Fatal("snapshot unexpectedly succeeded")
		}
		text := stderr.String()
		if strings.Count(text, "error:") != 1 || strings.Count(text, "next:") != 1 {
			t.Fatalf("stderr=%q want one public error and one action", text)
		}
		if strings.Contains(text, "Snapshots are raw provenance") {
			t.Fatalf("stderr contains pre-error remediation output: %s", text)
		}
		if !strings.Contains(text, "privacy acknowledgement") || !strings.Contains(text, "--accept-secrets") {
			t.Fatalf("stderr lacks safe actionable privacy error: %s", text)
		}
	})

	t.Run("json", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		args := append(append([]string(nil), base...), "--json")
		if code := cli.RunMain(args, &stdout, &stderr); code == 0 {
			t.Fatal("snapshot unexpectedly succeeded")
		}
		var envelope struct {
			Schema string `json:"schema"`
			Error  struct {
				Code       string   `json:"code"`
				Next       []string `json:"next"`
				NextAction struct {
					Command string   `json:"command"`
					Args    []string `json:"args"`
				} `json:"next_action"`
			} `json:"error"`
		}
		if err := json.Unmarshal(stderr.Bytes(), &envelope); err != nil {
			t.Fatalf("stderr is not one JSON document: %v\n%s", err, stderr.String())
		}
		if envelope.Schema != "aha.error.v1" || envelope.Error.Code != "privacy_acknowledgement_required" || len(envelope.Error.Next) != 1 || envelope.Error.NextAction.Command != "aha" || !strings.Contains(strings.Join(envelope.Error.NextAction.Args, " "), "--accept-secrets") {
			t.Fatalf("envelope=%+v", envelope)
		}
	})
}

func TestRunMainHumanErrorsAreConciseAndHaveOneAction(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := cli.RunMain([]string{"status", "--definitely-invalid"}, &stdout, &stderr)
	if code == 0 {
		t.Fatal("RunMain unexpectedly succeeded")
	}
	text := stderr.String()
	if strings.Count(text, "error:") != 1 || strings.Count(text, "next:") != 1 {
		t.Fatalf("stderr=%q want one error and one next action", text)
	}
	for _, raw := range []string{"flag provided but not defined", "Usage of", "aha doctor\naha help"} {
		if strings.Contains(text, raw) {
			t.Fatalf("stderr leaked raw/duplicate diagnostics %q: %s", raw, text)
		}
	}
}

func TestRunMainDefaultErrorHidesRawPathsAndVerboseUsesSafeDiagnostics(t *testing.T) {
	secretPath := filepath.Join(t.TempDir(), "secret-canary", "cpu.pprof")
	if err := os.WriteFile(filepath.Dir(secretPath), []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, verbose := range []bool{false, true} {
		args := []string{"status", "--cpuprofile", secretPath}
		if verbose {
			args = append(args, "--verbose-errors")
		}
		var stdout, stderr bytes.Buffer
		if code := cli.RunMain(args, &stdout, &stderr); code == 0 {
			t.Fatal("profiling unexpectedly succeeded")
		}
		if strings.Contains(stderr.String(), secretPath) || strings.Contains(stderr.String(), "secret-canary") {
			t.Fatalf("verbose=%v leaked path: %s", verbose, stderr.String())
		}
		if verbose && !strings.Contains(stderr.String(), "diagnostic:") {
			t.Fatalf("verbose stderr missing safe diagnostic: %s", stderr.String())
		}
	}
}

func TestRunMainJSONErrorContract(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := cli.RunMain([]string{"nope", "--json"}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("RunMain unexpectedly succeeded")
	}
	if stdout.Len() != 0 {
		t.Fatalf("JSON errors should not write stdout: %s", stdout.String())
	}
	var payload struct {
		Schema string `json:"schema"`
		Error  struct {
			Code       string   `json:"code"`
			Message    string   `json:"message"`
			Command    string   `json:"command"`
			Next       []string `json:"next"`
			NextAction struct {
				Command string   `json:"command"`
				Args    []string `json:"args"`
			} `json:"next_action"`
			Diagnostics []map[string]any `json:"diagnostics"`
		} `json:"error"`
	}
	if err := json.Unmarshal(stderr.Bytes(), &payload); err != nil {
		t.Fatalf("stderr is not JSON: %v\n%s", err, stderr.String())
	}
	if payload.Error.Code != "unknown_command" || payload.Error.Command != "nope" || payload.Error.Message == "" {
		t.Fatalf("bad JSON error payload: %+v", payload.Error)
	}
	if payload.Schema != "aha.error.v1" || len(payload.Error.Next) != 1 || payload.Error.NextAction.Command != "aha" || len(payload.Error.Diagnostics) != 0 {
		t.Fatalf("JSON error contract is incomplete: schema=%q error=%+v", payload.Schema, payload.Error)
	}
	if !strings.Contains(payload.Error.Next[0], "aha help") {
		t.Fatalf("JSON error missing sole next action: %+v", payload.Error)
	}
}

func TestEveryRegisteredCommandUsesTheSharedHumanErrorContract(t *testing.T) {
	for name := range cli.Registry() {
		t.Run(name, func(t *testing.T) {
			args := []string{name, "--definitely-invalid"}
			if name == "corpus" || name == "depot" {
				args = []string{name, "invalid-subcommand"}
			}
			var stdout, stderr bytes.Buffer
			if code := cli.RunMain(args, &stdout, &stderr); code == 0 {
				t.Fatalf("%s unexpectedly succeeded", name)
			}
			text := stderr.String()
			if strings.Count(text, "error:") != 1 || strings.Count(text, "next:") != 1 {
				t.Fatalf("%s stderr=%q want one error and one action", name, text)
			}
			if strings.Contains(text, "Usage of") || strings.Contains(text, "flag provided") {
				t.Fatalf("%s stderr leaked parser output: %s", name, text)
			}
		})
	}
}

func TestEveryRegisteredCommandUsesTheSharedJSONErrorContract(t *testing.T) {
	for name := range cli.Registry() {
		t.Run(name, func(t *testing.T) {
			args := []string{name, "--definitely-invalid", "--json"}
			if name == "corpus" || name == "depot" {
				args = []string{name, "invalid-subcommand", "--json"}
			}
			var stdout, stderr bytes.Buffer
			if code := cli.RunMain(args, &stdout, &stderr); code == 0 {
				t.Fatalf("%s unexpectedly succeeded", name)
			}
			if stdout.Len() != 0 {
				t.Fatalf("%s failure wrote stdout: %s", name, stdout.String())
			}
			var envelope struct {
				Schema string `json:"schema"`
				Error  struct {
					Message    string   `json:"message"`
					Next       []string `json:"next"`
					NextAction struct {
						Command string   `json:"command"`
						Args    []string `json:"args"`
					} `json:"next_action"`
				} `json:"error"`
			}
			if err := json.Unmarshal(stderr.Bytes(), &envelope); err != nil {
				t.Fatalf("%s stderr is not one JSON document: %v\n%s", name, err, stderr.String())
			}
			if envelope.Schema != "aha.error.v1" || envelope.Error.Message == "" || len(envelope.Error.Next) != 1 || envelope.Error.NextAction.Command == "" {
				t.Fatalf("%s error contract=%+v", name, envelope)
			}
		})
	}
}

func TestRunMainVerboseJSONAddsOnlySafeDiagnostics(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := cli.RunMain([]string{"nope", "--json", "--verbose-errors"}, &stdout, &stderr); code == 0 {
		t.Fatal("unknown command unexpectedly succeeded")
	}
	var envelope struct {
		Error struct {
			Diagnostics []struct {
				Kind string `json:"kind"`
			} `json:"diagnostics"`
		} `json:"error"`
	}
	if err := json.Unmarshal(stderr.Bytes(), &envelope); err != nil {
		t.Fatalf("stderr is not JSON: %v\n%s", err, stderr.String())
	}
	if len(envelope.Error.Diagnostics) != 1 || envelope.Error.Diagnostics[0].Kind == "" {
		t.Fatalf("diagnostics=%+v", envelope.Error.Diagnostics)
	}
}

func TestRunMainJSONFlagParseErrorIsOnlyJSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := cli.RunMain([]string{"status", "--bad", "--json"}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("RunMain unexpectedly succeeded")
	}
	if stdout.Len() != 0 {
		t.Fatalf("JSON errors should not write stdout: %s", stdout.String())
	}
	text := stderr.String()
	if strings.Contains(text, "Usage of") {
		t.Fatalf("JSON error stderr contains flag package usage text: %s", text)
	}
	var payload struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Command string `json:"command"`
		} `json:"error"`
	}
	if err := json.Unmarshal(stderr.Bytes(), &payload); err != nil {
		t.Fatalf("stderr is not JSON: %v\n%s", err, text)
	}
	if payload.Error.Code != "flag_parse_error" || payload.Error.Command != "status" || payload.Error.Message == "" {
		t.Fatalf("bad JSON flag error payload: %+v", payload.Error)
	}
}

func TestRunMainJSONUnsupportedRefError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := cli.RunMain([]string{"read", "pi:m:s#e", "--json"}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("RunMain unexpectedly succeeded")
	}
	var payload struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(stderr.Bytes(), &payload); err != nil {
		t.Fatalf("stderr is not JSON: %v\n%s", err, stderr.String())
	}
	if payload.Error.Code != "unsupported_ref" {
		t.Fatalf("bad typed ref error code: %+v stderr=%s", payload.Error, stderr.String())
	}
}

func TestRunMainJSONTypedNotFoundError(t *testing.T) {
	root := t.TempDir()
	store, err := corpus.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	store.Close()
	var stdout, stderr bytes.Buffer
	code := cli.RunMain([]string{"read", "--session", "missing", "--corpus", root, "--json"}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("RunMain unexpectedly succeeded")
	}
	var payload struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(stderr.Bytes(), &payload); err != nil {
		t.Fatalf("stderr is not JSON: %v\n%s", err, stderr.String())
	}
	if payload.Error.Code != "not_found" {
		t.Fatalf("bad typed error code: %+v stderr=%s", payload.Error, stderr.String())
	}
}

func TestRefreshJSONUsesStableLowercaseReportKeys(t *testing.T) {
	root := t.TempDir()
	fx := testutil.WriteAgentFixtures(t, root)
	outDir := filepath.Join(root, "bundles")
	repoDir := filepath.Join(root, "repo")
	var out bytes.Buffer
	if err := cli.Run([]string{"refresh", "--json", "--machine", "m1", "--source", "pi=" + fx.PiRoot, "--depot", "local:" + outDir, "--repo", repoDir, "--accept-secrets", "--captured-at", "2026-01-03T00:00:00Z"}, &out, &out); err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Push struct {
			ManifestSHA256 string `json:"manifest_sha256"`
			Reused         bool   `json:"reused"`
		} `json:"push"`
		Report struct {
			Sessions  int  `json:"sessions"`
			Entries   int  `json:"entries"`
			Messages  int  `json:"messages"`
			Images    int  `json:"images"`
			Artifacts int  `json:"artifacts"`
			Duplicate bool `json:"duplicate"`
		} `json:"report"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("refresh JSON did not decode: %v\n%s", err, out.String())
	}
	if payload.Push.ManifestSHA256 == "" || payload.Push.Reused || payload.Report.Sessions != 1 || payload.Report.Entries == 0 || payload.Report.Messages == 0 {
		t.Fatalf("bad refresh JSON payload: %+v", payload)
	}
	var raw map[string]any
	if err := json.Unmarshal(out.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	report := raw["report"].(map[string]any)
	for _, bad := range []string{"Sessions", "Entries", "Messages", "Images", "Artifacts", "Duplicate"} {
		if _, ok := report[bad]; ok {
			t.Fatalf("refresh JSON leaked PascalCase key %q: %s", bad, out.String())
		}
	}
}

func TestStatusAndDoctorJSONIncludeNextActions(t *testing.T) {
	root := t.TempDir()
	store, err := corpus.Open(filepath.Join(root, "corpus"))
	if err != nil {
		t.Fatal(err)
	}
	store.Close()
	var out bytes.Buffer
	if err := cli.Run([]string{"status", "--corpus", filepath.Join(root, "corpus"), "--json"}, &out, &out); err != nil {
		t.Fatal(err)
	}
	var status struct {
		Next []string `json:"next"`
	}
	if err := json.Unmarshal(out.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if len(status.Next) == 0 {
		t.Fatalf("status JSON missing next actions: %s", out.String())
	}
	out.Reset()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "xdg"))
	if err := cli.Run([]string{"doctor", "--json"}, &out, &out); err != nil {
		t.Fatal(err)
	}
	var doctor struct {
		Next []string `json:"next"`
	}
	if err := json.Unmarshal(out.Bytes(), &doctor); err != nil {
		t.Fatal(err)
	}
	if len(doctor.Next) != 1 {
		t.Fatalf("doctor JSON must contain exactly one next action: %s", out.String())
	}
}

func TestCommandsMarkdownGeneratedFromMetadata(t *testing.T) {
	got := cli.GenerateCommandsMarkdown()
	want, err := os.ReadFile(filepath.Join("..", "..", "docs", "commands.md"))
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Fatalf("docs/commands.md is stale; regenerate from command metadata")
	}
	for _, name := range cli.CommandNames() {
		if !strings.Contains(got, "## aha "+name) {
			t.Fatalf("generated commands doc missing %s", name)
		}
	}
}

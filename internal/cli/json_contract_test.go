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
		Error struct {
			Code    string   `json:"code"`
			Message string   `json:"message"`
			Command string   `json:"command"`
			Next    []string `json:"next"`
		} `json:"error"`
	}
	if err := json.Unmarshal(stderr.Bytes(), &payload); err != nil {
		t.Fatalf("stderr is not JSON: %v\n%s", err, stderr.String())
	}
	if payload.Error.Code != "unknown_command" || payload.Error.Command != "nope" || payload.Error.Message == "" {
		t.Fatalf("bad JSON error payload: %+v", payload.Error)
	}
	if len(payload.Error.Next) == 0 || !strings.Contains(strings.Join(payload.Error.Next, "\n"), "aha help") {
		t.Fatalf("JSON error missing next hints: %+v", payload.Error)
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
	if err := cli.Run([]string{"doctor", "--json"}, &out, &out); err != nil {
		t.Fatal(err)
	}
	var doctor struct {
		Next []string `json:"next"`
	}
	if err := json.Unmarshal(out.Bytes(), &doctor); err != nil {
		t.Fatal(err)
	}
	if len(doctor.Next) == 0 {
		t.Fatalf("doctor JSON missing next actions: %s", out.String())
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

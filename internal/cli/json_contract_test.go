package cli_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adewale/aha/internal/cli"
)

func TestV02PrivacyFailureUsesOneJSONErrorAction(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := cli.RunMain([]string{"init", "--config", filepath.Join(t.TempDir(), "config.jsonc"), "--json"}, &stdout, &stderr)
	if code != 1 || stdout.Len() != 0 {
		t.Fatalf("code=%d stdout=%q", code, stdout.String())
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
		t.Fatalf("error JSON: %v\n%s", err, stderr.String())
	}
	if envelope.Schema != "aha.error.v1" || envelope.Error.Code != "privacy_acknowledgement_required" || len(envelope.Error.Next) != 1 || envelope.Error.NextAction.Command != "aha" || !strings.Contains(strings.Join(envelope.Error.NextAction.Args, " "), "--acknowledge-raw-history") {
		t.Fatalf("envelope=%+v", envelope)
	}
}

func TestRemovedFlagFailsInsteadOfActingAsAlias(t *testing.T) {
	for _, args := range [][]string{{"search", "needle", "--corpus", t.TempDir()}, {"show", "x", "--repo", t.TempDir()}, {"status", "--depot", "local:/tmp/x"}, {"archive", "upload", "--accept-secrets"}} {
		if err := cli.Run(args, &bytes.Buffer{}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "flag provided but not defined") {
			t.Fatalf("args=%v err=%v", args, err)
		}
	}
}

func TestGeneratedCommandsMarkdownMatchesRegistry(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "docs", "commands.md"))
	if err != nil {
		t.Fatal(err)
	}
	if got := cli.GenerateCommandsMarkdown(); string(body) != got {
		t.Fatal("docs/commands.md is stale; run make gen-docs")
	}
}

package cli_test

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adewale/aha/internal/cli"
	"github.com/adewale/aha/internal/testutil"
)

// TestReadBranchAndLiveCLI exercises the agent/human-facing CLI surface
// for the tree-walking and live-context reads end to end. The fixture Pi
// session is p1 → p2; both --branch and --live from leaf p2 must return
// the root → leaf path, and the invalid combinations must be rejected.
func TestReadBranchAndLiveCLI(t *testing.T) {
	root := t.TempDir()
	fx := testutil.WriteAgentFixtures(t, root)
	corpusDir := filepath.Join(root, "corpus")
	configPath := filepath.Join(root, "config.jsonc")
	cfg := `{
		"machine_id":"cli-branch-test",
		"sources":[{"type":"pi","root":"` + filepath.ToSlash(fx.PiRoot) + `","enabled":true}],
		"corpus_dir":"` + filepath.ToSlash(corpusDir) + `",
		"depot":{"type":"local","location":"` + filepath.ToSlash(filepath.Join(root, "bundles")) + `"},
		"accept_secrets_warning":true
	}`
	if err := os.WriteFile(configPath, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := cli.Run([]string{"refresh", "--config", configPath, "--captured-at", "2026-01-03T00:00:00Z", "--bundle-id", "branch"}, io.Discard, io.Discard); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	for _, mode := range []string{"--branch", "--live"} {
		t.Run(mode, func(t *testing.T) {
			var out bytes.Buffer
			if err := cli.Run([]string{"read", "--corpus", corpusDir, "--session", "pi-session", mode, "p2", "--json"}, &out, io.Discard); err != nil {
				t.Fatalf("read %s: %v", mode, err)
			}
			s := out.String()
			if !strings.Contains(s, `"p1"`) || !strings.Contains(s, `"p2"`) {
				t.Fatalf("%s read missing path entries: %s", mode, s)
			}
			if strings.Index(s, `"p1"`) > strings.Index(s, `"p2"`) {
				t.Fatalf("%s read out of order (want p1 before p2): %s", mode, s)
			}
		})
	}

	// --branch and --live are mutually exclusive.
	var stderr bytes.Buffer
	if err := cli.Run([]string{"read", "--corpus", corpusDir, "--session", "pi-session", "--branch", "p2", "--live", "p2"}, io.Discard, &stderr); err == nil {
		t.Fatal("expected error combining --branch and --live")
	}
}

package adapters_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/adewale/aha/internal/adapters"
	"github.com/adewale/aha/internal/model"
	"github.com/adewale/aha/internal/testutil"
)

func TestCodexDiscoverAndParseRollout(t *testing.T) {
	root := t.TempDir()
	fx := testutil.WriteAgentFixtures(t, root)
	ad := adapters.CodexCLI{}
	files, err := ad.Discover(t.Context(), model.SourceConfig{Type: "codex", Root: fx.CodexRoot, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("codex files=%d want 1: %+v", len(files), files)
	}
	f, err := os.Open(files[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	ps, err := ad.ParseSession(t.Context(), files[0], f)
	if err != nil {
		t.Fatal(err)
	}
	if ps.Source != "codex" || ps.SourceSessionID == "" || len(ps.Entries) != 3 {
		t.Fatalf("bad parsed codex session: %+v", ps)
	}
	if got := ps.Entries[1].Text; got != "codex needle" {
		t.Fatalf("codex text=%q", got)
	}
}

func TestCodexDiscoverIgnoresNonSessionJSONL(t *testing.T) {
	root := t.TempDir()
	fx := testutil.WriteAgentFixtures(t, root)
	if err := os.WriteFile(filepath.Join(fx.CodexRoot, "auth.jsonl"), []byte(`{"token":"secret"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fx.CodexRoot, "2026", "05", "20", "debug.jsonl"), []byte(`{"level":"debug"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	files, err := (adapters.CodexCLI{}).Discover(t.Context(), model.SourceConfig{Type: "codex", Root: fx.CodexRoot, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || filepath.Base(files[0].Path) != "rollout-2026-05-20T00-00-00-codex-session.jsonl" {
		t.Fatalf("codex discovered non-session files: %+v", files)
	}
}

func TestCodexDiscoverSkipsSymlink(t *testing.T) {
	root := t.TempDir()
	fx := testutil.WriteAgentFixtures(t, root)
	outside := filepath.Join(root, "outside.jsonl")
	if err := os.WriteFile(outside, []byte(`{"id":"outside"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(fx.CodexRoot, "2026", "05", "20", "rollout-link.jsonl")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	files, err := (adapters.CodexCLI{}).Discover(t.Context(), model.SourceConfig{Type: "codex", Root: fx.CodexRoot, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("codex files=%d want 1: %+v", len(files), files)
	}
}

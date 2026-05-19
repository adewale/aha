package archive_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/adewale/aha/internal/adapters"
	"github.com/adewale/aha/internal/archive"
	"github.com/adewale/aha/internal/config"
	"github.com/adewale/aha/internal/model"
	"github.com/adewale/aha/internal/testutil"
)

func TestCaptureWriteDeterministicAndReadOnly(t *testing.T) {
	root := t.TempDir()
	fx := testutil.WriteAgentFixtures(t, root)
	sourceFile := filepath.Join(fx.PiRoot, "--Users-me-proj--", "2026_pi.jsonl")
	before, err := os.Stat(sourceFile)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.MachineID = "test-machine"
	cfg.Sources = []model.SourceConfig{{Type: "pi", Root: fx.PiRoot, Enabled: true}, {Type: "claude-code", Root: fx.ClaudeRoot, Enabled: true}}
	b1, err := archive.Capture(t.Context(), cfg, adapters.Builtins(), archive.Options{CapturedAt: "2026-01-03T00:00:00Z", BundleID: "fixed"})
	if err != nil {
		t.Fatal(err)
	}
	b2, err := archive.Capture(t.Context(), cfg, adapters.Builtins(), archive.Options{CapturedAt: "2026-01-03T00:00:00Z", BundleID: "fixed"})
	if err != nil {
		t.Fatal(err)
	}
	p1 := filepath.Join(root, "b1.tar.zst")
	p2 := filepath.Join(root, "b2.tar.zst")
	s1, err := archive.Write(p1, b1)
	if err != nil {
		t.Fatal(err)
	}
	s2, err := archive.Write(p2, b2)
	if err != nil {
		t.Fatal(err)
	}
	bb1, _ := os.ReadFile(p1)
	bb2, _ := os.ReadFile(p2)
	if s1 != s2 || !bytes.Equal(bb1, bb2) {
		t.Fatalf("bundle not deterministic")
	}
	after, err := os.Stat(sourceFile)
	if err != nil {
		t.Fatal(err)
	}
	if before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		t.Fatalf("snapshot modified source file")
	}
	manifest, _, _, _, err := archive.ReadBundle(p1)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.BundleID != "fixed" || manifest.Counts.SessionFiles != 2 || manifest.Counts.ImageFiles != 1 {
		t.Fatalf("bad manifest: %+v", manifest)
	}
}

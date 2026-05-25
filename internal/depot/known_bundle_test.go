package depot_test

import (
	"path/filepath"
	"testing"

	"github.com/adewale/aha/internal/adapters"
	"github.com/adewale/aha/internal/archive"
	"github.com/adewale/aha/internal/config"
	"github.com/adewale/aha/internal/depot"
	"github.com/adewale/aha/internal/model"
	"github.com/adewale/aha/internal/testutil"
)

func TestPutBundleKnownPreservesWriterStateMetadata(t *testing.T) {
	root := t.TempDir()
	fx := testutil.WriteAgentFixtures(t, root)
	cfg := config.Default()
	cfg.MachineID = "known-machine"
	cfg.Sources = []model.SourceConfig{{Type: "pi", Root: fx.PiRoot, Enabled: true}}
	bundle, err := archive.Capture(t.Context(), cfg, adapters.Builtins(), archive.Options{CapturedAt: "2026-01-03T00:00:00Z", BundleID: "known"})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "bundle.tar.zst")
	info, err := archive.WriteWithInfo(path, bundle)
	if err != nil {
		t.Fatal(err)
	}
	d, err := depot.NewLocal(filepath.Join(root, "depot"))
	if err != nil {
		t.Fatal(err)
	}
	ref, created, err := depot.PutBundleKnown(t.Context(), d, path, depot.BundleRefFromWriteInfo(bundle.Manifest, info, filepath.Base(path)))
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatalf("known put did not create bundle")
	}
	if ref.StateSHA256 != info.StateSHA256 || ref.ManifestSHA256 != info.ManifestSHA256 {
		t.Fatalf("known ref lost writer metadata: ref=%+v info=%+v", ref, info)
	}
	refs, err := d.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || refs[0].StateSHA256 != info.StateSHA256 || refs[0].ManifestSHA256 != info.ManifestSHA256 {
		t.Fatalf("catalog refs missing writer metadata: %+v", refs)
	}
}

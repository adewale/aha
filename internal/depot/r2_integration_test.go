//go:build integration

package depot_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/adewale/aha/internal/archive"
	"github.com/adewale/aha/internal/depot"
	"github.com/adewale/aha/internal/model"
)

func TestR2IntegrationPutListFetchVerify(t *testing.T) {
	bucket := os.Getenv("AHA_R2_TEST_BUCKET")
	if bucket == "" {
		t.Skip("set AHA_R2_TEST_BUCKET plus R2/AHA_R2 credentials to run integration test")
	}
	cfg, err := depot.ResolveR2Config(model.R2DepotConfig{})
	if err != nil {
		t.Skipf("R2 credentials not configured: %v", err)
	}
	d := depot.NewR2(bucket, cfg)
	if err := d.Init(t.Context()); err != nil {
		t.Fatal(err)
	}
	bundlePath := writeDepotTestBundle(t, filepath.Join(t.TempDir(), "src"))
	ref, _, err := d.PutBundle(t.Context(), bundlePath)
	if err != nil {
		t.Fatal(err)
	}
	refs, err := d.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, got := range refs {
		if got.BundleSHA256 == ref.BundleSHA256 {
			found = true
		}
	}
	if !found {
		t.Fatalf("uploaded bundle %s missing from catalog", ref.BundleSHA256)
	}
	fetched := filepath.Join(t.TempDir(), "fetched.tar.zst")
	if err := d.Fetch(t.Context(), ref, fetched); err != nil {
		t.Fatal(err)
	}
	if _, err := archive.ReadManifest(fetched); err != nil {
		t.Fatal(err)
	}
	report, err := d.Verify(t.Context(), false)
	if err != nil {
		t.Fatal(err)
	}
	if report.Bundles == 0 {
		t.Fatalf("verify saw no bundles: %+v", report)
	}
}

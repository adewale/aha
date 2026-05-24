package depot_test

import (
	"path/filepath"
	"testing"

	"github.com/adewale/aha/internal/archive"
	"github.com/adewale/aha/internal/depot"
)

func TestDepotDriverContractLocal(t *testing.T) {
	runDepotDriverContract(t, func(t *testing.T) depot.Driver {
		d, err := depot.NewLocal(filepath.Join(t.TempDir(), "depot"))
		if err != nil {
			t.Fatal(err)
		}
		return d
	})
}

func TestDepotDriverContractR2Fake(t *testing.T) {
	fake := newFakeS3(t)
	t.Cleanup(fake.Close)
	runDepotDriverContract(t, func(t *testing.T) depot.Driver {
		return fake.Depot("bucket")
	})
}

func runDepotDriverContract(t *testing.T, open func(*testing.T) depot.Driver) {
	t.Helper()
	d := open(t)
	if err := d.Init(t.Context()); err != nil {
		t.Fatal(err)
	}
	bundlePath := writeDepotTestBundle(t, filepath.Join(t.TempDir(), "src"))
	ref, created, err := d.PutBundle(t.Context(), bundlePath)
	if err != nil {
		t.Fatal(err)
	}
	if !created || ref.BundleSHA256 == "" || ref.Key != depot.BundleKey(ref.BundleSHA256) {
		t.Fatalf("PutBundle created=%v ref=%+v", created, ref)
	}
	ref2, created, err := d.PutBundle(t.Context(), bundlePath)
	if err != nil {
		t.Fatal(err)
	}
	if created || ref2.BundleSHA256 != ref.BundleSHA256 {
		t.Fatalf("duplicate PutBundle created=%v ref=%+v", created, ref2)
	}
	refs, err := d.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || refs[0].BundleSHA256 != ref.BundleSHA256 {
		t.Fatalf("List=%+v", refs)
	}
	fetched := filepath.Join(t.TempDir(), "fetched.tar.zst")
	if err := d.Fetch(t.Context(), ref, fetched); err != nil {
		t.Fatal(err)
	}
	if _, err := archive.ReadManifest(fetched); err != nil {
		t.Fatalf("Fetch produced invalid bundle: %v", err)
	}
	report, err := d.Verify(t.Context(), false)
	if err != nil {
		t.Fatal(err)
	}
	if report.Bundles != 1 || len(report.Problems) != 0 {
		t.Fatalf("Verify=%+v", report)
	}
}

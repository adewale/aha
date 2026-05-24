package depot_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/adewale/aha/internal/adapters"
	"github.com/adewale/aha/internal/archive"
	"github.com/adewale/aha/internal/config"
	"github.com/adewale/aha/internal/depot"
	"github.com/adewale/aha/internal/model"
	"github.com/adewale/aha/internal/testutil"
)

func TestParseAddressDefaults(t *testing.T) {
	tests := []struct {
		in       string
		wantType string
		wantLoc  string
	}{
		{"", "local", "~/.aha/depot"},
		{"r2", "r2", "aha-depot"},
		{"r2:aha-archive", "r2", "aha-archive"},
		{"local:/tmp/aha", "local", "/tmp/aha"},
		{"/tmp/aha", "local", "/tmp/aha"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := depot.ParseAddress(tt.in)
			if err != nil {
				t.Fatal(err)
			}
			if got.Type != tt.wantType || got.Location != tt.wantLoc {
				t.Fatalf("ParseAddress(%q)=%+v want type=%q location=%q", tt.in, got, tt.wantType, tt.wantLoc)
			}
		})
	}
}

func TestLocalDepotPutListFetchVerifyRepair(t *testing.T) {
	root := t.TempDir()
	bundlePath := writeDepotTestBundle(t, filepath.Join(root, "src"))
	d, err := depot.NewLocal(filepath.Join(root, "depot"))
	if err != nil {
		t.Fatal(err)
	}
	ref, created, err := d.PutBundle(t.Context(), bundlePath)
	if err != nil {
		t.Fatal(err)
	}
	if !created || ref.BundleSHA256 == "" || ref.MachineID != "m1" || ref.Key != depot.BundleKey(ref.BundleSHA256) {
		t.Fatalf("bad put ref created=%v ref=%+v", created, ref)
	}
	ref2, created, err := d.PutBundle(t.Context(), bundlePath)
	if err != nil {
		t.Fatal(err)
	}
	if created || ref2.BundleSHA256 != ref.BundleSHA256 {
		t.Fatalf("second put created=%v ref=%+v want existing %s", created, ref2, ref.BundleSHA256)
	}
	refs, err := d.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || refs[0].BundleSHA256 != ref.BundleSHA256 {
		t.Fatalf("List=%+v want one ref", refs)
	}
	fetched := filepath.Join(root, "fetched.tar.zst")
	if err := d.Fetch(t.Context(), ref, fetched); err != nil {
		t.Fatal(err)
	}
	if _, err := archive.ReadManifest(fetched); err != nil {
		t.Fatalf("fetched bundle invalid: %v", err)
	}
	report, err := d.Verify(t.Context(), true)
	if err != nil {
		t.Fatal(err)
	}
	if report.Bundles != 1 || !report.Repaired || len(report.Problems) != 0 {
		t.Fatalf("Verify repair report=%+v", report)
	}
}

func TestLocalDepotInitRejectsInvalidMarker(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "depot"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "depot", "depot.json"), []byte(`{"schema":"wrong","layout":"v1"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := depot.NewLocal(filepath.Join(root, "depot"))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Init(t.Context()); err == nil {
		t.Fatal("expected invalid depot marker error")
	}
}

func TestLocalDepotVerifyRepairsMissingMarker(t *testing.T) {
	root := t.TempDir()
	bundlePath := writeDepotTestBundle(t, filepath.Join(root, "src"))
	d, err := depot.NewLocal(filepath.Join(root, "depot"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := d.PutBundle(t.Context(), bundlePath); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, "depot", "depot.json")
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	report, err := d.Verify(t.Context(), true)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Repaired || !strings.Contains(strings.Join(report.Problems, "\n"), "missing depot marker") {
		t.Fatalf("expected repaired missing marker, got %+v", report)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("marker not repaired: %v", err)
	}
}

func TestLocalDepotRejectsUnsafeCatalogBundleKeys(t *testing.T) {
	d, err := depot.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	err = d.Fetch(t.Context(), depot.BundleRef{BundleSHA256: strings.Repeat("a", 64), Key: "../escape.tar.zst"}, filepath.Join(t.TempDir(), "out.tar.zst"))
	if err == nil {
		t.Fatal("expected unsafe key rejection")
	}
}

func TestLocalDepotVerifyReportsCatalogAndKeyMismatches(t *testing.T) {
	root := t.TempDir()
	bundlePath := writeDepotTestBundle(t, filepath.Join(root, "src"))
	d, err := depot.NewLocal(filepath.Join(root, "depot"))
	if err != nil {
		t.Fatal(err)
	}
	ref, _, err := d.PutBundle(t.Context(), bundlePath)
	if err != nil {
		t.Fatal(err)
	}
	badPath := filepath.Join(root, "depot", "bundles", "v1", strings.Repeat("b", 64)+".tar.zst")
	if err := os.Link(filepath.Join(root, "depot", filepath.FromSlash(ref.Key)), badPath); err != nil {
		t.Fatal(err)
	}
	uncatalogedPath := writeDepotTestBundleWithID(t, filepath.Join(root, "src-uncataloged"), "uncataloged")
	uncatalogedRef, err := depot.BundleRefFromPath(uncatalogedPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Link(uncatalogedPath, filepath.Join(root, "depot", filepath.FromSlash(uncatalogedRef.Key))); err != nil {
		t.Fatal(err)
	}
	report, err := d.Verify(t.Context(), false)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(report.Problems, "\n")
	if !strings.Contains(joined, "key/content sha mismatch") || !strings.Contains(joined, "catalog missing bundle") {
		t.Fatalf("expected key and catalog problems, got %+v", report)
	}
}

func TestLocalDepotConcurrentCatalogUpdatesDoNotLoseRefs(t *testing.T) {
	root := t.TempDir()
	var bundles []string
	for i := 0; i < 4; i++ {
		bundles = append(bundles, writeDepotTestBundleWithID(t, filepath.Join(root, fmt.Sprintf("src-%d", i)), fmt.Sprintf("bundle-%d", i)))
	}
	d, err := depot.NewLocal(filepath.Join(root, "depot"))
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, len(bundles))
	for _, path := range bundles {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := d.PutBundle(t.Context(), path)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	refs, err := d.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != len(bundles) {
		t.Fatalf("catalog has %d refs, want %d: %+v", len(refs), len(bundles), refs)
	}
}

func writeDepotTestBundle(t *testing.T, root string) string {
	return writeDepotTestBundleWithID(t, root, "depot-test")
}

func writeDepotTestBundleWithID(t *testing.T, root, bundleID string) string {
	t.Helper()
	fx := testutil.WriteAgentFixtures(t, root)
	cfg := config.Default()
	cfg.MachineID = "m1"
	cfg.Sources = []model.SourceConfig{{Type: "pi", Root: fx.PiRoot, Enabled: true}}
	b, err := archive.Capture(t.Context(), cfg, adapters.Builtins(), archive.Options{CapturedAt: "2026-01-03T00:00:00Z", BundleID: bundleID})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "bundle.tar.zst")
	if _, err := archive.Write(path, b); err != nil {
		t.Fatal(err)
	}
	return path
}

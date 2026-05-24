package depot_test

import (
	"path/filepath"
	"testing"
	"testing/quick"

	"github.com/adewale/aha/internal/archive"
	"github.com/adewale/aha/internal/depot"
	"github.com/adewale/aha/internal/hash"
	"github.com/adewale/aha/internal/model"
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

func TestDepotDriverPutFetchListProperties(t *testing.T) {
	drivers := map[string]func(*testing.T) depot.Driver{
		"local": func(t *testing.T) depot.Driver {
			d, err := depot.NewLocal(filepath.Join(t.TempDir(), "depot"))
			if err != nil {
				t.Fatal(err)
			}
			return d
		},
		"r2-fake": func(t *testing.T) depot.Driver {
			fake := newFakeS3(t)
			t.Cleanup(fake.Close)
			return fake.Depot("bucket")
		},
	}
	for name, open := range drivers {
		t.Run(name, func(t *testing.T) {
			prop := func(a, b []byte) bool {
				if len(a)+len(b) > 2048 {
					return true
				}
				d := open(t)
				if err := d.Init(t.Context()); err != nil {
					return false
				}
				bundlePath := writeDirectBundle(t, filepath.Join(t.TempDir(), "bundle.tar.zst"), a, b)
				wantSHA, err := archive.FileSHA256(bundlePath)
				if err != nil {
					return false
				}
				ref, created, err := d.PutBundle(t.Context(), bundlePath)
				if err != nil || !created || ref.BundleSHA256 != wantSHA || ref.Key != depot.BundleKey(wantSHA) {
					return false
				}
				ref2, created, err := d.PutBundle(t.Context(), bundlePath)
				if err != nil || created || ref2.BundleSHA256 != ref.BundleSHA256 {
					return false
				}
				refs, err := d.List(t.Context())
				if err != nil || !containsRefSHA(refs, wantSHA) {
					return false
				}
				fetched := filepath.Join(t.TempDir(), "fetched.tar.zst")
				if err := d.Fetch(t.Context(), ref, fetched); err != nil {
					return false
				}
				got, err := archive.FileSHA256(fetched)
				if err != nil || got != wantSHA {
					return false
				}
				report, err := d.Verify(t.Context(), false)
				if err != nil || len(report.Problems) != 0 || report.Bundles == 0 {
					return false
				}
				return true
			}
			if err := quick.Check(prop, &quick.Config{MaxCount: 12}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestR2ConcurrentPutMergeProperty(t *testing.T) {
	prop := func(a, b []byte) bool {
		if len(a)+len(b) > 2048 {
			return true
		}
		fake := newFakeS3(t)
		defer fake.Close()
		d := fake.Depot("bucket")
		if err := d.Init(t.Context()); err != nil {
			return false
		}
		pathA := writeDirectBundleWithID(t, filepath.Join(t.TempDir(), "a.tar.zst"), "a", a, nil)
		pathB := writeDirectBundleWithID(t, filepath.Join(t.TempDir(), "b.tar.zst"), "b", b, nil)
		errCh := make(chan error, 2)
		go func() { _, _, err := d.PutBundle(t.Context(), pathA); errCh <- err }()
		go func() { _, _, err := d.PutBundle(t.Context(), pathB); errCh <- err }()
		if err := <-errCh; err != nil {
			return false
		}
		if err := <-errCh; err != nil {
			return false
		}
		shaA, _ := archive.FileSHA256(pathA)
		shaB, _ := archive.FileSHA256(pathB)
		refs, err := d.List(t.Context())
		if err != nil {
			return false
		}
		want := map[string]bool{shaA: true, shaB: true}
		if len(refs) != len(want) {
			return false
		}
		for _, ref := range refs {
			if !want[ref.BundleSHA256] {
				return false
			}
		}
		return true
	}
	if err := quick.Check(prop, &quick.Config{MaxCount: 10}); err != nil {
		t.Fatal(err)
	}
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

func writeDirectBundle(t *testing.T, path string, a, b []byte) string {
	return writeDirectBundleWithID(t, path, "prop", a, b)
}

func writeDirectBundleWithID(t *testing.T, path, bundleID string, a, b []byte) string {
	t.Helper()
	if a == nil {
		a = []byte{}
	}
	if b == nil {
		b = []byte{}
	}
	files := []struct {
		name string
		data []byte
	}{
		{name: "sources/pi/sessions/a.jsonl", data: a},
		{name: "sources/pi/sessions/b.jsonl", data: b},
	}
	manifest := model.Manifest{Schema: model.BundleSchema, BundleID: bundleID, MachineID: "m1", CapturedAt: "2026-01-01T00:00:00Z", Policy: model.ManifestPolicy{IncludeImages: true}}
	var captured []model.CapturedFile
	for _, f := range files {
		mf := model.ManifestFile{Source: "pi", Kind: "session", RelativePath: f.name, SHA256: hash.SHA256Bytes(f.data), Bytes: int64(len(f.data)), CopyState: "stable"}
		manifest.Files = append(manifest.Files, mf)
		captured = append(captured, model.CapturedFile{Manifest: mf, Data: f.data})
	}
	if _, err := archive.Write(path, archive.Bundle{Manifest: manifest, Files: captured}); err != nil {
		t.Fatal(err)
	}
	return path
}

func containsRefSHA(refs []depot.BundleRef, sha string) bool {
	for _, ref := range refs {
		if ref.BundleSHA256 == sha {
			return true
		}
	}
	return false
}

package depot_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/adewale/aha/internal/archive"
	"github.com/adewale/aha/internal/depot"
	"github.com/adewale/aha/internal/hash"
	"github.com/adewale/aha/internal/model"
)

func BenchmarkLocalDepotPutBundle(b *testing.B) {
	d, err := depot.NewLocal(filepath.Join(b.TempDir(), "depot"))
	if err != nil {
		b.Fatal(err)
	}
	if err := d.Init(context.Background()); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		bundlePath := writeDepotBenchBundle(b, filepath.Join(b.TempDir(), fmt.Sprintf("bundle-%d", i)), fmt.Sprintf("depot-bench-%d", i), 4<<10)
		b.StartTimer()
		if _, _, err := d.PutBundle(context.Background(), bundlePath); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLocalDepotList(b *testing.B) {
	d := seededLocalDepot(b, 50)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := d.List(context.Background()); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLocalDepotVerify(b *testing.B) {
	d := seededLocalDepot(b, 25)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := d.Verify(context.Background(), false); err != nil {
			b.Fatal(err)
		}
	}
}

func seededLocalDepot(tb testing.TB, bundles int) *depot.Local {
	tb.Helper()
	d, err := depot.NewLocal(filepath.Join(tb.TempDir(), "depot"))
	if err != nil {
		tb.Fatal(err)
	}
	if err := d.Init(context.Background()); err != nil {
		tb.Fatal(err)
	}
	for i := 0; i < bundles; i++ {
		bundlePath := writeDepotBenchBundle(tb, filepath.Join(tb.TempDir(), fmt.Sprintf("bundle-%d", i)), fmt.Sprintf("depot-bench-%d", i), 1024)
		if _, _, err := d.PutBundle(context.Background(), bundlePath); err != nil {
			tb.Fatal(err)
		}
	}
	return d
}

func writeDepotBenchBundle(tb testing.TB, root, bundleID string, size int) string {
	tb.Helper()
	data := bytes.Repeat([]byte("x"), size)
	mf := model.ManifestFile{Source: "pi", Kind: "session", RelativePath: "sources/pi/sessions/bench.jsonl", RawPath: "bench.jsonl", SHA256: hash.SHA256Bytes(data), Bytes: int64(len(data)), SessionID: bundleID, CopyState: "stable"}
	manifest := model.Manifest{Schema: model.BundleSchema, BundleID: bundleID, MachineID: "bench-machine", CapturedAt: "2026-01-01T00:00:00Z", Policy: model.ManifestPolicy{PathMode: "raw", IncludeSubagents: true, IncludeImages: true, Redaction: "none-v1"}, Counts: model.ManifestCounts{SessionFiles: 1, BytesUncompressed: int64(size)}, Files: []model.ManifestFile{mf}}
	path := filepath.Join(root, "bench.tar.zst")
	if err := ensureDir(filepath.Dir(path)); err != nil {
		tb.Fatal(err)
	}
	if _, err := archive.Write(path, archive.Bundle{Manifest: manifest, Files: []model.CapturedFile{{Manifest: mf, Data: data}}}); err != nil {
		tb.Fatal(err)
	}
	return path
}

func ensureDir(path string) error {
	return os.MkdirAll(path, 0o755)
}

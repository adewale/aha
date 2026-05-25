package archive_test

import (
	"bytes"
	"fmt"
	"io"
	"path/filepath"
	"testing"

	"github.com/adewale/aha/internal/archive"
	"github.com/adewale/aha/internal/hash"
	"github.com/adewale/aha/internal/model"
)

func BenchmarkWrite(b *testing.B) {
	for _, tc := range []struct {
		name      string
		files     int
		bytesEach int
	}{
		{name: "100x4KiB", files: 100, bytesEach: 4 << 10},
		{name: "10x1MiB", files: 10, bytesEach: 1 << 20},
	} {
		b.Run(tc.name, func(b *testing.B) {
			bundle, totalBytes := archiveBenchBundle(tc.files, tc.bytesEach)
			b.ReportAllocs()
			b.SetBytes(totalBytes)
			for i := 0; i < b.N; i++ {
				path := filepath.Join(b.TempDir(), fmt.Sprintf("bundle-%d.tar.zst", i))
				if _, err := archive.Write(path, bundle); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkReadManifest(b *testing.B) {
	bundle, totalBytes := archiveBenchBundle(100, 4<<10)
	path := filepath.Join(b.TempDir(), "bundle.tar.zst")
	if _, err := archive.Write(path, bundle); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.SetBytes(totalBytes)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := archive.ReadManifest(path); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkStreamManifestFiles(b *testing.B) {
	bundle, totalBytes := archiveBenchBundle(100, 4<<10)
	path := filepath.Join(b.TempDir(), "bundle.tar.zst")
	if _, err := archive.Write(path, bundle); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.SetBytes(totalBytes)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := archive.StreamManifestFiles(path, func(model.ManifestFile, io.Reader) error { return nil }); err != nil {
			b.Fatal(err)
		}
	}
}

func archiveBenchBundle(files, bytesEach int) (archive.Bundle, int64) {
	captured := make([]model.CapturedFile, 0, files)
	manifestFiles := make([]model.ManifestFile, 0, files)
	var total int64
	for i := 0; i < files; i++ {
		data := bytes.Repeat([]byte{byte('a' + i%26)}, bytesEach)
		rel := fmt.Sprintf("sources/pi/sessions/bench-%05d.jsonl", i)
		mf := model.ManifestFile{Source: "pi", Kind: "session", RelativePath: rel, RawPath: rel, SHA256: hash.SHA256Bytes(data), Bytes: int64(len(data)), SessionID: fmt.Sprintf("bench-%05d", i), CopyState: "stable"}
		manifestFiles = append(manifestFiles, mf)
		captured = append(captured, model.CapturedFile{Manifest: mf, Data: data})
		total += int64(len(data))
	}
	manifest := model.Manifest{Schema: model.BundleSchema, BundleID: "archive-bench", MachineID: "bench-machine", CapturedAt: "2026-01-01T00:00:00Z", Policy: model.ManifestPolicy{PathMode: "raw", IncludeSubagents: true, IncludeImages: true, Redaction: "none-v1"}, Counts: model.ManifestCounts{SessionFiles: files, BytesUncompressed: total}, Files: manifestFiles}
	return archive.Bundle{Manifest: manifest, Files: captured}, total
}

package archive_test

import (
	"fmt"
	"io"
	"path/filepath"
	"testing"

	"github.com/adewale/aha/internal/archive"
	"github.com/adewale/aha/internal/model"
)

func BenchmarkPathologicalArchiveManyTinyFiles(b *testing.B) {
	bundle, totalBytes := archiveBenchBundle(5000, 64)
	b.Run("write", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(totalBytes)
		for i := 0; i < b.N; i++ {
			path := filepath.Join(b.TempDir(), fmt.Sprintf("many-tiny-%d.tar.zst", i))
			if _, err := archive.Write(path, bundle); err != nil {
				b.Fatal(err)
			}
		}
	})
	path := filepath.Join(b.TempDir(), "many-tiny.tar.zst")
	if _, err := archive.Write(path, bundle); err != nil {
		b.Fatal(err)
	}
	b.Run("read-manifest", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(totalBytes)
		for i := 0; i < b.N; i++ {
			if _, err := archive.ReadManifest(path); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("stream-files", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(totalBytes)
		for i := 0; i < b.N; i++ {
			if err := archive.StreamManifestFiles(path, func(model.ManifestFile, io.Reader) error { return nil }); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkPathologicalArchiveLargeCompressibleFile(b *testing.B) {
	bundle, totalBytes := archiveBenchBundle(1, 32<<20)
	b.ReportAllocs()
	b.SetBytes(totalBytes)
	for i := 0; i < b.N; i++ {
		path := filepath.Join(b.TempDir(), fmt.Sprintf("large-compressible-%d.tar.zst", i))
		if _, err := archive.Write(path, bundle); err != nil {
			b.Fatal(err)
		}
	}
}

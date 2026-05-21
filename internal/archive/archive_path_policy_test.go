package archive_test

import (
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adewale/aha/internal/archive"
	"github.com/adewale/aha/internal/hash"
	"github.com/adewale/aha/internal/model"
)

func TestWriteRejectsUnsafeArchivePaths(t *testing.T) {
	for _, name := range []string{"../evil.jsonl", "/abs.jsonl", "sources/pi/../evil.jsonl", "sources\\pi\\sessions\\a.jsonl", `sources\\pi\\sessions\\a.jsonl`, "manifest.json", "checksums/sha256sums.txt"} {
		t.Run(name, func(t *testing.T) {
			data := []byte("abc")
			mf := model.ManifestFile{Source: "pi", Kind: "session", RelativePath: name, SHA256: hash.SHA256Bytes(data), Bytes: int64(len(data)), CopyState: "stable"}
			bundle := archive.Bundle{Manifest: model.Manifest{Schema: model.BundleSchema, BundleID: "unsafe", MachineID: "m1", CapturedAt: "2026-01-01T00:00:00Z", Policy: model.ManifestPolicy{IncludeImages: true}, Files: []model.ManifestFile{mf}}, Files: []model.CapturedFile{{Manifest: mf, Data: data}}}
			_, err := archive.Write(filepath.Join(t.TempDir(), "bundle.tar.zst"), bundle)
			if err == nil || !strings.Contains(err.Error(), "unsafe archive path") {
				t.Fatalf("Write(%q) err=%v, want unsafe archive path", name, err)
			}
		})
	}
}

func TestWalkBundleRejectsUnsafeManifestPath(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "unsafe.tar.zst")
	data := []byte("abc")
	mf := model.ManifestFile{Source: "pi", Kind: "session", RelativePath: "../evil.jsonl", SHA256: hash.SHA256Bytes(data), Bytes: int64(len(data)), CopyState: "stable"}
	manifest := model.Manifest{Schema: model.BundleSchema, BundleID: "unsafe", MachineID: "m1", CapturedAt: "2026-01-01T00:00:00Z", Policy: model.ManifestPolicy{IncludeImages: true}, Files: []model.ManifestFile{mf}}
	if err := writeTestTarZst(path, manifest, map[string][]byte{mf.RelativePath: data}); err != nil {
		t.Fatal(err)
	}
	err := archive.WalkBundle(path, func(name string, size int64, r io.Reader) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "unsafe archive path") {
		t.Fatalf("WalkBundle err=%v, want unsafe archive path", err)
	}
}

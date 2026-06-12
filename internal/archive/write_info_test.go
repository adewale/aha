package archive_test

import (
	"path/filepath"
	"testing"

	"github.com/adewale/aha/internal/archive"
	"github.com/adewale/aha/internal/hash"
)

func TestWriteWithInfoMatchesIndependentHashes(t *testing.T) {
	bundle, _ := archiveBenchBundle(3, 128)
	path := filepath.Join(t.TempDir(), "bundle.tar.zst")
	info, err := archive.WriteWithInfo(path, bundle)
	if err != nil {
		t.Fatal(err)
	}
	sha, err := archive.FileSHA256(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.BundleSHA256 != sha {
		t.Fatalf("WriteWithInfo sha=%s FileSHA256=%s", info.BundleSHA256, sha)
	}
	manifest, err := archive.ReadManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	mb, err := archive.CanonicalManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if info.ManifestSHA256 != hash.SHA256Bytes(mb) {
		t.Fatalf("manifest sha=%s want %s", info.ManifestSHA256, hash.SHA256Bytes(mb))
	}
	if info.Path != path || info.SizeBytes <= 0 {
		t.Fatalf("bad info: %+v", info)
	}
}

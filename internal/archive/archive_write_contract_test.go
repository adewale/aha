package archive_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/adewale/aha/internal/archive"
	"github.com/adewale/aha/internal/hash"
	"github.com/adewale/aha/internal/model"
)

func TestWriteRejectsManifestFileWithoutCapturedData(t *testing.T) {
	mf := model.ManifestFile{Source: "pi", Kind: "session", RelativePath: "sources/pi/sessions/missing.jsonl", SHA256: hash.SHA256Bytes([]byte("abc")), Bytes: 3, CopyState: "stable"}
	bundle := archive.Bundle{Manifest: model.Manifest{Schema: model.BundleSchema, BundleID: "missing", MachineID: "m1", CapturedAt: "2026-01-01T00:00:00Z", Policy: model.ManifestPolicy{IncludeImages: true}, Files: []model.ManifestFile{mf}}}
	_, err := archive.Write(filepath.Join(t.TempDir(), "bundle.tar.zst"), bundle)
	if err == nil || !strings.Contains(err.Error(), "manifest file has no captured data") {
		t.Fatalf("Write err=%v, want missing captured data error", err)
	}
}

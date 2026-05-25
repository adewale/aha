package archive_test

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/adewale/aha/internal/archive"
	"github.com/adewale/aha/internal/hash"
	"github.com/adewale/aha/internal/model"
)

func TestCanonicalBundleGoldenSHA(t *testing.T) {
	data := []byte(`{"type":"message","id":"e1","message":{"role":"user","content":[{"type":"text","text":"golden needle"}]}}` + "\n")
	mf := model.ManifestFile{Source: "pi", Kind: "session", RelativePath: "sources/pi/sessions/golden.jsonl", RawPath: "golden.jsonl", SHA256: hash.SHA256Bytes(data), Bytes: int64(len(data)), SessionID: "golden-session", CopyState: "stable"}
	manifest := model.Manifest{Schema: model.BundleSchema, BundleID: "golden-bundle", MachineID: "golden-machine", CapturedAt: "2026-01-01T00:00:00Z", Policy: model.ManifestPolicy{PathMode: "raw", IncludeSubagents: true, IncludeImages: true, IndexToolOutput: false, Redaction: "none-v1"}, Files: []model.ManifestFile{mf}}
	path := filepath.Join(t.TempDir(), "golden.tar.zst")
	if _, err := archive.Write(path, archive.Bundle{Manifest: manifest, Files: []model.CapturedFile{{Manifest: mf, Data: data}}}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	got := hex.EncodeToString(sum[:])
	const want = "e99dae6a4a72e249ba8f34e03f0f0732fcbd721bef7bec09d99a4194eebdcf71"
	if got != want {
		t.Fatalf("canonical bundle SHA changed: got %s want %s", got, want)
	}
}

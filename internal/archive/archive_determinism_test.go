package archive_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/adewale/aha/internal/archive"
	"github.com/adewale/aha/internal/hash"
	"github.com/adewale/aha/internal/model"
)

func TestWriteDeterministicIndependentOfInputFileOrder(t *testing.T) {
	mk := func(name, data string) model.CapturedFile {
		mf := model.ManifestFile{Source: "pi", Kind: "session", RelativePath: name, SHA256: hash.SHA256Bytes([]byte(data)), Bytes: int64(len(data)), CopyState: "stable"}
		return model.CapturedFile{Manifest: mf, Data: []byte(data)}
	}
	a := mk("sources/pi/sessions/a.jsonl", "a")
	b := mk("sources/pi/sessions/b.jsonl", "b")
	manifest := model.Manifest{Schema: model.BundleSchema, BundleID: "deterministic", MachineID: "m1", CapturedAt: "2026-01-01T00:00:00Z", Policy: model.ManifestPolicy{IncludeImages: true}, Files: []model.ManifestFile{a.Manifest, b.Manifest}}
	manifestReverse := manifest
	manifestReverse.Files = []model.ManifestFile{b.Manifest, a.Manifest}
	path1 := filepath.Join(t.TempDir(), "one.tar.zst")
	path2 := filepath.Join(t.TempDir(), "two.tar.zst")
	if _, err := archive.Write(path1, archive.Bundle{Manifest: manifest, Files: []model.CapturedFile{a, b}}); err != nil {
		t.Fatal(err)
	}
	if _, err := archive.Write(path2, archive.Bundle{Manifest: manifestReverse, Files: []model.CapturedFile{b, a}}); err != nil {
		t.Fatal(err)
	}
	bytes1, err := os.ReadFile(path1)
	if err != nil {
		t.Fatal(err)
	}
	bytes2, err := os.ReadFile(path2)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bytes1, bytes2) {
		t.Fatalf("archive bytes differ for equivalent file sets")
	}
}

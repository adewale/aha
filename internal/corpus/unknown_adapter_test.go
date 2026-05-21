package corpus_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/adewale/aha/internal/archive"
	"github.com/adewale/aha/internal/corpus"
	"github.com/adewale/aha/internal/hash"
	"github.com/adewale/aha/internal/model"
)

func TestIngestUnknownSourceAdapterReturnsExplicitError(t *testing.T) {
	data := []byte(`{"type":"session","id":"s"}` + "\n")
	mf := model.ManifestFile{Source: "unknown", Kind: "session", RelativePath: "sources/unknown/sessions/s.jsonl", SHA256: hash.SHA256Bytes(data), Bytes: int64(len(data)), CopyState: "stable"}
	bundle := archive.Bundle{Manifest: model.Manifest{Schema: model.BundleSchema, BundleID: "unknown-adapter", MachineID: "m1", CapturedAt: "2026-01-01T00:00:00Z", Policy: model.ManifestPolicy{IncludeImages: true}, Files: []model.ManifestFile{mf}}, Files: []model.CapturedFile{{Manifest: mf, Data: data}}}
	bundlePath := filepath.Join(t.TempDir(), "bundle.tar.zst")
	if _, err := archive.Write(bundlePath, bundle); err != nil {
		t.Fatal(err)
	}
	store, err := corpus.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	_, err = corpus.IngestBundle(store, nil, bundlePath)
	if err == nil || !strings.Contains(err.Error(), `unknown source adapter "unknown"`) {
		t.Fatalf("IngestBundle err=%v, want explicit unknown adapter", err)
	}
}

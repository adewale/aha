package archive_test

import (
	"testing"
	"testing/quick"

	"github.com/adewale/aha/internal/archive"
	"github.com/adewale/aha/internal/hash"
	"github.com/adewale/aha/internal/model"
)

func TestManifestStateSignatureProperty(t *testing.T) {
	prop := func(data string, bundleID string, capturedAt string) bool {
		m := model.Manifest{
			Schema:     model.BundleSchema,
			BundleID:   "base",
			MachineID:  "m1",
			CapturedAt: "2026-01-01T00:00:00Z",
			Policy:     model.ManifestPolicy{PathMode: "raw", IncludeSubagents: true, IncludeImages: true, Redaction: "none-v1"},
			Files:      []model.ManifestFile{{Source: "pi", Kind: "session", RelativePath: "sources/pi/sessions/a.jsonl", SHA256: hash.SHA256Bytes([]byte(data)), Bytes: int64(len(data)), SessionID: "s1"}},
		}
		changedMetadata := m
		changedMetadata.BundleID = bundleID
		changedMetadata.CapturedAt = capturedAt
		if archive.ManifestStateSHA256(m) != archive.ManifestStateSHA256(changedMetadata) {
			return false
		}
		changedContent := m
		changedContent.Files = append([]model.ManifestFile(nil), m.Files...)
		changedContent.Files[0].SHA256 = hash.SHA256Bytes([]byte(data + "x"))
		changedContent.Files[0].Bytes++
		if archive.ManifestStateSHA256(m) == archive.ManifestStateSHA256(changedContent) {
			return false
		}
		return true
	}
	if err := quick.Check(prop, &quick.Config{MaxCount: 100}); err != nil {
		t.Fatal(err)
	}
}

package archive_test

import (
	"testing"

	"github.com/adewale/aha/internal/archive"
	"github.com/adewale/aha/internal/model"
)

func TestManifestStateSHAIgnoresBundleMetadata(t *testing.T) {
	base := model.Manifest{
		Schema:     model.BundleSchema,
		BundleID:   "one",
		MachineID:  "m1",
		CapturedAt: "2026-01-01T00:00:00Z",
		Policy:     model.ManifestPolicy{PathMode: "raw", IncludeSubagents: true, IncludeImages: true, Redaction: "none-v1"},
		Files:      []model.ManifestFile{{Source: "pi", Kind: "session", RelativePath: "sources/pi/sessions/a.jsonl", SHA256: "abc", Bytes: 3, SessionID: "s1"}},
	}
	changedMeta := base
	changedMeta.BundleID = "two"
	changedMeta.CapturedAt = "2026-01-02T00:00:00Z"
	if archive.ManifestStateSHA256(base) != archive.ManifestStateSHA256(changedMeta) {
		t.Fatal("state signature changed for bundle_id/captured_at only")
	}
	changedContent := base
	changedContent.Files = append([]model.ManifestFile(nil), base.Files...)
	changedContent.Files[0].SHA256 = "def"
	if archive.ManifestStateSHA256(base) == archive.ManifestStateSHA256(changedContent) {
		t.Fatal("state signature ignored file content")
	}
}

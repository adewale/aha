package archive_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/adewale/aha/internal/archive"
	"github.com/adewale/aha/internal/hash"
	"github.com/adewale/aha/internal/model"
)

func TestWriteRejectsSemanticallyInvalidManifest(t *testing.T) {
	validData := []byte("{}\n")
	validMF := model.ManifestFile{Source: "pi", Kind: "session", RelativePath: "sources/pi/sessions/s.jsonl", SHA256: hash.SHA256Bytes(validData), Bytes: int64(len(validData)), CopyState: "stable"}
	base := model.Manifest{Schema: model.BundleSchema, BundleID: "valid", MachineID: "m1", CapturedAt: "2026-01-01T00:00:00Z", Policy: model.ManifestPolicy{IncludeImages: true}, Files: []model.ManifestFile{validMF}}
	tests := []struct {
		name string
		edit func(*model.Manifest)
	}{
		{"schema", func(m *model.Manifest) { m.Schema = "" }},
		{"pre-release v1 schema", func(m *model.Manifest) { m.Schema = "agent-session-snapshot-bundle/v1" }},
		{"bundle", func(m *model.Manifest) { m.BundleID = "" }},
		{"machine", func(m *model.Manifest) { m.MachineID = "" }},
		{"captured", func(m *model.Manifest) { m.CapturedAt = "" }},
		{"file source", func(m *model.Manifest) { m.Files[0].Source = "" }},
		{"file kind", func(m *model.Manifest) { m.Files[0].Kind = "" }},
		{"source path mismatch", func(m *model.Manifest) { m.Files[0].RelativePath = "sources/claude-code/sessions/s.jsonl" }},
		{"kind path mismatch", func(m *model.Manifest) { m.Files[0].Kind = "artifact" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manifest := base
			manifest.Files = append([]model.ManifestFile(nil), base.Files...)
			tt.edit(&manifest)
			bundle := archive.Bundle{Manifest: manifest, Files: []model.CapturedFile{{Manifest: manifest.Files[0], Data: validData}}}
			_, err := archive.Write(filepath.Join(t.TempDir(), "bundle.tar.zst"), bundle)
			if err == nil || !strings.Contains(err.Error(), "invalid manifest") {
				t.Fatalf("Write err=%v, want invalid manifest", err)
			}
		})
	}
}

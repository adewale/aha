package depot_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adewale/aha/internal/depot"
	"github.com/adewale/aha/internal/model"
)

func TestArchiveCapabilitiesRequireInitialisedMarker(t *testing.T) {
	archive, err := depot.NewLocalV2(filepath.Join(t.TempDir(), "archive"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := archive.PrepareUpload(t.Context()); err == nil {
		t.Fatal("PrepareUpload succeeded before archive init")
	}
	if err := archive.Init(t.Context()); err != nil {
		t.Fatal(err)
	}
	reader, err := archive.PreparePull(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	binding, err := reader.ArchiveBinding()
	if err != nil {
		t.Fatal(err)
	}
	if !binding.Valid() || binding.Identity() == "" || binding.Address() == "" {
		t.Fatalf("binding=%+v", binding)
	}
	writer, err := archive.PrepareUpload(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if state, err := writer.ArchiveState(); err != nil || state != model.ArchiveEmpty {
		t.Fatalf("writer state=%s err=%v", state, err)
	}
}

func TestPrepareUploadRejectsArchiveWithMissingLatestManifest(t *testing.T) {
	root := filepath.Join(t.TempDir(), "archive")
	archive, err := depot.NewLocalV2(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := archive.Init(t.Context()); err != nil {
		t.Fatal(err)
	}
	index, err := depot.EncodeMachinesIndex([]string{"damaged-machine"})
	if err != nil {
		t.Fatal(err)
	}
	sha, err := model.NewManifestSHA256(strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	latest, err := depot.EncodeLatestPointer(sha)
	if err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string][]byte{
		filepath.Join(root, "aha-v3", depot.MachinesIndexKey):                    index,
		filepath.Join(root, "aha-v3", depot.LatestPointerKey("damaged-machine")): latest,
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writer, err := archive.PrepareUpload(t.Context())
	if err != nil {
		t.Fatalf("marker/index preflight failed before publishing machine was known: %v", err)
	}
	manifest := model.SnapshotManifest{
		Schema: model.SnapshotManifestSchema, RequiredFeatures: model.RequiredSnapshotFeatures(), MachineID: "damaged-machine", CapturedAt: "2026-01-01T00:00:00Z",
		Policy: model.ManifestPolicy{PathMode: "raw", Redaction: "none-v1"}, Adapters: []model.ManifestAdapt{}, Files: []model.ManifestFile{},
	}
	if _, err := writer.Push(t.Context(), manifest, newMapBlobSource(t, nil), depot.PushOptions{}); err == nil || !strings.Contains(err.Error(), "damaged") {
		t.Fatalf("target-scoped upload error=%v, want damaged Archive rejection", err)
	}
}

func TestArchiveMarkerDeclaresFormatAndRequiredFeatures(t *testing.T) {
	root := filepath.Join(t.TempDir(), "archive")
	archive, err := depot.NewLocalV2(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := archive.Init(t.Context()); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, depot.MarkerObjectKey))
	if err != nil {
		t.Fatal(err)
	}
	var marker struct {
		FormatMajor      int      `json:"format_major"`
		FormatMinor      int      `json:"format_minor"`
		RequiredFeatures []string `json:"required_features"`
	}
	if err := json.Unmarshal(b, &marker); err != nil {
		t.Fatal(err)
	}
	if marker.FormatMajor != 3 || marker.FormatMinor != 0 || len(marker.RequiredFeatures) < 2 {
		t.Fatalf("marker compatibility declaration=%+v", marker)
	}
	foundFence := false
	for _, feature := range marker.RequiredFeatures {
		foundFence = foundFence || feature == depot.ArchiveWriterFenceFeature
	}
	if !foundFence {
		t.Fatalf("marker does not fence pre-0.2 writers: %+v", marker)
	}
}

func TestCurrentArchiveMarkerIsRejectedByFrozenNMinusOneFeatureSet(t *testing.T) {
	root := filepath.Join(t.TempDir(), "archive")
	archive, err := depot.NewLocalV2(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := archive.Init(t.Context()); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, depot.MarkerObjectKey))
	if err != nil {
		t.Fatal(err)
	}
	var marker struct {
		Schema           string   `json:"schema"`
		RequiredFeatures []string `json:"required_features"`
	}
	if err := json.Unmarshal(b, &marker); err != nil {
		t.Fatal(err)
	}
	// The immediately preceding writer accepts only the exact v2 marker schema.
	// The v3 schema therefore fences it before it can issue a write capability.
	if marker.Schema == depot.MarkerSchemaV2 {
		t.Fatalf("current marker schema %q would be accepted by the N-1 writer", marker.Schema)
	}
}

func TestPreWriterFenceArchiveIsRejectedByCurrentBinary(t *testing.T) {
	root := filepath.Join(t.TempDir(), "archive")
	archive, err := depot.NewLocalV2(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := archive.Init(t.Context()); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, depot.MarkerObjectKey)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var marker map[string]any
	if err := json.Unmarshal(b, &marker); err != nil {
		t.Fatal(err)
	}
	marker["schema"] = depot.MarkerSchemaV2
	marker["layout"] = depot.LayoutVersionV2
	marker["format_major"] = float64(2)
	marker["format_minor"] = float64(0)
	marker["required_features"] = []any{"full-manifest-v2"}
	b, _ = json.Marshal(marker)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := archive.PreparePull(t.Context()); err != nil {
		t.Fatalf("legacy Archive read capability rejected: %v", err)
	}
	_, err = archive.PrepareUpload(t.Context())
	var upgrade *depot.ArchiveWriteUpgradeRequiredError
	if !errors.As(err, &upgrade) {
		t.Fatalf("legacy Archive write error=%T %v, want ArchiveWriteUpgradeRequiredError", err, err)
	}
}

func TestLegacyMarkerAndV2SnapshotRemainDownloadable(t *testing.T) {
	root := filepath.Join(t.TempDir(), "archive")
	archive, err := depot.NewLocalV2(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := archive.Init(t.Context()); err != nil {
		t.Fatal(err)
	}
	markerPath := filepath.Join(root, depot.MarkerObjectKey)
	markerBytes, _ := os.ReadFile(markerPath)
	var marker map[string]any
	_ = json.Unmarshal(markerBytes, &marker)
	marker["schema"], marker["layout"] = depot.MarkerSchemaV2, depot.LayoutVersionV2
	marker["format_major"], marker["format_minor"] = float64(2), float64(0)
	marker["required_features"] = []any{"full-manifest-v2"}
	markerBytes, _ = json.Marshal(marker)
	if err := os.WriteFile(markerPath, markerBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := model.SnapshotManifest{
		Schema: model.SnapshotManifestSchemaV2, MachineID: "legacy-machine", CapturedAt: "2026-01-01T00:00:00Z",
		Policy: model.ManifestPolicy{PathMode: "raw", Redaction: "none-v1"}, Adapters: []model.ManifestAdapt{}, Files: []model.ManifestFile{},
	}
	canonical, sha, err := model.EncodeSnapshotManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	index, _ := depot.EncodeMachinesIndex([]string{"legacy-machine"})
	latest, _ := depot.EncodeLatestPointer(sha)
	for path, body := range map[string][]byte{
		filepath.Join(root, depot.MachinesIndexKey):                         index,
		filepath.Join(root, depot.LatestPointerKey("legacy-machine")):       latest,
		filepath.Join(root, depot.ManifestObjectKey("legacy-machine", sha)): canonical,
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	reader, err := archive.PreparePull(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	download, err := reader.PlanDownload(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	plan, err := download.PrepareMaterialisation(t.Context(), nil, map[string]string{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer plan.Close()
	seen := false
	if err := plan.ForEachManifest(t.Context(), func(_ string, prepared model.PreparedSnapshot, _ func(model.BlobKey) (io.ReadCloser, error)) error {
		seen = prepared.Manifest().Schema == model.SnapshotManifestSchemaV2
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !seen {
		t.Fatal("legacy v2 snapshot was not materialised")
	}
}

func TestBaselineAndFutureArchiveFormatMajorsAreRejected(t *testing.T) {
	for _, major := range []int{0, 4} {
		t.Run(fmt.Sprintf("major-%d", major), func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "archive")
			archive, err := depot.NewLocalV2(root)
			if err != nil {
				t.Fatal(err)
			}
			if err := archive.Init(t.Context()); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, depot.MarkerObjectKey)
			b, _ := os.ReadFile(path)
			var marker map[string]any
			_ = json.Unmarshal(b, &marker)
			marker["format_major"] = float64(major)
			b, _ = json.Marshal(marker)
			if err := os.WriteFile(path, b, 0o600); err != nil {
				t.Fatal(err)
			}
			_, err = archive.PreparePull(t.Context())
			var unsupported *depot.UnsupportedArchiveFormatError
			if !errors.As(err, &unsupported) || unsupported.FoundMajor != major {
				t.Fatalf("PreparePull error=%T %v, want major %d format error", err, err, major)
			}
		})
	}
}

func TestFutureArchiveFormatMinorIsRejectedBeforeCapabilityConstruction(t *testing.T) {
	root := filepath.Join(t.TempDir(), "archive")
	archive, err := depot.NewLocalV2(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := archive.Init(t.Context()); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, depot.MarkerObjectKey)
	b, _ := os.ReadFile(path)
	var marker map[string]any
	_ = json.Unmarshal(b, &marker)
	marker["format_minor"] = float64(99)
	b, _ = json.Marshal(marker)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = archive.PreparePull(t.Context())
	var unsupported *depot.UnsupportedArchiveFormatError
	if !errors.As(err, &unsupported) || unsupported.FoundMinor != 99 {
		t.Fatalf("PreparePull error=%T %v, want future minor format error", err, err)
	}
}

func TestUnknownRequiredArchiveFeatureRejectsReadAndWriteCapabilities(t *testing.T) {
	root := filepath.Join(t.TempDir(), "archive")
	archive, err := depot.NewLocalV2(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := archive.Init(t.Context()); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, depot.MarkerObjectKey)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var marker map[string]any
	if err := json.Unmarshal(b, &marker); err != nil {
		t.Fatal(err)
	}
	marker["required_features"] = append(marker["required_features"].([]any), "future-delta-manifest-v9")
	b, err = json.Marshal(marker)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := archive.InspectMetadata(t.Context())
	if err != nil || !report.UpgradeRequired {
		t.Fatalf("InspectMetadata report=%+v err=%v want upgrade required", report, err)
	}
	for name, prepare := range map[string]func() error{
		"read":  func() error { _, err := archive.PreparePull(t.Context()); return err },
		"write": func() error { _, err := archive.PrepareUpload(t.Context()); return err },
		"verify": func() error {
			_, err := archive.Verify(t.Context(), false)
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := prepare()
			var unsupported *depot.UnsupportedArchiveFeatureError
			if !errors.As(err, &unsupported) || unsupported.Feature != "future-delta-manifest-v9" {
				t.Fatalf("prepare error=%v want unsupported feature", err)
			}
		})
	}
}

func TestUnknownOptionalArchiveFeatureIsSafelyIgnored(t *testing.T) {
	root := filepath.Join(t.TempDir(), "archive")
	archive, err := depot.NewLocalV2(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := archive.Init(t.Context()); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, depot.MarkerObjectKey)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var marker map[string]any
	if err := json.Unmarshal(b, &marker); err != nil {
		t.Fatal(err)
	}
	marker["optional_features"] = []string{"future-advisory-metadata-v1"}
	b, _ = json.Marshal(marker)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := archive.PreparePull(t.Context()); err != nil {
		t.Fatalf("optional feature blocked read: %v", err)
	}
	if _, err := archive.PrepareUpload(t.Context()); err != nil {
		t.Fatalf("optional feature blocked write: %v", err)
	}
}

func TestZeroArchiveCapabilitiesAuthoriseNothing(t *testing.T) {
	var reader depot.PreparedPull
	if _, err := reader.ArchiveBinding(); err == nil {
		t.Fatal("zero reader exposed an Archive binding")
	}
	var writer depot.PreparedUpload
	if _, err := writer.ArchiveBinding(); err == nil {
		t.Fatal("zero writer exposed an Archive binding")
	}
	var upload depot.UploadPlan
	if _, err := upload.Execute(t.Context(), depot.PushOptions{}); err == nil {
		t.Fatal("zero upload plan authorised publication")
	}
	var download depot.DownloadPlan
	if _, err := download.LatestVector(); err == nil {
		t.Fatal("zero download plan authorised materialisation")
	}
}

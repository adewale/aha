package depot_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
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
	if marker.FormatMajor != 2 || marker.FormatMinor != 0 || len(marker.RequiredFeatures) == 0 {
		t.Fatalf("marker compatibility declaration=%+v", marker)
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

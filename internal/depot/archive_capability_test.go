package depot_test

import (
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

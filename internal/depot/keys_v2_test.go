package depot_test

import (
	"strings"
	"testing"

	"github.com/adewale/aha/internal/depot"
	"github.com/adewale/aha/internal/model"
)

// TestV2ObjectKeyGoldens locks the depot v2 key layout pre-release
// (docs/depot-v2-spec.md, Phase 0). Once released these keys are sticky:
// changing any golden here is a breaking layout change.
func TestV2ObjectKeyGoldens(t *testing.T) {
	blob, err := model.NewBlobKey(strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := model.NewManifestSHA256(strings.Repeat("b", 64))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := depot.BlobObjectKey(blob), "blobs/v2/"+strings.Repeat("a", 64)+".zst"; got != want {
		t.Fatalf("BlobObjectKey=%q want %q", got, want)
	}
	if got, want := depot.ManifestObjectKey("Work-Mac", manifest), "machines/work-mac/manifests/"+strings.Repeat("b", 64)+".json"; got != want {
		t.Fatalf("ManifestObjectKey=%q want %q", got, want)
	}
	if got, want := depot.LatestPointerKey("Work-Mac"), "machines/work-mac/latest"; got != want {
		t.Fatalf("LatestPointerKey=%q want %q", got, want)
	}
	if got, want := depot.MachinesIndexKey, "machines/index.json"; got != want {
		t.Fatalf("MachinesIndexKey=%q want %q", got, want)
	}
	if got, want := depot.MarkerSchemaV2, "aha-depot/v2"; got != want {
		t.Fatalf("MarkerSchemaV2=%q want %q", got, want)
	}
}

func TestV2MachineKeysSanitizeUnsafeMachineIDs(t *testing.T) {
	manifest, err := model.NewManifestSHA256(strings.Repeat("c", 64))
	if err != nil {
		t.Fatal(err)
	}
	for _, machine := range []string{"../escape", "a/b\\c", "..", "  "} {
		for _, key := range []string{depot.ManifestObjectKey(machine, manifest), depot.LatestPointerKey(machine)} {
			if strings.Contains(key, "..") || strings.Contains(key, "\\") || strings.Contains(key, "//") || strings.HasPrefix(key, "/") {
				t.Fatalf("machine %q produced unsafe key %q", machine, key)
			}
			if !strings.HasPrefix(key, "machines/") {
				t.Fatalf("machine %q escaped the machines/ namespace: %q", machine, key)
			}
		}
	}
}

func TestLatestPointerCodec(t *testing.T) {
	sha, err := model.NewManifestSHA256(strings.Repeat("d", 64))
	if err != nil {
		t.Fatal(err)
	}
	b, err := depot.EncodeLatestPointer(sha)
	if err != nil {
		t.Fatal(err)
	}
	got, err := depot.DecodeLatestPointer(b)
	if err != nil {
		t.Fatal(err)
	}
	if got != sha {
		t.Fatalf("pointer round trip: %s want %s", got, sha)
	}
	if _, err := depot.DecodeLatestPointer([]byte(`{"schema":"wrong","manifest_sha256":"` + strings.Repeat("d", 64) + `"}`)); err == nil {
		t.Fatal("DecodeLatestPointer accepted wrong schema")
	}
	if _, err := depot.DecodeLatestPointer([]byte(`{"schema":"aha-depot-latest/v2","manifest_sha256":"nothex"}`)); err == nil {
		t.Fatal("DecodeLatestPointer accepted invalid sha")
	}
}

func TestMachinesIndexCodec(t *testing.T) {
	b, err := depot.EncodeMachinesIndex([]string{"beta", "alpha", "beta"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := depot.DecodeMachinesIndex(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "alpha" || got[1] != "beta" {
		t.Fatalf("index must be sorted+deduped, got %v", got)
	}
	if _, err := depot.DecodeMachinesIndex([]byte(`{"schema":"wrong","machines":[]}`)); err == nil {
		t.Fatal("DecodeMachinesIndex accepted wrong schema")
	}
	if _, err := depot.EncodeMachinesIndex([]string{""}); err == nil {
		t.Fatal("EncodeMachinesIndex accepted empty machine id")
	}
}

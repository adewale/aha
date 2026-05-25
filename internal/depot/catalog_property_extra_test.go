package depot_test

import (
	"fmt"
	"testing"
	"testing/quick"

	"github.com/adewale/aha/internal/depot"
)

func TestMergeBundleRefsSetSemanticsAndMetadataProperty(t *testing.T) {
	prop := func(inputs []uint8) bool {
		refs := make([]depot.BundleRef, 0, len(inputs)*2)
		want := map[string]depot.BundleRef{}
		for i, raw := range inputs {
			sha := fmt.Sprintf("%064x", int(raw)%32)
			machine := fmt.Sprintf("machine-%02d", int(raw)%5)
			ref := depot.BundleRef{BundleSHA256: sha, Key: depot.BundleKey(sha), MachineID: machine, BundleID: fmt.Sprintf("bundle-%d", i), CapturedAt: fmt.Sprintf("2026-01-01T00:%02d:00Z", int(raw)%60), Bytes: int64(raw) + 1, ManifestSHA256: fmt.Sprintf("%064x", i+1), StateSHA256: fmt.Sprintf("%064x", i+1000)}
			refs = append(refs, ref, depot.BundleRef{BundleSHA256: sha, Key: ref.Key, MachineID: machine})
			if _, ok := want[sha]; !ok {
				want[sha] = ref
			}
		}
		merged := depot.MergeBundleRefs(refs)
		if len(merged) != len(want) {
			return false
		}
		seen := map[string]bool{}
		for _, ref := range merged {
			if seen[ref.BundleSHA256] || ref.Key != depot.BundleKey(ref.BundleSHA256) {
				return false
			}
			seen[ref.BundleSHA256] = true
			w := want[ref.BundleSHA256]
			if ref.BundleID == "" || ref.MachineID == "" || ref.CapturedAt == "" || ref.Bytes == 0 || ref.ManifestSHA256 == "" || ref.StateSHA256 == "" {
				return false
			}
			if ref.BundleID != w.BundleID || ref.ManifestSHA256 != w.ManifestSHA256 || ref.StateSHA256 != w.StateSHA256 {
				return false
			}
		}
		return true
	}
	if err := quick.Check(prop, &quick.Config{MaxCount: 100}); err != nil {
		t.Fatal(err)
	}
}

package depot

import (
	"fmt"
	"testing"
)

func BenchmarkPathologicalCatalogMergeManyTrivialRefs(b *testing.B) {
	refs := trivialCatalogRefs(1000, 4)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		var merged []BundleRef
		for _, ref := range refs {
			merged = mergeBundleRef(merged, ref)
		}
		sortRefs(merged)
		if len(merged) != 1000 {
			b.Fatalf("merged refs=%d, want 1000", len(merged))
		}
	}
}

func trivialCatalogRefs(unique, duplicates int) []BundleRef {
	refs := make([]BundleRef, 0, unique*duplicates)
	for i := 0; i < unique; i++ {
		sha := testSHA(fmt.Sprintf("trivial-bundle-%08d", i))
		ref := BundleRef{BundleSHA256: sha, BundleID: "bundle-" + sha[:8], MachineID: fmt.Sprintf("machine-%02d", i%7), CapturedAt: fmt.Sprintf("2026-01-01T00:%02d:%02dZ", (i/60)%60, i%60), Bytes: 1, Sessions: 1, Key: BundleKey(sha)}
		for j := 0; j < duplicates; j++ {
			refs = append(refs, ref)
		}
	}
	return refs
}

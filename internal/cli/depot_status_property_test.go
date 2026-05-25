package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"testing/quick"

	"github.com/adewale/aha/internal/depot"
)

func TestDepotBehindFromRefsCountsUniqueCatalogMinusCorpusProperty(t *testing.T) {
	prop := func(catalogInputs, duplicateInputs, ingestedInputs []string) bool {
		var refs []depot.BundleRef
		catalogSet := map[string]bool{}
		for _, input := range append(append([]string{}, catalogInputs...), duplicateInputs...) {
			sha := statusPropSHA(input)
			catalogSet[sha] = true
			refs = append(refs, depot.BundleRef{BundleSHA256: sha, Key: depot.BundleKey(sha)})
		}
		// Add duplicates deliberately: many trivial bundle refs should affect metadata cardinality, not work units.
		for _, input := range duplicateInputs {
			sha := statusPropSHA(input)
			refs = append(refs, depot.BundleRef{BundleSHA256: sha, Key: depot.BundleKey(sha)})
		}
		ingested := map[string]bool{}
		for _, input := range ingestedInputs {
			ingested[statusPropSHA(input)] = true
		}
		want := 0
		for sha := range catalogSet {
			if !ingested[sha] {
				want++
			}
		}
		return depotBehindFromRefs(refs, ingested) == want
	}
	if err := quick.Check(prop, &quick.Config{MaxCount: 100}); err != nil {
		t.Fatal(err)
	}
}

func statusPropSHA(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

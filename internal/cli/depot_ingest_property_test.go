package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"testing/quick"

	"github.com/adewale/aha/internal/depot"
)

func TestPendingDepotRefsIsCatalogMinusCorpusProperty(t *testing.T) {
	prop := func(catalogInputs, ingestedInputs []string) bool {
		var refs []depot.BundleRef
		catalogSet := map[string]bool{}
		for _, input := range catalogInputs {
			sha := cliPropSHA(input)
			catalogSet[sha] = true
			refs = append(refs, depot.BundleRef{BundleSHA256: sha, Key: depot.BundleKey(sha)})
		}
		ingested := map[string]bool{}
		for _, input := range ingestedInputs {
			ingested[cliPropSHA(input)] = true
		}
		pending := pendingDepotRefs(refs, ingested)
		seen := map[string]bool{}
		for _, ref := range pending {
			if seen[ref.BundleSHA256] {
				return false
			}
			if !catalogSet[ref.BundleSHA256] || ingested[ref.BundleSHA256] {
				return false
			}
			seen[ref.BundleSHA256] = true
		}
		for sha := range catalogSet {
			if !ingested[sha] && !seen[sha] {
				return false
			}
		}
		return true
	}
	if err := quick.Check(prop, &quick.Config{MaxCount: 100}); err != nil {
		t.Fatal(err)
	}
}

func cliPropSHA(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

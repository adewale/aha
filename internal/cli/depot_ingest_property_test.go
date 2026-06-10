package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"testing/quick"

	"github.com/adewale/aha/internal/depot"
)

func TestPendingDepotRefsIsCatalogMinusCorpusProperty(t *testing.T) {
	// Refs carry their snapshot identity (manifest sha); the corpus set is
	// keyed by the same identity. Refs without identity metadata are always
	// pending (ingest itself dedups by identity).
	prop := func(catalogInputs, ingestedInputs []string) bool {
		var refs []depot.BundleRef
		catalogManifest := map[string]string{}
		for _, input := range catalogInputs {
			sha := cliPropSHA(input)
			manifestSHA := cliPropSHA(input + "-manifest")
			catalogManifest[sha] = manifestSHA
			refs = append(refs, depot.BundleRef{BundleSHA256: sha, Key: depot.BundleKey(sha), ManifestSHA256: manifestSHA})
		}
		ingested := map[string]bool{}
		for _, input := range ingestedInputs {
			ingested[cliPropSHA(input+"-manifest")] = true
		}
		pending := pendingDepotRefs(refs, ingested)
		seen := map[string]bool{}
		for _, ref := range pending {
			if seen[ref.BundleSHA256] {
				return false
			}
			if catalogManifest[ref.BundleSHA256] == "" || ingested[ref.ManifestSHA256] {
				return false
			}
			seen[ref.BundleSHA256] = true
		}
		for sha, manifestSHA := range catalogManifest {
			if !ingested[manifestSHA] && !seen[sha] {
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

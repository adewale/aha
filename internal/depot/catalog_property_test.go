package depot

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"
	"testing"
	"testing/quick"
)

func TestCatalogMergeProperties(t *testing.T) {
	prop := func(inputs []string) bool {
		var refs []BundleRef
		want := map[string]bool{}
		for _, input := range inputs {
			sha := testSHA(input)
			want[sha] = true
			refs = mergeBundleRef(refs, BundleRef{BundleSHA256: sha, BundleID: "bundle-" + sha[:8], MachineID: "machine", CapturedAt: input, Key: BundleKey(sha)})
		}
		// Merging the same refs again is idempotent and preserves one ref per SHA.
		before := append([]BundleRef(nil), refs...)
		for _, ref := range before {
			refs = mergeBundleRef(refs, ref)
		}
		if len(refs) != len(want) {
			return false
		}
		seen := map[string]bool{}
		for _, ref := range refs {
			if seen[ref.BundleSHA256] || !want[ref.BundleSHA256] || ref.Key != BundleKey(ref.BundleSHA256) {
				return false
			}
			seen[ref.BundleSHA256] = true
		}
		sortRefs(refs)
		for i := 1; i < len(refs); i++ {
			if refs[i-1].CapturedAt > refs[i].CapturedAt {
				return false
			}
			if refs[i-1].CapturedAt == refs[i].CapturedAt && refs[i-1].BundleSHA256 > refs[i].BundleSHA256 {
				return false
			}
		}
		return true
	}
	if err := quick.Check(prop, &quick.Config{MaxCount: 100}); err != nil {
		t.Fatal(err)
	}
}

func TestCatalogKeyPathSafetyProperty(t *testing.T) {
	prop := func(machine string) bool {
		key := CatalogKey(machine)
		if !strings.HasPrefix(key, "catalog/v1/") || !strings.HasSuffix(key, ".json") {
			return false
		}
		if strings.Contains(key, "\\") || strings.Contains(key, "..") || filepath.IsAbs(key) || filepath.Clean(key) != filepath.FromSlash(key) {
			return false
		}
		return true
	}
	if err := quick.Check(prop, &quick.Config{MaxCount: 1000}); err != nil {
		t.Fatal(err)
	}
}

func testSHA(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

package depot_test

import (
	"strings"
	"testing"

	"github.com/adewale/aha/internal/depot"
)

func FuzzParseAddress(f *testing.F) {
	for _, seed := range []string{"", "r2", "r2:aha-depot", "local:/tmp/aha", "/tmp/aha", "bad:addr", "local:", "r2:"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		addr, err := depot.ParseAddress(s)
		if err != nil {
			return
		}
		if addr.Type != "local" && addr.Type != "r2" {
			t.Fatalf("unexpected address type: %+v", addr)
		}
		if strings.TrimSpace(addr.Location) == "" {
			t.Fatalf("empty address location for %q: %+v", s, addr)
		}
	})
}

func FuzzValidateBundleKey(f *testing.F) {
	validSHA := strings.Repeat("a", 64)
	for _, seed := range []string{"", "../x", "bundles/v1/" + validSHA + ".tar.zst", "bundles/v1/" + strings.Repeat("g", 64) + ".tar.zst", "/bundles/v1/" + validSHA + ".tar.zst"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, key string) {
		err := depot.ValidateBundleKey(key)
		if err == nil {
			sha := strings.TrimSuffix(strings.TrimPrefix(key, "bundles/v1/"), ".tar.zst")
			if key != depot.BundleKey(sha) {
				t.Fatalf("accepted non-canonical key %q", key)
			}
		}
	})
}

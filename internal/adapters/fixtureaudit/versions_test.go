package fixtureaudit

import (
	"sort"
	"testing"
)

// TestCompareVersionsOrdersReleasesNotStrings is the reason the band gate can
// be trusted. String ordering puts 2.1.206 before 2.1.92, which would let a
// corpus from a newer release read as in-band.
func TestCompareVersionsOrdersReleasesNotStrings(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"2.1.92", "2.1.206", -1},
		{"2.1.206", "2.1.92", 1},
		{"2.1.92", "2.1.92", 0},
		{"0.116.0", "0.130.0", -1},
		{"0.101.0", "0.144.1", -1},
		{"3", "3", 0},
		{"3", "4", -1},
		{"3", "3.1", -1},
		{"1.0", "1.0.0", -1},
		// Producers spell pre-releases in several ways; a numeric component
		// sorts before an alphanumeric one so a release precedes its own
		// release candidate suffix.
		{"1.0.0", "1.0.0rc1", -1},
		{"1.0.0-alpha", "1.0.0-beta", -1},
	}
	for _, tc := range cases {
		got := CompareVersions(tc.a, tc.b)
		if (got < 0) != (tc.want < 0) || (got > 0) != (tc.want > 0) || (got == 0) != (tc.want == 0) {
			t.Fatalf("CompareVersions(%q, %q) = %d, want sign %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestCompareVersionsIsAntisymmetric(t *testing.T) {
	versions := []string{"", "3", "2.1.92", "2.1.206", "0.116.0", "0.130.0", "1.0.0rc1", "1.0.0"}
	for _, a := range versions {
		for _, b := range versions {
			ab, ba := CompareVersions(a, b), CompareVersions(b, a)
			if (ab < 0) != (ba > 0) || (ab == 0) != (ba == 0) {
				t.Fatalf("CompareVersions(%q,%q)=%d but CompareVersions(%q,%q)=%d", a, b, ab, b, a, ba)
			}
		}
	}
}

func TestCompareVersionsSortsAKnownBand(t *testing.T) {
	// The band Letta's published audit reports for Claude Code, which is the
	// external scale our single observation sits below.
	got := []string{"2.1.206", "2.1.92", "2.1.139", "2.1.100"}
	sort.Slice(got, func(i, j int) bool { return CompareVersions(got[i], got[j]) < 0 })
	want := []string{"2.1.92", "2.1.100", "2.1.139", "2.1.206"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sorted band = %v, want %v", got, want)
		}
	}
}

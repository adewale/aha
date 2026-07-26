package fixtureaudit

import (
	"strconv"
	"strings"
)

// CompareVersions orders two version values the way a release sequence reads
// rather than the way strings sort: component-wise, numerically where both
// components are numeric, so 2.1.92 precedes 2.1.206 and 0.116.0 precedes
// 0.130.0. Components are split on the separators producers actually use.
// A shorter version that is otherwise a prefix sorts first, so 3 precedes
// 3.1. It returns a negative number when a precedes b, zero when they are
// equal, and a positive number otherwise.
func CompareVersions(a, b string) int {
	as, bs := splitVersion(a), splitVersion(b)
	for i := 0; i < len(as) && i < len(bs); i++ {
		if c := compareVersionComponent(as[i], bs[i]); c != 0 {
			return c
		}
	}
	return len(as) - len(bs)
}

func splitVersion(v string) []string {
	return strings.FieldsFunc(v, func(r rune) bool {
		return !(r >= '0' && r <= '9') && !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z')
	})
}

func compareVersionComponent(a, b string) int {
	an, aErr := strconv.ParseInt(a, 10, 64)
	bn, bErr := strconv.ParseInt(b, 10, 64)
	if aErr == nil && bErr == nil {
		switch {
		case an < bn:
			return -1
		case an > bn:
			return 1
		default:
			return 0
		}
	}
	// A numeric component sorts before an alphanumeric one, which is how
	// pre-release suffixes read: 1.0 before 1.0rc.
	if aErr == nil {
		return -1
	}
	if bErr == nil {
		return 1
	}
	return strings.Compare(a, b)
}

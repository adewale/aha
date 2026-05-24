package cli

import (
	"io"
	"strconv"
	"testing"
	"testing/quick"
)

func TestPostPositionalFlagReorderProperty(t *testing.T) {
	prop := func(queryRaw, repoRaw string, limitRaw uint8, jsonOut bool) bool {
		query := safeCLIAtom(queryRaw, "query")
		repo := safeCLIAtom(repoRaw, "repo")
		limit := int(limitRaw%50 + 1)
		args := []string{query, "--repo", repo, "--limit", strconv.Itoa(limit)}
		if jsonOut {
			args = append(args, "--json")
		}
		pf, err := parseFlagSpecs("search", reorderArgsBySpec(args, searchFlagSpecs), io.Discard, searchFlagSpecs)
		if err != nil {
			return false
		}
		return pf.NArg() == 1 && pf.Arg(0) == query && pf.String("repo") == repo && pf.Int("limit") == limit && pf.Bool("json") == jsonOut
	}
	if err := quick.Check(prop, &quick.Config{MaxCount: 200}); err != nil {
		t.Fatal(err)
	}
}

func safeCLIAtom(s, fallback string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' || r == '/' {
			out = append(out, r)
		}
	}
	if len(out) == 0 || out[0] == '-' {
		return fallback
	}
	if len(out) > 32 {
		out = out[:32]
	}
	return string(out)
}

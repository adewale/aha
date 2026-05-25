package corpus

import (
	"path/filepath"
	"sort"
	"strings"
)

func pathTokens(paths ...string) []string {
	seen := map[string]bool{}
	for _, p := range paths {
		p = strings.ToLower(filepath.ToSlash(strings.TrimSpace(p)))
		if p == "" {
			continue
		}
		for _, seg := range strings.Split(p, "/") {
			addPathToken(seen, seg)
			for _, part := range strings.FieldsFunc(seg, func(r rune) bool {
				return r == '-' || r == '_' || r == '.' || r == ' ' || r == '\t'
			}) {
				addPathToken(seen, part)
			}
		}
	}
	out := make([]string, 0, len(seen))
	for token := range seen {
		out = append(out, token)
	}
	sort.Strings(out)
	return out
}

func addPathToken(seen map[string]bool, token string) {
	token = strings.TrimSpace(token)
	if token == "" || token == "." || token == ".." {
		return
	}
	seen[token] = true
}

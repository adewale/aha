package corpus

import (
	"encoding/json"
	"path/filepath"
)

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }
func projectKey(cwd string) string {
	if cwd == "" {
		return ""
	}
	return filepath.Base(cwd)
}

package corpus

import (
	"encoding/json"
	"mime"
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
func extFromMime(mt string) string {
	if mt == "" {
		return ".bin"
	}
	exts, _ := mime.ExtensionsByType(mt)
	if len(exts) > 0 {
		return exts[0]
	}
	switch mt {
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	}
	return ".bin"
}

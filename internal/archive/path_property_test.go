package archive

import (
	"path"
	"strings"
	"testing"
	"testing/quick"
)

func TestValidateArchiveDataPathSafetyProperty(t *testing.T) {
	prop := func(name string) bool {
		err := validateArchiveDataPath(name)
		if err != nil {
			return true
		}
		if name == "" || name == "." || name == ".." || path.IsAbs(name) || path.Clean(name) != name {
			return false
		}
		if strings.Contains(name, "\\") || strings.HasPrefix(name, "../") || strings.Contains(name, "/../") || strings.HasPrefix(name, "./") || strings.Contains(name, "/./") {
			return false
		}
		if name == "manifest.json" || name == "checksums/sha256sums.txt" || strings.HasPrefix(name, "checksums/") {
			return false
		}
		return true
	}
	if err := quick.Check(prop, &quick.Config{MaxCount: 1000}); err != nil {
		t.Fatal(err)
	}
}

func TestGeneratedSourceArchivePathAcceptedProperty(t *testing.T) {
	prop := func(raw string) bool {
		name := "sources/pi/sessions/" + safeArchiveComponent(raw) + ".jsonl"
		if err := validateArchiveDataPath(name); err != nil {
			return false
		}
		return true
	}
	if err := quick.Check(prop, &quick.Config{MaxCount: 1000}); err != nil {
		t.Fatal(err)
	}
}

func safeArchiveComponent(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), ".-")
	if out == "" || out == ".." {
		return "file"
	}
	return out
}

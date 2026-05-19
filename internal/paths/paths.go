package paths

import (
	"os"
	"path/filepath"
	"strings"
)

func Expand(p string) (string, error) {
	if p == "" {
		return "", nil
	}
	p = os.ExpandEnv(p)
	if strings.HasPrefix(p, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if p == "~" {
			return home, nil
		}
		if strings.HasPrefix(p, "~/") {
			return filepath.Join(home, p[2:]), nil
		}
	}
	return filepath.Abs(p)
}

func MustExpand(p string) string {
	x, err := Expand(p)
	if err != nil {
		return p
	}
	return x
}

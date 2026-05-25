package safety

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/adewale/aha/internal/paths"
)

type SourceRoot struct{ path string }
type CorpusRoot struct{ path string }
type DepotRoot struct{ path string }
type SafeRelPath struct{ path string }

func NewSourceRoot(p string) (SourceRoot, error) {
	clean, err := cleanRoot(p)
	return SourceRoot{path: clean}, err
}

func NewCorpusRoot(p string) (CorpusRoot, error) {
	clean, err := cleanRoot(p)
	return CorpusRoot{path: clean}, err
}

func NewDepotRoot(p string) (DepotRoot, error) {
	clean, err := cleanRoot(p)
	return DepotRoot{path: clean}, err
}

func NewSafeRelPath(p string) (SafeRelPath, error) {
	if err := validateRelativePath(p); err != nil {
		return SafeRelPath{}, err
	}
	return SafeRelPath{path: filepath.ToSlash(filepath.Clean(p))}, nil
}

func (r SourceRoot) String() string  { return r.path }
func (r CorpusRoot) String() string  { return r.path }
func (r DepotRoot) String() string   { return r.path }
func (p SafeRelPath) String() string { return p.path }

func validateRelativePath(p string) error {
	if p == "" || filepath.IsAbs(p) || strings.Contains(p, "\\") {
		return fmt.Errorf("unsafe relative path %q", p)
	}
	clean := filepath.Clean(p)
	if clean == "." || strings.HasPrefix(clean, "..") || strings.Contains(clean, string(filepath.Separator)+".."+string(filepath.Separator)) {
		return fmt.Errorf("unsafe relative path %q", p)
	}
	return nil
}

func cleanRoot(p string) (string, error) {
	if p == "" {
		return "", fmt.Errorf("root path required")
	}
	expanded, err := paths.Expand(p)
	if err != nil {
		return "", err
	}
	if expanded == "" {
		return "", fmt.Errorf("root path required")
	}
	return filepath.Clean(expanded), nil
}

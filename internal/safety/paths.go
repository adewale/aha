package safety

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/adewale/aha/internal/model"
	"github.com/adewale/aha/internal/paths"
)

func ResolveExisting(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return filepath.Abs(resolved)
	}
	parent := abs
	var suffix []string
	for {
		next := filepath.Dir(parent)
		if next == parent {
			return abs, nil
		}
		suffix = append([]string{filepath.Base(parent)}, suffix...)
		parent = next
		if resolved, err := filepath.EvalSymlinks(parent); err == nil {
			parts := append([]string{resolved}, suffix...)
			return filepath.Abs(filepath.Join(parts...))
		}
	}
}

func Contains(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel))
}

func EnsureUnderRoot(root, target string) error {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	targetAbs, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	if !Contains(rootAbs, targetAbs) {
		return fmt.Errorf("source file %s is outside source root %s", targetAbs, rootAbs)
	}
	return nil
}

func ValidateWriteOutsideSources(cfg model.Config, target, label string) error {
	target, err := paths.Expand(target)
	if err != nil {
		return err
	}
	targetAbs, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	targetResolved, err := ResolveExisting(targetAbs)
	if err != nil {
		return err
	}
	for _, sc := range cfg.Sources {
		if !sc.Enabled || strings.TrimSpace(sc.Root) == "" {
			continue
		}
		root, err := paths.Expand(sc.Root)
		if err != nil {
			return err
		}
		rootAbs, err := filepath.Abs(root)
		if err != nil {
			return err
		}
		rootResolved, err := ResolveExisting(rootAbs)
		if err != nil {
			return err
		}
		if Contains(rootAbs, targetAbs) || Contains(rootResolved, targetResolved) {
			return fmt.Errorf("%s path %s must not be inside source root %s", label, targetAbs, rootAbs)
		}
	}
	return nil
}

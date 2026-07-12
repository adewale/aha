package safety

import (
	"errors"
	"fmt"
	"os"
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

// WorkspaceDestination is a write target proven not to overlap a source or
// repurpose an unrelated non-empty directory. Its path is private so mutation
// code must pass through PrepareWorkspaceDestination.
type WorkspaceDestination struct{ path string }

func (d WorkspaceDestination) Path() (string, error) {
	if d.path == "" {
		return "", errors.New("invalid Workspace destination capability")
	}
	return d.path, nil
}

// WorkspaceDestinationError deliberately carries no path, so callers can
// present it without disclosing local filesystem details.
type WorkspaceDestinationError struct{}

func (*WorkspaceDestinationError) Error() string {
	return "Workspace destination must be a dedicated empty directory or an existing aha Workspace"
}

func PrepareWorkspaceDestination(cfg model.Config, target string) (WorkspaceDestination, error) {
	if err := ValidateWriteOutsideSources(cfg, target, "Workspace"); err != nil {
		return WorkspaceDestination{}, err
	}
	expanded, err := paths.Expand(target)
	if err != nil {
		return WorkspaceDestination{}, err
	}
	if expanded == "" {
		expanded, err = paths.Expand("~/.aha/workspace")
		if err != nil {
			return WorkspaceDestination{}, err
		}
	}
	root, err := ResolveExisting(expanded)
	if err != nil {
		return WorkspaceDestination{}, err
	}
	info, err := os.Stat(root)
	if os.IsNotExist(err) {
		return WorkspaceDestination{path: root}, nil
	}
	if err != nil {
		return WorkspaceDestination{}, err
	}
	if !info.IsDir() {
		return WorkspaceDestination{}, &WorkspaceDestinationError{}
	}
	if _, err := os.Stat(filepath.Join(root, "corpus.db")); err == nil {
		return WorkspaceDestination{path: root}, nil
	} else if !os.IsNotExist(err) {
		return WorkspaceDestination{}, err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return WorkspaceDestination{}, err
	}
	if len(entries) > 0 {
		return WorkspaceDestination{}, &WorkspaceDestinationError{}
	}
	return WorkspaceDestination{path: root}, nil
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
		if Contains(targetAbs, rootAbs) || Contains(targetResolved, rootResolved) {
			return fmt.Errorf("%s path %s must not overlap source root %s", label, targetAbs, rootAbs)
		}
	}
	return nil
}

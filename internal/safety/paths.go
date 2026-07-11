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

// CorpusDestination is a write target that has been proven not to overlap a
// source and not to repurpose an unrelated non-empty directory. Its path is
// private so CLI mutation code must pass through PrepareCorpusDestination.
type CorpusDestination struct{ path string }

func (d CorpusDestination) Path() (string, error) {
	if d.path == "" {
		return "", errors.New("invalid corpus destination capability")
	}
	return d.path, nil
}

// CorpusDestinationError deliberately carries no path: callers can safely
// present it without disclosing local filesystem details.
type CorpusDestinationError struct{}

func (*CorpusDestinationError) Error() string {
	return "corpus destination must be a dedicated empty directory or an existing aha corpus"
}

func PrepareCorpusDestination(cfg model.Config, target string) (CorpusDestination, error) {
	if err := ValidateWriteOutsideSources(cfg, target, "corpus"); err != nil {
		return CorpusDestination{}, err
	}
	expanded, err := paths.Expand(target)
	if err != nil {
		return CorpusDestination{}, err
	}
	if expanded == "" {
		expanded, err = paths.Expand("~/.aha")
		if err != nil {
			return CorpusDestination{}, err
		}
	}
	root, err := ResolveExisting(expanded)
	if err != nil {
		return CorpusDestination{}, err
	}
	info, err := os.Stat(root)
	if os.IsNotExist(err) {
		return CorpusDestination{path: root}, nil
	}
	if err != nil {
		return CorpusDestination{}, err
	}
	if !info.IsDir() {
		return CorpusDestination{}, &CorpusDestinationError{}
	}
	if _, err := os.Stat(filepath.Join(root, "corpus.db")); err == nil {
		return CorpusDestination{path: root}, nil
	} else if !os.IsNotExist(err) {
		return CorpusDestination{}, err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return CorpusDestination{}, err
	}
	allowed := map[string]bool{"depot": true, "capture-cache.json": true, "blobs": true}
	if cfg.Depot.Type == "local" && strings.TrimSpace(cfg.Depot.Location) != "" {
		localDepot, expandErr := paths.Expand(cfg.Depot.Location)
		if expandErr != nil {
			return CorpusDestination{}, expandErr
		}
		localDepot, resolveErr := ResolveExisting(localDepot)
		if resolveErr != nil {
			return CorpusDestination{}, resolveErr
		}
		if rel, relErr := filepath.Rel(root, localDepot); relErr == nil && rel != "." && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel) {
			allowed[strings.Split(rel, string(os.PathSeparator))[0]] = true
		}
	}
	for _, entry := range entries {
		if !allowed[entry.Name()] {
			return CorpusDestination{}, &CorpusDestinationError{}
		}
	}
	return CorpusDestination{path: root}, nil
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

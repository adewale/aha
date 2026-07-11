package corpus

import (
	"os"
	"path/filepath"

	"github.com/adewale/aha/internal/paths"
)

// canonicalCorpusIdentity resolves every existing symlink component and
// returns one absolute spelling for both existing and not-yet-created corpus
// roots. Lifecycle locks, staging siblings, database opens, and directory
// exchange all derive from this identity.
func canonicalCorpusIdentity(root string) (string, error) {
	expanded, err := paths.Expand(root)
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(expanded)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return filepath.Abs(resolved)
	} else if !os.IsNotExist(err) {
		return "", err
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
		resolved, err := filepath.EvalSymlinks(parent)
		if err == nil {
			parts := append([]string{resolved}, suffix...)
			return filepath.Abs(filepath.Join(parts...))
		}
		if !os.IsNotExist(err) {
			return "", err
		}
	}
}

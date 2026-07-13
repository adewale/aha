package safety

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

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
// repurpose an unrelated non-empty directory. Claim rechecks the proof while
// holding an OS directory handle, closing symlink/rename substitution races.
type WorkspaceDestination struct {
	path       string
	publicPath string
	info       os.FileInfo
	sources    []sourceIdentity
	consumed   *atomic.Bool
}

type sourceIdentity struct {
	path string
	info os.FileInfo
}

// WorkspaceRoot keeps the claimed directory anchored until the Store closes.
type WorkspaceRoot struct {
	identityPath string
	storagePath  string
	anchor       io.Closer
}

func (r *WorkspaceRoot) IdentityPath() string { return r.identityPath }
func (r *WorkspaceRoot) StoragePath() string  { return r.storagePath }
func (r *WorkspaceRoot) Close() error {
	if r == nil || r.anchor == nil {
		return nil
	}
	return r.anchor.Close()
}

func claimWorkspaceRoot(path string) (storagePath, actualPath string, anchor io.Closer, info os.FileInfo, err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", "", nil, nil, err
	}
	if err := os.Mkdir(path, 0o700); err != nil && !os.IsExist(err) {
		return "", "", nil, nil, err
	}
	actualPath, err = ResolveExisting(path)
	if err != nil {
		return "", "", nil, nil, err
	}
	file, info, err := openClaimedWorkspaceDirectory(path)
	if err != nil {
		return "", "", nil, nil, err
	}
	fail := func(err error) (string, string, io.Closer, os.FileInfo, error) {
		_ = file.Close()
		return "", "", nil, nil, err
	}
	current, err := os.Lstat(path)
	if err != nil || current.Mode()&os.ModeSymlink != 0 || !os.SameFile(info, current) {
		if err == nil {
			err = &WorkspaceDestinationError{}
		}
		return fail(err)
	}
	entries, err := file.Readdirnames(-1)
	if err != nil {
		return fail(err)
	}
	if len(entries) > 0 {
		if _, err := os.Stat(filepath.Join(path, model.WorkspaceDatabaseFilename)); err != nil {
			return fail(&WorkspaceDestinationError{})
		}
	}
	return path, actualPath, file, info, nil
}

func (d WorkspaceDestination) Claim() (*WorkspaceRoot, error) {
	if d.path == "" || d.publicPath == "" || d.consumed == nil || !d.consumed.CompareAndSwap(false, true) {
		return nil, errors.New("invalid or already consumed Workspace destination capability")
	}
	stablePath, err := stabiliseWorkspacePath(d.publicPath, d.path, d.info)
	if err != nil {
		return nil, err
	}
	storagePath, actualPath, anchor, info, err := claimWorkspaceRoot(stablePath)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*WorkspaceRoot, error) {
		_ = anchor.Close()
		return nil, err
	}
	if d.info != nil && !os.SameFile(info, d.info) {
		return fail(&WorkspaceDestinationError{})
	}
	for _, source := range d.sources {
		if source.info != nil && os.SameFile(info, source.info) {
			return fail(&WorkspaceDestinationError{})
		}
		if Contains(source.path, actualPath) || Contains(actualPath, source.path) {
			return fail(&WorkspaceDestinationError{})
		}
	}
	return &WorkspaceRoot{identityPath: stablePath, storagePath: storagePath, anchor: anchor}, nil
}

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
	publicPath, err := filepath.Abs(expanded)
	if err != nil {
		return WorkspaceDestination{}, err
	}
	requestedPath := publicPath
	root, err := ResolveExisting(requestedPath)
	if err != nil {
		return WorkspaceDestination{}, err
	}
	publicPath = root
	if info, err := os.Lstat(requestedPath); err == nil && info.Mode()&os.ModeSymlink != 0 {
		publicPath = requestedPath
	} else if err != nil && !os.IsNotExist(err) {
		return WorkspaceDestination{}, err
	}
	sources, err := workspaceSourceIdentities(cfg)
	if err != nil {
		return WorkspaceDestination{}, err
	}
	info, err := os.Stat(root)
	if os.IsNotExist(err) {
		return WorkspaceDestination{path: root, publicPath: publicPath, sources: sources, consumed: &atomic.Bool{}}, nil
	}
	if err != nil {
		return WorkspaceDestination{}, err
	}
	if !info.IsDir() {
		return WorkspaceDestination{}, &WorkspaceDestinationError{}
	}
	if _, err := os.Stat(filepath.Join(root, model.WorkspaceDatabaseFilename)); err == nil {
		return WorkspaceDestination{path: root, publicPath: publicPath, info: info, sources: sources, consumed: &atomic.Bool{}}, nil
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
	return WorkspaceDestination{path: root, publicPath: publicPath, info: info, sources: sources, consumed: &atomic.Bool{}}, nil
}

func workspaceSourceIdentities(cfg model.Config) ([]sourceIdentity, error) {
	var sources []sourceIdentity
	for _, source := range cfg.Sources {
		if !source.Enabled || strings.TrimSpace(source.Root) == "" {
			continue
		}
		expanded, err := paths.Expand(source.Root)
		if err != nil {
			return nil, err
		}
		resolved, err := ResolveExisting(expanded)
		if err != nil {
			return nil, err
		}
		identity := sourceIdentity{path: resolved}
		if info, err := os.Stat(resolved); err == nil {
			identity.info = info
		} else if !os.IsNotExist(err) {
			return nil, err
		}
		sources = append(sources, identity)
	}
	return sources, nil
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

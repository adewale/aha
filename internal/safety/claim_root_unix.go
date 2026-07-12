//go:build !windows

package safety

import (
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func stabiliseWorkspacePath(publicPath, resolvedPath string, expected os.FileInfo) (string, error) {
	if publicPath != resolvedPath {
		current, err := os.Stat(resolvedPath)
		if err != nil || (expected != nil && !os.SameFile(current, expected)) {
			if err == nil {
				err = &WorkspaceDestinationError{}
			}
			return "", err
		}
		return resolvedPath, nil
	}
	parent := filepath.Dir(publicPath)
	if err := validateWorkspaceParentBeforeMutation(parent); err != nil {
		return "", err
	}
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return "", err
	}
	if err := validateSafeWorkspaceParent(parent); err != nil {
		return "", err
	}
	stable := filepath.Join(parent, "."+filepath.Base(publicPath)+".aha-workspace")
	if expected != nil {
		if !workspaceOwnedByCurrentUser(publicPath) {
			return "", &WorkspaceDestinationError{}
		}
		current, err := os.Lstat(publicPath)
		if err != nil || current.Mode()&os.ModeSymlink != 0 || !os.SameFile(current, expected) {
			if err == nil {
				err = &WorkspaceDestinationError{}
			}
			return "", err
		}
		if _, err := os.Lstat(stable); err == nil || !errors.Is(err, os.ErrNotExist) {
			return "", &WorkspaceDestinationError{}
		}
		if err := os.Rename(publicPath, stable); err != nil {
			return "", err
		}
	} else {
		if _, err := os.Lstat(publicPath); err == nil || !errors.Is(err, os.ErrNotExist) {
			return "", &WorkspaceDestinationError{}
		}
		if _, err := os.Lstat(stable); errors.Is(err, os.ErrNotExist) {
			if err := os.Mkdir(stable, 0o700); err != nil {
				return "", err
			}
		} else if err != nil {
			return "", err
		}
		if !workspaceOwnedByCurrentUser(stable) {
			return "", &WorkspaceDestinationError{}
		}
	}
	if err := os.Symlink(filepath.Base(stable), publicPath); err != nil {
		// A failed first migration remains recoverable at the deterministic
		// stable path; the next claim completes the symlink publication.
		return "", err
	}
	if dir, err := os.Open(parent); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return stable, nil
}

func workspaceOwnedByCurrentUser(path string) bool {
	var stat unix.Stat_t
	return unix.Stat(path, &stat) == nil && stat.Uid == uint32(os.Geteuid())
}

func validateSafeWorkspaceParent(parent string) error {
	info, err := os.Stat(parent)
	if err != nil {
		return err
	}
	if !info.IsDir() || (info.Mode().Perm()&0o022 != 0 && info.Mode()&os.ModeSticky == 0) {
		return &WorkspaceDestinationError{}
	}
	return nil
}

func validateWorkspaceParentBeforeMutation(parent string) error {
	candidate := parent
	for {
		if _, err := os.Stat(candidate); err == nil {
			return validateSafeWorkspaceParent(candidate)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		next := filepath.Dir(candidate)
		if next == candidate {
			return &WorkspaceDestinationError{}
		}
		candidate = next
	}
}

func openClaimedWorkspaceDirectory(path string) (*os.File, os.FileInfo, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil || stat.Uid != uint32(os.Geteuid()) {
		_ = file.Close()
		if err == nil {
			err = &WorkspaceDestinationError{}
		}
		return nil, nil, err
	}
	parent, err := os.Stat(filepath.Dir(path))
	if err != nil {
		_ = file.Close()
		return nil, nil, err
	}
	if parent.Mode().Perm()&0o022 != 0 && parent.Mode()&os.ModeSticky == 0 {
		_ = file.Close()
		return nil, nil, &WorkspaceDestinationError{}
	}
	info, err := file.Stat()
	if err != nil || !info.IsDir() {
		_ = file.Close()
		if err == nil {
			err = &WorkspaceDestinationError{}
		}
		return nil, nil, err
	}
	return file, info, nil
}

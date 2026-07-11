//go:build linux

package corpus

import "golang.org/x/sys/unix"

func atomicSwapSupported() bool { return true }

func atomicSwapDirectories(first, second string) error {
	return unix.Renameat2(unix.AT_FDCWD, first, unix.AT_FDCWD, second, unix.RENAME_EXCHANGE)
}

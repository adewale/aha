//go:build darwin

package corpus

import "golang.org/x/sys/unix"

func atomicSwapDirectories(first, second string) error {
	return unix.RenamexNp(first, second, unix.RENAME_SWAP)
}

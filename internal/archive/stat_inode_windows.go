//go:build windows

package archive

import "os"

// os.FileInfo does not expose the Windows file index. The cache remains an
// advisory size/mtime hint and its racy-mtime rule still prevents in-window
// changes from being trusted.
func statInode(os.FileInfo) uint64 { return 0 }

//go:build windows

package corpus

func syncRebuildTree(string) error      { return ErrRebuildUnsupported }
func syncRebuildDirectory(string) error { return ErrRebuildUnsupported }

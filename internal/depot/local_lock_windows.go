//go:build windows

package depot

func withLocalLock(root string, fn func() error) error { return fn() }

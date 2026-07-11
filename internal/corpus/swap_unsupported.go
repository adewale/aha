//go:build !darwin && !linux

package corpus

import "fmt"

func atomicSwapSupported() bool { return false }

func atomicSwapDirectories(string, string) error {
	return fmt.Errorf("atomic directory swap is unsupported on this platform")
}

//go:build !windows

package cli

import (
	"os"
	"syscall"
)

func shutdownSignals() []os.Signal {
	return []os.Signal{syscall.SIGINT, syscall.SIGTERM}
}

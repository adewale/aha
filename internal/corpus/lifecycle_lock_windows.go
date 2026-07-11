//go:build windows

package corpus

type lifecycleLock struct{}

func rebuildLifecycleSupported() bool { return false }

func acquireLifecycleLock(string, bool) (*lifecycleLock, error) { return &lifecycleLock{}, nil }
func (l *lifecycleLock) release() error                         { return nil }

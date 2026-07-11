//go:build windows

package corpus

import "context"

type lifecycleLock struct{}

func rebuildLifecycleSupported() bool { return false }

func acquireLifecycleLock(ctx context.Context, _ string, _ bool) (*lifecycleLock, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &lifecycleLock{}, nil
}
func (l *lifecycleLock) release() error { return nil }

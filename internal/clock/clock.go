package clock

import "time"

type Clock interface {
	Now() time.Time
}

type Sleeper interface {
	Sleep(time.Duration)
}

type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now().UTC() }

type RealSleeper struct{}

func (RealSleeper) Sleep(d time.Duration) { time.Sleep(d) }

type FixedClock struct{ T time.Time }

func (c FixedClock) Now() time.Time { return c.T.UTC() }

type NoopSleeper struct{}

func (NoopSleeper) Sleep(time.Duration) {}

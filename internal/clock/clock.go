package clock

import "time"

type Clock interface {
	Now() time.Time
}

type Sleeper interface {
	Sleep(time.Duration)
}

type Backoff interface {
	Delay(attempt int) time.Duration
}

type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now().UTC() }

type RealSleeper struct{}

func (RealSleeper) Sleep(d time.Duration) { time.Sleep(d) }

type FixedClock struct{ T time.Time }

func (c FixedClock) Now() time.Time { return c.T.UTC() }

type NoopSleeper struct{}

func (NoopSleeper) Sleep(time.Duration) {}

type LinearBackoff struct{ Base time.Duration }

func (b LinearBackoff) Delay(attempt int) time.Duration {
	base := b.Base
	if base == 0 {
		base = 10 * time.Millisecond
	}
	return time.Duration(attempt+1) * base
}

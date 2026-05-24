package clock_test

import (
	"testing"
	"time"

	"github.com/adewale/aha/internal/clock"
)

func TestFixedClockReturnsPinnedUTCInstant(t *testing.T) {
	instant := time.Date(2026, 5, 24, 10, 0, 0, 0, time.FixedZone("x", 3600))
	got := (clock.FixedClock{T: instant}).Now()
	if got.Location() != time.UTC || !got.Equal(instant) {
		t.Fatalf("FixedClock.Now()=%s location=%s", got, got.Location())
	}
}

func TestNoopSleeperReturnsImmediately(t *testing.T) {
	start := time.Now()
	clock.NoopSleeper{}.Sleep(time.Hour)
	if time.Since(start) > 100*time.Millisecond {
		t.Fatal("NoopSleeper slept")
	}
}

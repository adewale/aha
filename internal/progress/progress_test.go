package progress_test

import (
	"sync"
	"testing"
	"time"

	"github.com/adewale/aha/internal/progress"
)

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

type recorder struct {
	mu     sync.Mutex
	events []progress.Event
}

func (r *recorder) Observe(event progress.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
}

func TestTrackerEmitsTypedOrderedEventsWithExactElapsedAndHonestTotal(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 7, 11, 10, 0, 0, 0, time.UTC)}
	record := &recorder{}
	tracker := progress.NewTracker(record, clock)
	tracker.Start(progress.PhaseCapture, progress.UnknownTotal(), progress.UnitFiles)
	clock.now = clock.now.Add(2 * time.Second)
	tracker.Advance(progress.PhaseCapture, 3, progress.KnownTotal(5), progress.UnitFiles)
	clock.now = clock.now.Add(time.Second)
	tracker.Complete(progress.PhaseCapture, 5, progress.KnownTotal(5), progress.UnitFiles)
	if len(record.events) != 3 {
		t.Fatalf("events=%v", record.events)
	}
	if record.events[0].Kind != progress.Started || record.events[1].Kind != progress.Advanced || record.events[2].Kind != progress.Completed {
		t.Fatalf("event order=%v", record.events)
	}
	if record.events[1].Elapsed != 2*time.Second || record.events[2].Elapsed != 3*time.Second {
		t.Fatalf("elapsed=%v / %v", record.events[1].Elapsed, record.events[2].Elapsed)
	}
	if record.events[0].Total.Known || !record.events[1].Total.Known || record.events[1].Total.Value != 5 {
		t.Fatalf("totals=%+v %+v", record.events[0].Total, record.events[1].Total)
	}
}

func TestTrackerConcurrentObserveIsRaceFreeAndCapsKnownTotals(t *testing.T) {
	clock := &fakeClock{now: time.Unix(0, 0)}
	record := &recorder{}
	tracker := progress.NewTracker(record, clock)
	tracker.Start(progress.PhaseUpload, progress.KnownTotal(10), progress.UnitBlobs)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tracker.Advance(progress.PhaseUpload, uint64(i+1), progress.KnownTotal(10), progress.UnitBlobs)
		}(i)
	}
	wg.Wait()
	for _, event := range record.events {
		if event.Total.Known && event.Current > event.Total.Value {
			t.Fatalf("event exceeded total: %+v", event)
		}
	}
}

func TestTrackerFailureIsExplicit(t *testing.T) {
	clock := &fakeClock{now: time.Unix(0, 0)}
	record := &recorder{}
	tracker := progress.NewTracker(record, clock)
	tracker.Start(progress.PhaseUpload, progress.KnownTotal(2), progress.UnitBlobs)
	tracker.Fail(progress.PhaseUpload, 1, progress.KnownTotal(2), progress.UnitBlobs)
	if len(record.events) != 2 || record.events[1].Kind != progress.Failed {
		t.Fatalf("events=%v", record.events)
	}
}

func TestTrackerCancellationIsExplicit(t *testing.T) {
	clock := &fakeClock{now: time.Unix(0, 0)}
	record := &recorder{}
	tracker := progress.NewTracker(record, clock)
	tracker.Start(progress.PhasePull, progress.UnknownTotal(), progress.UnitMachines)
	tracker.Cancel(progress.PhasePull, 2, progress.UnknownTotal(), progress.UnitMachines)
	if len(record.events) != 2 || record.events[1].Kind != progress.Cancelled {
		t.Fatalf("events=%v", record.events)
	}
}

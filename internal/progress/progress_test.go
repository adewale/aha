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
	record.mu.Lock()
	events := append([]progress.Event(nil), record.events...)
	record.mu.Unlock()
	if len(events) != 21 {
		t.Fatalf("events=%d want start plus all 20 advances: %+v", len(events), events)
	}
	if events[0].Kind != progress.Started {
		t.Fatalf("first event=%+v want started", events[0])
	}
	counts := map[uint64]int{}
	for i, event := range events {
		if event.Sequence != uint64(i+1) {
			t.Fatalf("event[%d] sequence=%d want %d; observer delivery was reordered", i, event.Sequence, i+1)
		}
		if event.Total.Known && event.Current > event.Total.Value {
			t.Fatalf("event exceeded total: %+v", event)
		}
		if event.Kind == progress.Advanced {
			counts[event.Current]++
		}
	}
	for current := uint64(1); current < 10; current++ {
		if counts[current] != 1 {
			t.Fatalf("current=%d count=%d want 1; events=%+v", current, counts[current], events)
		}
	}
	if counts[10] != 11 {
		t.Fatalf("capped current=10 count=%d want 11", counts[10])
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

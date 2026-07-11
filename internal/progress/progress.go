package progress

import (
	"sync"
	"time"
)

type Phase string

const (
	PhaseCapture       Phase = "capture"
	PhaseUpload        Phase = "upload"
	PhasePublish       Phase = "publish"
	PhasePull          Phase = "pull"
	PhaseIngest        Phase = "ingest"
	PhaseVerify        Phase = "verify"
	PhaseVerifyBlobs   Phase = "verify_blobs"
	PhaseRebuildBuild  Phase = "rebuild_build"
	PhaseRebuildVerify Phase = "rebuild_verify"
	PhaseRebuildSwap   Phase = "rebuild_swap"
)

type Kind string

const (
	Started   Kind = "started"
	Advanced  Kind = "advanced"
	Completed Kind = "completed"
	Cancelled Kind = "cancelled"
	Failed    Kind = "failed"
)

type Unit string

const (
	UnitNone     Unit = ""
	UnitFiles    Unit = "files"
	UnitBlobs    Unit = "blobs"
	UnitMachines Unit = "machines"
	UnitSessions Unit = "sessions"
	UnitBytes    Unit = "bytes"
	UnitSteps    Unit = "steps"
)

type Total struct {
	Known bool   `json:"known"`
	Value uint64 `json:"value,omitempty"`
}

func UnknownTotal() Total           { return Total{} }
func KnownTotal(value uint64) Total { return Total{Known: true, Value: value} }

type Event struct {
	Kind    Kind          `json:"kind"`
	Phase   Phase         `json:"phase"`
	Current uint64        `json:"current,omitempty"`
	Total   Total         `json:"total"`
	Unit    Unit          `json:"unit,omitempty"`
	Elapsed time.Duration `json:"-"`
}

type Observer interface {
	Observe(Event)
}

type ObserverFunc func(Event)

func (f ObserverFunc) Observe(event Event) { f(event) }

type Clock interface {
	Now() time.Time
}

type Tracker struct {
	mu       sync.Mutex
	observer Observer
	clock    Clock
	started  map[Phase]time.Time
}

func NewTracker(observer Observer, clock Clock) *Tracker {
	return &Tracker{observer: observer, clock: clock, started: map[Phase]time.Time{}}
}

func (t *Tracker) Start(phase Phase, total Total, unit Unit) {
	t.emit(Started, phase, 0, total, unit, true)
}

func (t *Tracker) Advance(phase Phase, current uint64, total Total, unit Unit) {
	t.emit(Advanced, phase, current, total, unit, false)
}

func (t *Tracker) Complete(phase Phase, current uint64, total Total, unit Unit) {
	t.emit(Completed, phase, current, total, unit, false)
}

func (t *Tracker) Cancel(phase Phase, current uint64, total Total, unit Unit) {
	t.emit(Cancelled, phase, current, total, unit, false)
}

func (t *Tracker) Fail(phase Phase, current uint64, total Total, unit Unit) {
	t.emit(Failed, phase, current, total, unit, false)
}

func (t *Tracker) emit(kind Kind, phase Phase, current uint64, total Total, unit Unit, reset bool) {
	if t == nil || t.observer == nil || t.clock == nil {
		return
	}
	if total.Known && current > total.Value {
		current = total.Value
	}
	now := t.clock.Now()
	t.mu.Lock()
	started, ok := t.started[phase]
	if reset || !ok {
		started = now
		t.started[phase] = now
	}
	event := Event{Kind: kind, Phase: phase, Current: current, Total: total, Unit: unit, Elapsed: now.Sub(started)}
	t.mu.Unlock()
	t.observer.Observe(event)
}

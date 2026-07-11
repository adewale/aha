package depot

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adewale/aha/internal/clock"
	"github.com/adewale/aha/internal/hash"
	"github.com/adewale/aha/internal/model"
	ahaprogress "github.com/adewale/aha/internal/progress"
)

type forcedConditionalConflictsStore struct {
	objectStore
	key       string
	remaining int
	conflicts int
}

func (s *forcedConditionalConflictsStore) putBytesConditional(ctx context.Context, key, contentType string, b []byte, etag string) error {
	if key == s.key && s.remaining > 0 {
		s.remaining--
		s.conflicts++
		return fmt.Errorf("%w: forced conditional-write race", errPreconditionFailed)
	}
	return s.objectStore.putBytesConditional(ctx, key, contentType, b, etag)
}

// TestEnsureInMachinesIndexRetriesUntilCASConverges pins that a machine is
// registered after more lost conditional-write races than the former fixed
// retry ceiling. The real concurrency test covers scheduling; this test makes
// the retry path deterministic.
func TestEnsureInMachinesIndexRetriesUntilCASConverges(t *testing.T) {
	const forcedConflicts = 6
	store := &forcedConditionalConflictsStore{
		objectStore: &localStoreV2{root: t.TempDir()},
		key:         MachinesIndexKey,
		remaining:   forcedConflicts,
	}
	v2 := &V2{store: store}
	machine := &MachineDepot{v: v2, machine: "first-machine"}
	if err := machine.ensureInMachinesIndex(t.Context()); err != nil {
		t.Fatalf("ensureInMachinesIndex: %v", err)
	}
	if store.conflicts != forcedConflicts {
		t.Fatalf("conditional conflicts=%d want %d", store.conflicts, forcedConflicts)
	}
	machines, err := v2.Machines(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(machines) != 1 || machines[0] != "first-machine" {
		t.Fatalf("Machines=%v want [first-machine]", machines)
	}
}

// TestSetLatestRetriesUntilCASConverges pins the same retry property for a
// machine's latest pointer. Distinct-machine concurrency only races on the
// index, so this forced conflict covers the other conditional-write path.
func TestSetLatestRetriesUntilCASConverges(t *testing.T) {
	const (
		machineID       = "first-machine"
		forcedConflicts = 6
	)
	store := &forcedConditionalConflictsStore{
		objectStore: &localStoreV2{root: t.TempDir()},
		key:         LatestPointerKey(machineID),
		remaining:   forcedConflicts,
	}
	v2 := &V2{store: store}
	machine := &MachineDepot{v: v2, machine: machineID}
	sha, err := model.NewManifestSHA256(strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	if err := machine.SetLatest(t.Context(), PublishedSnapshot{machine: machineID, sha: sha}); err != nil {
		t.Fatalf("SetLatest: %v", err)
	}
	if store.conflicts != forcedConflicts {
		t.Fatalf("conditional conflicts=%d want %d", store.conflicts, forcedConflicts)
	}
	latest, ok, err := v2.Latest(t.Context(), machineID)
	if err != nil || !ok || latest != sha {
		t.Fatalf("Latest=%v ok=%v err=%v want %s", latest, ok, err, sha)
	}
	machines, err := v2.Machines(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(machines) != 1 || machines[0] != machineID {
		t.Fatalf("Machines=%v want [%s]", machines, machineID)
	}
}

func TestEnsureInMachinesIndexReturnsTypedContentionError(t *testing.T) {
	store := &forcedConditionalConflictsStore{
		objectStore: &localStoreV2{root: t.TempDir()},
		key:         MachinesIndexKey,
		remaining:   1000,
	}
	v2 := &V2{store: store}
	err := (&MachineDepot{v: v2, machine: "blocked-machine"}).ensureInMachinesIndex(t.Context())
	var contention *ContentionError
	if !errors.As(err, &contention) {
		t.Fatalf("ensureInMachinesIndex error=%v want *ContentionError", err)
	}
	if contention.Key != MachinesIndexKey || contention.Attempts <= 6 {
		t.Fatalf("contention=%+v", contention)
	}
}

func TestWaitForConditionalRetryHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := waitForConditionalRetry(ctx, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("waitForConditionalRetry error=%v want context.Canceled", err)
	}
}

type cancelAfterMachinesStore struct {
	objectStore
	cancel context.CancelFunc
}

func (s *cancelAfterMachinesStore) get(ctx context.Context, key string) ([]byte, string, error) {
	body, etag, err := s.objectStore.get(ctx, key)
	if key == MachinesIndexKey && err == nil {
		s.cancel()
	}
	return body, etag, err
}

func TestDeepVerifyChecksCancellationBeforeSuccessfulCompletion(t *testing.T) {
	base := &localStoreV2{root: t.TempDir()}
	v2 := &V2{store: base}
	if err := v2.Init(t.Context()); err != nil {
		t.Fatal(err)
	}
	index, err := EncodeMachinesIndex(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := base.putBytesIfAbsent(t.Context(), MachinesIndexKey, "application/json", index); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	v2.store = &cancelAfterMachinesStore{objectStore: base, cancel: cancel}
	recorder := &progressRecorder{}
	tracker := ahaprogress.NewTracker(recorder, clock.RealClock{})
	_, err = v2.VerifyWithOptions(ctx, true, VerifyOptions{Progress: tracker})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("VerifyWithOptions error=%v want context.Canceled", err)
	}
	for _, event := range recorder.events {
		if event.Kind == ahaprogress.Completed {
			t.Fatalf("completed event after cancellation: %+v", event)
		}
	}
}

type cancelOnReadStore struct {
	objectStore
	cancel context.CancelFunc
}

func (s *cancelOnReadStore) getStream(ctx context.Context, key string) (io.ReadCloser, error) {
	rc, err := s.objectStore.getStream(ctx, key)
	if err != nil {
		return nil, err
	}
	return &cancelOnFirstRead{ReadCloser: rc, cancel: s.cancel}, nil
}

type cancelOnFirstRead struct {
	io.ReadCloser
	cancel context.CancelFunc
	done   bool
}

func (r *cancelOnFirstRead) Read(p []byte) (int, error) {
	if !r.done {
		r.done = true
		r.cancel()
	}
	return r.ReadCloser.Read(p)
}

type progressRecorder struct{ events []ahaprogress.Event }

func (r *progressRecorder) Observe(event ahaprogress.Event) { r.events = append(r.events, event) }

func TestDeepVerifyReturnsCancellationInsteadOfCorruptionReport(t *testing.T) {
	root := t.TempDir()
	base := &localStoreV2{root: root}
	v2 := &V2{store: base}
	if err := v2.Init(t.Context()); err != nil {
		t.Fatal(err)
	}
	const content = "a blob large enough to require another cancellation-aware read"
	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	key, err := model.NewBlobKey(hash.SHA256Bytes([]byte(content)))
	if err != nil {
		t.Fatal(err)
	}
	machine, err := v2.ForMachine("cancel-machine")
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := machine.EnsureBlob(t.Context(), key, path)
	if err != nil {
		t.Fatal(err)
	}
	manifest := model.SnapshotManifest{
		Schema: model.SnapshotManifestSchema, MachineID: "cancel-machine", CapturedAt: "2026-07-10T00:00:00Z", CreatedBy: "test",
		Source:   model.ManifestSource{HostOS: "linux"},
		Policy:   model.ManifestPolicy{PathMode: "raw", IncludeSubagents: true, IncludeImages: true, Redaction: "none-v1"},
		Adapters: []model.ManifestAdapt{{Name: "pi", Version: "test"}},
		Files:    []model.ManifestFile{{Source: "pi", Kind: "session", RelativePath: "sources/pi/sessions/one.jsonl", RawPath: "/one.jsonl", SHA256: key.String(), Bytes: int64(len(content)), SessionID: "one", CopyState: "stable"}},
	}
	published, err := machine.PublishSnapshot(t.Context(), manifest, []BlobReceipt{receipt})
	if err != nil {
		t.Fatal(err)
	}
	if err := machine.SetLatest(t.Context(), published); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	v2.store = &cancelOnReadStore{objectStore: base, cancel: cancel}
	recorder := &progressRecorder{}
	tracker := ahaprogress.NewTracker(recorder, clock.RealClock{})
	_, err = v2.VerifyWithOptions(ctx, true, VerifyOptions{Progress: tracker})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("VerifyWithOptions error=%v want context.Canceled", err)
	}
	for _, event := range recorder.events {
		if event.Kind == ahaprogress.Completed {
			t.Fatalf("completed event after cancellation: %+v", event)
		}
	}
	if len(recorder.events) == 0 || recorder.events[len(recorder.events)-1].Kind != ahaprogress.Cancelled {
		t.Fatalf("events=%+v want terminal cancellation", recorder.events)
	}
}

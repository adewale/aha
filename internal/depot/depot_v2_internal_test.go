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
	machine := &machineDepot{v: v2, machine: "first-machine"}
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
	machine := &machineDepot{v: v2, machine: machineID}
	sha, err := model.NewManifestSHA256(strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	if err := machine.setLatest(t.Context(), publishedSnapshot{machine: machineID, sha: sha, expected: latestExpectation{kind: expectLatestAbsent}}); err != nil {
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
	err := (&machineDepot{v: v2, machine: "blocked-machine"}).ensureInMachinesIndex(t.Context())
	var contention *ContentionError
	if !errors.As(err, &contention) {
		t.Fatalf("ensureInMachinesIndex error=%v want *ContentionError", err)
	}
	if contention.Key != MachinesIndexKey || contention.Attempts <= 6 {
		t.Fatalf("contention=%+v", contention)
	}
}

type cancelOnNthConflictStore struct {
	objectStore
	key       string
	remaining int
	cancel    context.CancelFunc
}

func (s *cancelOnNthConflictStore) putBytesConditional(ctx context.Context, key, contentType string, b []byte, etag string) error {
	if key == s.key && s.remaining > 0 {
		s.remaining--
		if s.remaining == 0 {
			s.cancel()
		}
		return fmt.Errorf("%w: forced terminal conflict", errPreconditionFailed)
	}
	return s.objectStore.putBytesConditional(ctx, key, contentType, b, etag)
}

func TestContentionExhaustionDoesNotSleepOrReplaceContentionWithLateCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	store := &cancelOnNthConflictStore{
		objectStore: &localStoreV2{root: t.TempDir()},
		key:         MachinesIndexKey,
		remaining:   conditionalRetryAttempts,
		cancel:      cancel,
	}
	err := (&machineDepot{v: &V2{store: store}, machine: "blocked-machine"}).ensureInMachinesIndex(ctx)
	var contention *ContentionError
	if !errors.As(err, &contention) {
		t.Fatalf("terminal error=%v want *ContentionError despite cancellation after final write", err)
	}
	if contention.Attempts != conditionalRetryAttempts {
		t.Fatalf("attempts=%d want %d", contention.Attempts, conditionalRetryAttempts)
	}
}

func TestWaitForConditionalRetryHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := waitForConditionalRetry(ctx, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("waitForConditionalRetry error=%v want context.Canceled", err)
	}
}

type countingGetStore struct {
	objectStore
	gets        map[string]int
	existsCalls int
}

func (s *countingGetStore) get(ctx context.Context, key string) ([]byte, string, error) {
	s.gets[key]++
	return s.objectStore.get(ctx, key)
}

func (s *countingGetStore) exists(ctx context.Context, key string) (bool, error) {
	s.existsCalls++
	return s.objectStore.exists(ctx, key)
}

type internalBlobSource struct {
	path  string
	calls int
}

func (s *internalBlobSource) BlobPath(model.BlobKey) (string, error) {
	s.calls++
	if s.path == "" {
		return "", errors.New("blob source must not be consulted")
	}
	return s.path, nil
}

func singleFileManifest(machine, content string) (model.SnapshotManifest, model.BlobKey) {
	key, _ := model.NewBlobKey(hash.SHA256Bytes([]byte(content)))
	manifest := model.SnapshotManifest{
		Schema: model.SnapshotManifestSchema, RequiredFeatures: model.RequiredSnapshotFeatures(), MachineID: machine, CapturedAt: "2026-07-10T00:00:00Z", CreatedBy: "test",
		Source:   model.ManifestSource{HostOS: "linux"},
		Policy:   model.ManifestPolicy{PathMode: "raw", IncludeSubagents: true, IncludeImages: true, Redaction: "none-v1"},
		Adapters: []model.ManifestAdapt{{Name: "pi", Version: "test"}},
		Files:    []model.ManifestFile{{Source: "pi", Kind: "session", RelativePath: "sources/pi/sessions/one.jsonl", RawPath: "/one.jsonl", SHA256: key.String(), Bytes: int64(len(content)), SessionID: "one", CopyState: "stable"}},
	}
	return manifest, key
}

func writeInternalBlob(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "blob")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestArchiveMetadataInspectionDoesNotHeadEveryBlob(t *testing.T) {
	v2, err := NewLocalV2(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := v2.Init(t.Context()); err != nil {
		t.Fatal(err)
	}
	manifest, _ := singleFileManifest("a", "alpha")
	if _, err := pushV2(t.Context(), v2, manifest, &internalBlobSource{path: writeInternalBlob(t, "alpha")}); err != nil {
		t.Fatal(err)
	}
	counter := &countingGetStore{objectStore: v2.markerObjects(), gets: map[string]int{}}
	v2.markerStore = counter
	v2.store = &prefixedStore{base: counter, prefix: currentArchiveDataPrefix}
	report, err := v2.InspectMetadata(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !report.Initialised || len(report.Latest) != 1 || counter.existsCalls != 0 {
		t.Fatalf("report=%+v blob HEADs=%d", report, counter.existsCalls)
	}
}

func TestPrepareUploadDoesNotReadUnrelatedMachinePointersOrManifests(t *testing.T) {
	v2, err := NewLocalV2(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := v2.Init(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, machine := range []string{"a", "b", "c"} {
		manifest, _ := singleFileManifest(machine, machine)
		if _, err := pushV2(t.Context(), v2, manifest, &internalBlobSource{path: writeInternalBlob(t, machine)}); err != nil {
			t.Fatal(err)
		}
	}
	counter := &countingGetStore{objectStore: v2.markerObjects(), gets: map[string]int{}}
	v2.markerStore = counter
	if _, err := v2.PrepareUpload(t.Context()); err != nil {
		t.Fatal(err)
	}
	for key, count := range counter.gets {
		if count > 0 && (strings.HasSuffix(key, "/latest") || strings.Contains(key, "/manifests/")) {
			t.Fatalf("PrepareUpload fetched unrelated metadata %s %d time(s)", key, count)
		}
	}
}

func TestDownloadSummaryExaminesOnlyChangedMachineSnapshots(t *testing.T) {
	v2, err := NewLocalV2(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := v2.Init(t.Context()); err != nil {
		t.Fatal(err)
	}
	baseline := map[string]string{}
	for _, tc := range []struct{ machine, content string }{{"a", "alpha"}, {"b", "bravo"}} {
		manifest, _ := singleFileManifest(tc.machine, tc.content)
		source := &internalBlobSource{path: writeInternalBlob(t, tc.content)}
		result, err := pushV2(t.Context(), v2, manifest, source)
		if err != nil {
			t.Fatal(err)
		}
		if tc.machine == "a" {
			baseline[tc.machine] = result.ManifestSHA256().String()
		}
	}
	counter := &countingGetStore{objectStore: v2.markerObjects(), gets: map[string]int{}}
	v2.markerStore = counter
	v2.store = &prefixedStore{base: counter, prefix: currentArchiveDataPrefix}
	reader, err := v2.PreparePull(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	plan, err := reader.PlanDownload(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	lookups := 0
	materialisation, err := plan.PrepareMaterialisation(t.Context(), baseline, map[string]string{"pi": "test"}, func(model.BlobKey) (bool, error) {
		lookups++
		return false, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer materialisation.Close()
	summary, err := materialisation.Summary()
	if err != nil {
		t.Fatal(err)
	}
	if lookups != 1 || summary.UnknownBlobs != 1 || summary.Machines != 2 {
		t.Fatalf("lookups=%d summary=%+v want only machine b delta", lookups, summary)
	}
	if err := materialisation.ForEachManifest(t.Context(), func(string, model.PreparedSnapshot, func(model.BlobKey) (io.ReadCloser, error)) error {
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	manifestKey := ManifestObjectKey("b", plan.latest["b"])
	if got := counter.gets[currentArchiveDataPrefix+manifestKey]; got != 1 {
		t.Fatalf("manifest GETs=%d want 1 across summary and execution", got)
	}
}

func TestMaterialisationPlanScopesBlobsAndSpoolsManifestsOffHeap(t *testing.T) {
	v2, err := NewLocalV2(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := v2.Init(t.Context()); err != nil {
		t.Fatal(err)
	}
	manifest, key := singleFileManifest("a", "alpha")
	if _, err := pushV2(t.Context(), v2, manifest, &internalBlobSource{path: writeInternalBlob(t, "alpha")}); err != nil {
		t.Fatal(err)
	}
	reader, err := v2.PreparePull(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	download, err := reader.PlanDownload(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	plan, err := download.PrepareMaterialisation(t.Context(), nil, map[string]string{"pi": "test"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	tempRoot := plan.tempRoot
	unknown, _ := model.NewBlobKey(strings.Repeat("f", 64))
	if unknown == key {
		t.Fatal("bad test key")
	}
	if err := plan.ForEachManifest(t.Context(), func(_ string, _ model.PreparedSnapshot, open func(model.BlobKey) (io.ReadCloser, error)) error {
		_, err := open(unknown)
		if err == nil || !strings.Contains(err.Error(), "outside") {
			t.Fatalf("out-of-vector blob error=%v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := plan.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(tempRoot); !os.IsNotExist(err) {
		t.Fatalf("materialisation spool survived close: %v", err)
	}
}

func TestDownloadPlanRejectsUnsupportedAdaptersBeforeMaterialisation(t *testing.T) {
	v2, err := NewLocalV2(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := v2.Init(t.Context()); err != nil {
		t.Fatal(err)
	}
	manifest, _ := singleFileManifest("a", "alpha")
	manifest.Files[0].Source = "future-adapter"
	manifest.Files[0].RelativePath = "sources/future-adapter/sessions/one.jsonl"
	manifest.Adapters = []model.ManifestAdapt{{Name: "future-adapter", Version: "v9"}}
	if _, err := pushV2(t.Context(), v2, manifest, &internalBlobSource{path: writeInternalBlob(t, "alpha")}); err != nil {
		t.Fatal(err)
	}
	reader, err := v2.PreparePull(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	plan, err := reader.PlanDownload(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	err = plan.RequireAdapters(t.Context(), nil, map[string]string{"pi": "test"})
	var unsupported *UnsupportedSnapshotAdapterError
	if !errors.As(err, &unsupported) || unsupported.Adapter != "future-adapter" {
		t.Fatalf("RequireAdapters error=%v want future-adapter rejection", err)
	}
}

func TestUnchangedRetryRepairsMissingMachineIndexWithoutReadingBlob(t *testing.T) {
	base := &localStoreV2{root: t.TempDir()}
	v2 := &V2{store: base}
	if err := v2.Init(t.Context()); err != nil {
		t.Fatal(err)
	}
	manifest, _ := singleFileManifest("heal-machine", "steady bytes")
	conflicts := &forcedConditionalConflictsStore{objectStore: base, key: MachinesIndexKey, remaining: conditionalRetryAttempts}
	v2.store = conflicts
	firstSource := &internalBlobSource{path: writeInternalBlob(t, "steady bytes")}
	err := func() error {
		_, err := pushV2(t.Context(), v2, manifest, firstSource)
		return err
	}()
	var contention *ContentionError
	if !errors.As(err, &contention) {
		t.Fatalf("first push error=%v want index contention after pointer publication", err)
	}
	if _, ok, err := v2.Latest(t.Context(), "heal-machine"); err != nil || !ok {
		t.Fatalf("latest pointer was not published before index failure: ok=%v err=%v", ok, err)
	}
	conflicts.remaining = 0
	retrySource := &internalBlobSource{}
	result, err := pushV2(t.Context(), v2, manifest, retrySource)
	if err != nil {
		t.Fatalf("unchanged retry: %v", err)
	}
	if !result.Reused || retrySource.calls != 0 {
		t.Fatalf("retry result=%+v blob_calls=%d", result, retrySource.calls)
	}
	machines, err := v2.Machines(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(machines) != 1 || machines[0] != "heal-machine" {
		t.Fatalf("machines=%v want healed registration", machines)
	}
}

func TestPushCountsOnlyNewlyCreatedBlobObjectsAsUploaded(t *testing.T) {
	base := &localStoreV2{root: t.TempDir()}
	v2 := &V2{store: base}
	if err := v2.Init(t.Context()); err != nil {
		t.Fatal(err)
	}
	manifest, key := singleFileManifest("existing-blob-machine", "already present")
	machine, err := v2.forMachine("existing-blob-machine")
	if err != nil {
		t.Fatal(err)
	}
	path := writeInternalBlob(t, "already present")
	if _, err := machine.ensureBlob(t.Context(), key, path); err != nil {
		t.Fatal(err)
	}
	result, err := pushV2(t.Context(), v2, manifest, &internalBlobSource{path: path})
	if err != nil {
		t.Fatal(err)
	}
	if result.BlobsUploaded != 0 {
		t.Fatalf("BlobsUploaded=%d want 0 for pre-existing object", result.BlobsUploaded)
	}
}

type cancelAfterMachinesStore struct {
	objectStore
	cancel context.CancelFunc
}

func (s *cancelAfterMachinesStore) get(ctx context.Context, key string) ([]byte, string, error) {
	body, etag, err := s.objectStore.get(ctx, key)
	if strings.HasSuffix(key, MachinesIndexKey) && err == nil {
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
	if _, err := v2.store.putBytesIfAbsent(t.Context(), MachinesIndexKey, "application/json", index); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	v2.markerStore = &cancelAfterMachinesStore{objectStore: base, cancel: cancel}
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
	machine, err := v2.forMachine("cancel-machine")
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := machine.ensureBlob(t.Context(), key, path)
	if err != nil {
		t.Fatal(err)
	}
	manifest := model.SnapshotManifest{
		Schema: model.SnapshotManifestSchema, RequiredFeatures: model.RequiredSnapshotFeatures(), MachineID: "cancel-machine", CapturedAt: "2026-07-10T00:00:00Z", CreatedBy: "test",
		Source:   model.ManifestSource{HostOS: "linux"},
		Policy:   model.ManifestPolicy{PathMode: "raw", IncludeSubagents: true, IncludeImages: true, Redaction: "none-v1"},
		Adapters: []model.ManifestAdapt{{Name: "pi", Version: "test"}},
		Files:    []model.ManifestFile{{Source: "pi", Kind: "session", RelativePath: "sources/pi/sessions/one.jsonl", RawPath: "/one.jsonl", SHA256: key.String(), Bytes: int64(len(content)), SessionID: "one", CopyState: "stable"}},
	}
	published, err := machine.publishSnapshot(t.Context(), manifest, []blobReceipt{receipt}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := machine.setLatest(t.Context(), published); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	v2.markerStore = &cancelOnReadStore{objectStore: base, cancel: cancel}
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

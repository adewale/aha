package depot_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/adewale/aha/internal/depot"
	"github.com/adewale/aha/internal/hash"
	"github.com/adewale/aha/internal/model"
	ahaprogress "github.com/adewale/aha/internal/progress"
)

// mapBlobSource serves blob bytes from an in-memory state map and records
// which keys were requested — the push-side read counter.
type mapBlobSource struct {
	t        *testing.T
	dir      string
	byKey    map[string][]byte
	requests []string
}

func newMapBlobSource(t *testing.T, state map[string]string) *mapBlobSource {
	src := &mapBlobSource{t: t, dir: t.TempDir(), byKey: map[string][]byte{}}
	for _, content := range state {
		src.byKey[hash.SHA256Bytes([]byte(content))] = []byte(content)
	}
	return src
}

func (s *mapBlobSource) BlobPath(key model.BlobKey) (string, error) {
	s.requests = append(s.requests, key.String())
	b, ok := s.byKey[key.String()]
	if !ok {
		return "", fmt.Errorf("no content for %s", key)
	}
	p := filepath.Join(s.dir, key.String())
	if err := os.WriteFile(p, b, 0o644); err != nil {
		return "", err
	}
	return p, nil
}

// TestPushV2UnchangedStateIsReusedWithoutWritesOrReads pins acceptance
// property 1 end to end at the push layer: identical state re-pushed is
// recognised from the parent pointer alone — no blob source reads, no
// PUTs, no LISTs.
func TestPushV2ProgressUsesActualUniqueBlobWork(t *testing.T) {
	ctx := context.Background()
	f := newFakeS3(t)
	defer f.Close()
	v2 := depot.NewV2FromR2(f.Depot("bucket"))
	if err := v2.Init(ctx); err != nil {
		t.Fatal(err)
	}
	var events []ahaprogress.Event
	tracker := ahaprogress.NewTracker(ahaprogress.ObserverFunc(func(event ahaprogress.Event) {
		events = append(events, event)
	}), fixedProgressClock{})
	manifest := snapshotManifestFor("m", sessionFile("same", "a.jsonl"), sessionFile("same", "duplicate.jsonl"), sessionFile("different", "b.jsonl"))
	_, err := depot.PushV2WithOptions(ctx, v2, manifest, newMapBlobSource(t, map[string]string{"a": "same", "b": "different"}), depot.PushOptions{Progress: tracker})
	if err != nil {
		t.Fatal(err)
	}
	var uploadCompleted, publishCompleted bool
	for _, event := range events {
		if event.Phase == ahaprogress.PhaseUpload && event.Kind == ahaprogress.Completed {
			uploadCompleted = event.Current == 2 && event.Total.Known && event.Total.Value == 2
		}
		if event.Phase == ahaprogress.PhasePublish && event.Kind == ahaprogress.Completed {
			publishCompleted = true
		}
	}
	if !uploadCompleted || !publishCompleted {
		t.Fatalf("progress events=%+v", events)
	}
}

type fixedProgressClock struct{}

func (fixedProgressClock) Now() time.Time { return time.Unix(0, 0) }

func TestPushV2UnchangedStateIsReusedWithoutWritesOrReads(t *testing.T) {
	ctx := context.Background()
	f := newFakeS3(t)
	defer f.Close()
	v2 := depot.NewV2FromR2(f.Depot("bucket"))
	if err := v2.Init(ctx); err != nil {
		t.Fatal(err)
	}
	state := map[string]string{"a.jsonl": "steady bytes"}
	manifest := snapshotManifestFor("mach-a", sessionFile("steady bytes", "a.jsonl"))
	first, err := depot.PushV2(ctx, v2, manifest, newMapBlobSource(t, state))
	if err != nil {
		t.Fatal(err)
	}
	if first.Reused || first.BlobsUploaded != 1 {
		t.Fatalf("first push: %+v", first)
	}
	f.resetCounts()
	src := newMapBlobSource(t, state)
	second, err := depot.PushV2(ctx, v2, manifest, src)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Reused {
		t.Fatalf("second push not reused: %+v", second)
	}
	if second.ManifestSHA256() != first.ManifestSHA256() {
		t.Fatalf("identity changed across identical pushes")
	}
	if len(src.requests) != 0 {
		t.Fatalf("unchanged push read blob content: %v", src.requests)
	}
	for op := range f.opsSnapshot() {
		if strings.HasPrefix(op, "PUT ") || strings.HasPrefix(op, "LIST") {
			t.Fatalf("unchanged push performed %q", op)
		}
	}
}

// TestPushV2DeltaReadsAndUploadsOnlyNewContent pins acceptance property 2
// at the push layer: carried content is neither read locally nor uploaded.
func TestPushV2DeltaReadsAndUploadsOnlyNewContent(t *testing.T) {
	ctx := context.Background()
	f := newFakeS3(t)
	defer f.Close()
	v2 := depot.NewV2FromR2(f.Depot("bucket"))
	if err := v2.Init(ctx); err != nil {
		t.Fatal(err)
	}
	oldState := map[string]string{"a.jsonl": "old content"}
	if _, err := depot.PushV2(ctx, v2, snapshotManifestFor("mach-a", sessionFile("old content", "a.jsonl")), newMapBlobSource(t, oldState)); err != nil {
		t.Fatal(err)
	}
	f.resetCounts()
	grown := map[string]string{"a.jsonl": "old content", "b.jsonl": "new content"}
	src := newMapBlobSource(t, grown)
	res, err := depot.PushV2(ctx, v2, snapshotManifestFor("mach-a", sessionFile("old content", "a.jsonl"), sessionFile("new content", "b.jsonl")), src)
	if err != nil {
		t.Fatal(err)
	}
	if res.Reused || res.BlobsUploaded != 1 || res.BlobsCarried != 1 {
		t.Fatalf("delta push: %+v", res)
	}
	newKey := hash.SHA256Bytes([]byte("new content"))
	if len(src.requests) != 1 || src.requests[0] != newKey {
		t.Fatalf("delta push read %v, want only the new blob %s", src.requests, newKey)
	}
}

// TestPushV2FirstPushUploadsEverything pins the cold-start path.
func TestPushV2FirstPushUploadsEverything(t *testing.T) {
	ctx := context.Background()
	v2 := newLocalV2(t)
	state := map[string]string{"a.jsonl": "content a", "b.jsonl": "content b"}
	res, err := depot.PushV2(ctx, v2, snapshotManifestFor("mach-a", sessionFile("content a", "a.jsonl"), sessionFile("content b", "b.jsonl")), newMapBlobSource(t, state))
	if err != nil {
		t.Fatal(err)
	}
	if res.Reused || res.BlobsUploaded != 2 || res.BlobsCarried != 0 || res.Files != 2 {
		t.Fatalf("first push: %+v", res)
	}
	latest, ok, err := v2.Latest(ctx, "mach-a")
	if err != nil || !ok || latest != res.ManifestSHA256() {
		t.Fatalf("pointer after first push: %v ok=%v err=%v", latest, ok, err)
	}
}

// TestPushV2UnchangedStateWithNewTimestampIsReused pins that reuse is
// decided by captured STATE, not by capture time: a daily refresh of an
// unchanged machine reuses the parent snapshot (zero writes) even though
// captured_at differs, and reports the parent's identity.
func TestPushV2UnchangedStateWithNewTimestampIsReused(t *testing.T) {
	ctx := context.Background()
	f := newFakeS3(t)
	defer f.Close()
	v2 := depot.NewV2FromR2(f.Depot("bucket"))
	if err := v2.Init(ctx); err != nil {
		t.Fatal(err)
	}
	state := map[string]string{"a.jsonl": "same bytes"}
	m1 := snapshotManifestFor("mach-a", sessionFile("same bytes", "a.jsonl"))
	first, err := depot.PushV2(ctx, v2, m1, newMapBlobSource(t, state))
	if err != nil {
		t.Fatal(err)
	}
	m2 := snapshotManifestFor("mach-a", sessionFile("same bytes", "a.jsonl"))
	m2.CapturedAt = "2026-06-10T00:00:00Z"
	f.resetCounts()
	src := newMapBlobSource(t, state)
	second, err := depot.PushV2(ctx, v2, m2, src)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Reused {
		t.Fatalf("unchanged state with new timestamp not reused: %+v", second)
	}
	if second.ManifestSHA256() != first.ManifestSHA256() {
		t.Fatalf("reuse must report the parent identity: %s vs %s", second.ManifestSHA256(), first.ManifestSHA256())
	}
	if len(src.requests) != 0 {
		t.Fatalf("reused push read blobs: %v", src.requests)
	}
	for op := range f.opsSnapshot() {
		if strings.HasPrefix(op, "PUT ") {
			t.Fatalf("reused push wrote %q", op)
		}
	}
}

package archive_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/adewale/aha/internal/adapters"
	"github.com/adewale/aha/internal/archive"
	ahaclock "github.com/adewale/aha/internal/clock"
	"github.com/adewale/aha/internal/config"
	"github.com/adewale/aha/internal/hash"
	"github.com/adewale/aha/internal/model"
	ahaprogress "github.com/adewale/aha/internal/progress"
	"github.com/adewale/aha/internal/testutil"
)

type fixedClock struct{ at time.Time }

func (c fixedClock) Now() time.Time { return c.at }

var _ ahaclock.Clock = fixedClock{}

func writeSession(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestCaptureCacheRoundTripAndInvalidation pins the advisory scan cache:
// hits require identical size+mtime+inode recorded strictly before the
// cache was written; any visible change is a miss.
func TestCaptureCacheRoundTripAndInvalidation(t *testing.T) {
	dir := t.TempDir()
	p := writeSession(t, dir, "s.jsonl", `{"id":"a"}`+"\n")
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(p, old, old); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	cachePath := filepath.Join(dir, "cache.json")
	sha := hash.SHA256Bytes([]byte(`{"id":"a"}` + "\n"))
	c := archive.LoadCaptureCache(cachePath, fixedClock{at: time.Now()})
	if _, ok := c.Lookup(p, st); ok {
		t.Fatal("empty cache reported a hit")
	}
	c.Record(p, st, sha)
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	reloaded := archive.LoadCaptureCache(cachePath, fixedClock{at: time.Now()})
	got, ok := reloaded.Lookup(p, st)
	if !ok || got != sha {
		t.Fatalf("Lookup after reload: sha=%q ok=%v", got, ok)
	}
	// Size change -> miss.
	if err := os.WriteFile(p, []byte(`{"id":"a","x":1}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, old, old); err != nil {
		t.Fatal(err)
	}
	st2, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reloaded.Lookup(p, st2); ok {
		t.Fatal("cache hit despite size change")
	}
	// Mtime change -> miss.
	if err := os.WriteFile(p, []byte(`{"id":"a"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	newer := old.Add(time.Hour)
	if err := os.Chtimes(p, newer, newer); err != nil {
		t.Fatal(err)
	}
	st3, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reloaded.Lookup(p, st3); ok {
		t.Fatal("cache hit despite mtime change")
	}
}

// TestCaptureCacheRacyMtimeNeverTrusted pins git's racy rule: an entry
// whose file mtime is not strictly older than the cache write time is
// never trusted, closing the same-second modification window.
func TestCaptureCacheRacyMtimeNeverTrusted(t *testing.T) {
	dir := t.TempDir()
	p := writeSession(t, dir, "s.jsonl", "content\n")
	now := time.Now()
	if err := os.Chtimes(p, now, now); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	cachePath := filepath.Join(dir, "cache.json")
	// The cache is written at exactly the file's mtime: same-second race.
	c := archive.LoadCaptureCache(cachePath, fixedClock{at: st.ModTime()})
	c.Record(p, st, hash.SHA256Bytes([]byte("content\n")))
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	reloaded := archive.LoadCaptureCache(cachePath, fixedClock{at: st.ModTime()})
	if _, ok := reloaded.Lookup(p, st); ok {
		t.Fatal("cache trusted an entry inside the racy-mtime window")
	}
}

// TestCaptureCacheCorruptFileSelfHeals pins the escape hatch: a damaged
// or garbage cache loads as empty (one slow rescan), never an error.
func TestCaptureCacheCorruptFileSelfHeals(t *testing.T) {
	dir := t.TempDir()
	cachePath := filepath.Join(dir, "cache.json")
	if err := os.WriteFile(cachePath, []byte("not json at all"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := archive.LoadCaptureCache(cachePath, fixedClock{at: time.Now()})
	p := writeSession(t, dir, "s.jsonl", "x\n")
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Lookup(p, st); ok {
		t.Fatal("corrupt cache produced a hit")
	}
}

func TestCaptureStateTempDirectoryFailureTerminatesProgress(t *testing.T) {
	badTemp := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(badTemp, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", badTemp)
	var events []ahaprogress.Event
	tracker := ahaprogress.NewTracker(ahaprogress.ObserverFunc(func(event ahaprogress.Event) {
		events = append(events, event)
	}), fixedClock{at: time.Unix(0, 0)})
	cfg := model.Config{MachineID: "machine"}
	_, err := archive.CaptureState(t.Context(), cfg, map[string]adapters.SourceAdapter{}, archive.StateOptions{CapturedAt: "2026-07-10T00:00:00Z", Progress: tracker})
	if err == nil {
		t.Fatal("CaptureState unexpectedly succeeded")
	}
	if len(events) < 2 || events[0].Kind != ahaprogress.Started || events[len(events)-1].Kind != ahaprogress.Failed {
		t.Fatalf("events=%+v want started then failed", events)
	}
}

func TestCaptureStateProgressCountsDiscoveredFilesWithoutPaths(t *testing.T) {
	root := t.TempDir()
	fx := testutil.WriteAgentFixtures(t, root)
	cfg := config.Default()
	cfg.MachineID = "m1"
	cfg.Sources = []model.SourceConfig{{Type: "pi", Root: fx.PiRoot, Enabled: true}}
	var events []ahaprogress.Event
	tracker := ahaprogress.NewTracker(ahaprogress.ObserverFunc(func(event ahaprogress.Event) { events = append(events, event) }), fixedClock{at: time.Unix(0, 0)})
	sc, err := archive.CaptureState(context.Background(), cfg, adapters.Builtins(), archive.StateOptions{CapturedAt: "2026-06-09T00:00:00Z", Progress: tracker})
	if err != nil {
		t.Fatal(err)
	}
	defer sc.Close()
	var completed *ahaprogress.Event
	for i := range events {
		if events[i].Phase == ahaprogress.PhaseCapture && events[i].Kind == ahaprogress.Completed {
			completed = &events[i]
		}
	}
	if completed == nil || completed.Current != uint64(len(sc.Manifest.Files)) || !completed.Total.Known || completed.Total.Value != uint64(len(sc.Manifest.Files)) {
		t.Fatalf("files=%d events=%+v", len(sc.Manifest.Files), events)
	}
	joined := fmt.Sprint(events)
	if strings.Contains(joined, root) || strings.Contains(joined, cfg.MachineID) {
		t.Fatalf("progress leaked sensitive identity/path: %s", joined)
	}
}

// TestCaptureStateSkipsReadsOnCacheHit pins the scan layer's contract and
// its documented residual: with a warm cache, unchanged-looking files are
// not re-read (the stale sha is returned), and a nil cache (--force)
// re-reads and returns the truth. CaptureState never parses sessions.
func TestCaptureStateSkipsReadsOnCacheHit(t *testing.T) {
	root := t.TempDir()
	fx := testutil.WriteAgentFixtures(t, root)
	cfg := config.Default()
	cfg.MachineID = "m1"
	cfg.Sources = []model.SourceConfig{{Type: "pi", Root: fx.PiRoot, Enabled: true}}
	cachePath := filepath.Join(root, "capture-cache.json")

	cold := archive.LoadCaptureCache(cachePath, ahaclock.RealClock{})
	sc1, err := archive.CaptureState(context.Background(), cfg, adapters.Builtins(), archive.StateOptions{CapturedAt: "2026-06-09T00:00:00Z", Cache: cold})
	if err != nil {
		t.Fatal(err)
	}
	defer sc1.Close()
	if err := cold.Save(); err != nil {
		t.Fatal(err)
	}
	if len(sc1.Manifest.Files) == 0 {
		t.Fatal("no files captured")
	}
	target := sc1.Manifest.Files[0]
	oldSHA := target.SHA256

	// Flip one byte, keep size, restore mtime to dodge the stat check —
	// then prove the cache was used (old sha, no read) and that --force
	// reads the truth.
	st, err := os.Stat(target.RawPath)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(target.RawPath)
	if err != nil {
		t.Fatal(err)
	}
	b[len(b)-2] ^= 0x01
	if err := os.WriteFile(target.RawPath, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(target.RawPath, st.ModTime(), st.ModTime()); err != nil {
		t.Fatal(err)
	}

	warm := archive.LoadCaptureCache(cachePath, ahaclock.RealClock{})
	sc2, err := archive.CaptureState(context.Background(), cfg, adapters.Builtins(), archive.StateOptions{CapturedAt: "2026-06-09T01:00:00Z", Cache: warm})
	if err != nil {
		t.Fatal(err)
	}
	defer sc2.Close()
	got := fileBySHAPath(t, sc2.Manifest.Files, target.RelativePath)
	if got.SHA256 != oldSHA {
		t.Fatalf("warm capture re-read the file: sha=%s want cached %s", got.SHA256, oldSHA)
	}

	forced, err := archive.CaptureState(context.Background(), cfg, adapters.Builtins(), archive.StateOptions{CapturedAt: "2026-06-09T02:00:00Z", Cache: nil})
	if err != nil {
		t.Fatal(err)
	}
	defer forced.Close()
	fresh := fileBySHAPath(t, forced.Manifest.Files, target.RelativePath)
	if fresh.SHA256 == oldSHA {
		t.Fatal("forced capture returned the stale cached sha")
	}
	if fresh.SHA256 != hash.SHA256Bytes(b) {
		t.Fatalf("forced capture sha=%s want hash of current bytes", fresh.SHA256)
	}
}

func fileBySHAPath(t *testing.T, files []model.ManifestFile, rel string) model.ManifestFile {
	t.Helper()
	for _, f := range files {
		if f.RelativePath == rel {
			return f
		}
	}
	t.Fatalf("file %s not in manifest", rel)
	return model.ManifestFile{}
}

// TestCaptureStateBlobPathProvidesBytesOnDemand pins that a cache-hit file
// whose blob is still needed (not carried by the parent) can be read on
// demand, and that the read verifies the manifest's claimed hash.
func TestCaptureStateBlobPathProvidesBytesOnDemand(t *testing.T) {
	root := t.TempDir()
	fx := testutil.WriteAgentFixtures(t, root)
	cfg := config.Default()
	cfg.MachineID = "m1"
	cfg.Sources = []model.SourceConfig{{Type: "pi", Root: fx.PiRoot, Enabled: true}}
	cachePath := filepath.Join(root, "capture-cache.json")
	cold := archive.LoadCaptureCache(cachePath, ahaclock.RealClock{})
	sc1, err := archive.CaptureState(context.Background(), cfg, adapters.Builtins(), archive.StateOptions{CapturedAt: "2026-06-09T00:00:00Z", Cache: cold})
	if err != nil {
		t.Fatal(err)
	}
	defer sc1.Close()
	if err := cold.Save(); err != nil {
		t.Fatal(err)
	}
	warm := archive.LoadCaptureCache(cachePath, ahaclock.RealClock{})
	sc2, err := archive.CaptureState(context.Background(), cfg, adapters.Builtins(), archive.StateOptions{CapturedAt: "2026-06-09T01:00:00Z", Cache: warm})
	if err != nil {
		t.Fatal(err)
	}
	defer sc2.Close()
	mf := sc2.Manifest.Files[0]
	key, err := model.NewBlobKey(mf.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	path, err := sc2.BlobPath(key)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if hash.SHA256Bytes(b) != mf.SHA256 {
		t.Fatalf("BlobPath bytes hash to %s, manifest claims %s", hash.SHA256Bytes(b), mf.SHA256)
	}
	unknown, err := model.NewBlobKey(hash.SHA256Bytes([]byte("not captured")))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sc2.BlobPath(unknown); err == nil {
		t.Fatal("BlobPath served a key the capture does not contain")
	}
}

type settableClock struct{ at time.Time }

func (c *settableClock) Now() time.Time { return c.at }

// TestCaptureCacheRacyAnchorIsLoadTimeNotSaveTime pins the racy-mtime
// anchor to when the cache was OPENED, not when it was saved: a file
// whose mtime falls inside the capture window must never be trusted on
// the next run, even though Save happened much later. Anchoring at save
// time would leave a coarse-mtime-granularity window in which a
// modification during capture is silently masked.
func TestCaptureCacheRacyAnchorIsLoadTimeNotSaveTime(t *testing.T) {
	dir := t.TempDir()
	loadTime := time.Now().Add(-time.Hour)
	clk := &settableClock{at: loadTime}
	cachePath := filepath.Join(dir, "cache.json")
	c := archive.LoadCaptureCache(cachePath, clk)

	// File hashed mid-capture: its mtime is after the cache was opened.
	p := writeSession(t, dir, "s.jsonl", "captured mid-run\n")
	mid := loadTime.Add(30 * time.Minute)
	if err := os.Chtimes(p, mid, mid); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	c.Record(p, st, hash.SHA256Bytes([]byte("captured mid-run\n")))
	clk.at = loadTime.Add(time.Hour) // Save happens long after the record
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	reloaded := archive.LoadCaptureCache(cachePath, &settableClock{at: time.Now()})
	if _, ok := reloaded.Lookup(p, st); ok {
		t.Fatal("entry recorded inside the capture window was trusted; the racy anchor must be the cache open time")
	}
}

// TestCaptureCachePrunesDeletedFiles pins that the cache does not grow
// without bound as session files are deleted: entries for paths the
// capture no longer sees are dropped on save.
func TestCaptureCachePrunesDeletedFiles(t *testing.T) {
	root := t.TempDir()
	fx := testutil.WriteAgentFixtures(t, root)
	cfg := config.Default()
	cfg.MachineID = "m1"
	cfg.Sources = []model.SourceConfig{{Type: "pi", Root: fx.PiRoot, Enabled: true}}
	cachePath := filepath.Join(root, "capture-cache.json")
	cold := archive.LoadCaptureCache(cachePath, ahaclock.RealClock{})
	sc, err := archive.CaptureState(context.Background(), cfg, adapters.Builtins(), archive.StateOptions{CapturedAt: "2026-06-09T00:00:00Z", Cache: cold})
	if err != nil {
		t.Fatal(err)
	}
	victim := sc.Manifest.Files[0].RawPath
	sc.Close()
	if err := cold.Save(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(victim); err != nil {
		t.Fatal(err)
	}
	warm := archive.LoadCaptureCache(cachePath, ahaclock.RealClock{})
	sc2, err := archive.CaptureState(context.Background(), cfg, adapters.Builtins(), archive.StateOptions{CapturedAt: "2026-06-09T01:00:00Z", Cache: warm})
	if err != nil {
		t.Fatal(err)
	}
	sc2.Close()
	if err := warm.Save(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), victim) {
		t.Fatalf("cache still holds the deleted file %s", victim)
	}
}

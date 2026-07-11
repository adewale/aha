package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/adewale/aha/internal/corpus"
	ahaprogress "github.com/adewale/aha/internal/progress"
)

func TestProgressModeSelectionProtectsFinalJSON(t *testing.T) {
	cases := []struct {
		requested string
		finalJSON bool
		tty       bool
		want      progressMode
	}{
		{"auto", true, true, progressTTY},
		{"auto", true, false, progressOff},
		{"auto", false, true, progressTTY},
		{"auto", false, false, progressPlain},
		{"json", true, false, progressJSON},
		{"off", false, true, progressOff},
	}
	for _, tc := range cases {
		got, err := selectProgressMode(tc.requested, tc.finalJSON, tc.tty)
		if err != nil || got != tc.want {
			t.Fatalf("selectProgressMode(%q,json=%v,tty=%v)=%q err=%v want %q", tc.requested, tc.finalJSON, tc.tty, got, err, tc.want)
		}
	}
}

func TestDisabledProgressConstructsNoTracker(t *testing.T) {
	session, err := newProgressSession(&bytes.Buffer{}, "auto", true)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if session.Tracker != nil {
		t.Fatal("disabled progress constructed a live tracker")
	}
}

func TestPlainProgressUsesStablePhaseLinesWithoutInventingPercent(t *testing.T) {
	var stderr bytes.Buffer
	r := newProgressRenderer(&stderr, progressPlain)
	r.Observe(ahaprogress.Event{Kind: ahaprogress.Started, Phase: ahaprogress.PhaseCapture, Total: ahaprogress.UnknownTotal(), Unit: ahaprogress.UnitFiles})
	r.Observe(ahaprogress.Event{Kind: ahaprogress.Advanced, Phase: ahaprogress.PhaseCapture, Current: 2, Total: ahaprogress.UnknownTotal(), Unit: ahaprogress.UnitFiles})
	r.Observe(ahaprogress.Event{Kind: ahaprogress.Completed, Phase: ahaprogress.PhaseCapture, Current: 2, Total: ahaprogress.UnknownTotal(), Unit: ahaprogress.UnitFiles, Elapsed: 2 * time.Second})
	_ = r.Close()
	text := stderr.String()
	if strings.Count(text, "phase=capture") != 2 || strings.Contains(text, "%") || strings.Contains(text, "NaN") || strings.Contains(text, "eta") {
		t.Fatalf("plain progress=%q", text)
	}
}

func TestTTYProgressUpdatesOneLineAndCapsPercentage(t *testing.T) {
	var stderr bytes.Buffer
	r := newProgressRenderer(&stderr, progressTTY)
	r.Observe(ahaprogress.Event{Kind: ahaprogress.Advanced, Phase: ahaprogress.PhaseUpload, Current: 20, Total: ahaprogress.KnownTotal(10), Unit: ahaprogress.UnitBlobs})
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	text := stderr.String()
	if !strings.Contains(text, "\r") || !strings.Contains(text, "100%") || !strings.HasSuffix(text, "\n") {
		t.Fatalf("TTY progress=%q", text)
	}
}

func TestJSONProgressIsNDJSONOnItsOwnWriter(t *testing.T) {
	var stderr bytes.Buffer
	r := newProgressRenderer(&stderr, progressJSON)
	r.Observe(ahaprogress.Event{Kind: ahaprogress.Completed, Phase: ahaprogress.PhaseVerifyBlobs, Current: 4, Total: ahaprogress.KnownTotal(4), Unit: ahaprogress.UnitBlobs, Elapsed: 1500 * time.Millisecond})
	_ = r.Close()
	lines := strings.Split(strings.TrimSpace(stderr.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("NDJSON lines=%q", lines)
	}
	var event map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &event); err != nil {
		t.Fatal(err)
	}
	if event["phase"] != "verify_blobs" || event["elapsed_ms"] != float64(1500) {
		t.Fatalf("event=%v", event)
	}
}

func TestVerifyJSONProgressStaysOnStderrAndFinalJSONStaysDecodable(t *testing.T) {
	root := filepath.Join(t.TempDir(), "corpus")
	store, err := corpus.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	_ = store.Close()
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"verify", "--corpus", root, "--json", "--progress=json"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	var final map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &final); err != nil {
		t.Fatalf("final stdout is not one JSON document: %v\n%s", err, stdout.String())
	}
	for _, line := range strings.Split(strings.TrimSpace(stderr.String()), "\n") {
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil || event["phase"] == nil {
			t.Fatalf("progress line is not NDJSON: %q err=%v", line, err)
		}
	}
	stdout.Reset()
	stderr.Reset()
	if err := Run([]string{"verify", "--corpus", root, "--json"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("automatic progress must be off for JSON: %q", stderr.String())
	}
}

func TestStructuredProgressFailureKeepsStderrValidNDJSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := RunMain([]string{"verify", "--repo", filepath.Join(t.TempDir(), "missing"), "--json", "--progress=json"}, &stdout, &stderr)
	if code == 0 {
		t.Fatal("verify unexpectedly succeeded")
	}
	if stdout.Len() != 0 {
		t.Fatalf("failure wrote stdout: %s", stdout.String())
	}
	lines := strings.Split(strings.TrimSpace(stderr.String()), "\n")
	if len(lines) < 2 {
		t.Fatalf("stderr=%q want progress and terminal error events", stderr.String())
	}
	for i, line := range lines {
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("stderr line %d is not JSON: %v\n%s", i, err, line)
		}
	}
	var terminal struct {
		Schema string `json:"schema"`
	}
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &terminal); err != nil || terminal.Schema != "aha.error.v1" {
		t.Fatalf("terminal line=%q schema=%q err=%v", lines[len(lines)-1], terminal.Schema, err)
	}
}

func TestOperationalFailureEmitsTerminalProgress(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := RunContext(t.Context(), []string{"verify", "--repo", filepath.Join(t.TempDir(), "missing"), "--progress=plain"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("verify unexpectedly succeeded")
	}
	if !strings.Contains(stderr.String(), "phase=verify state=failed") {
		t.Fatalf("stderr=%q want terminal failed event", stderr.String())
	}
}

func TestRunContextCancellationStopsVerifyAndRendersCancellation(t *testing.T) {
	root := filepath.Join(t.TempDir(), "corpus")
	store, err := corpus.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	_ = store.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var stderr bytes.Buffer
	err = RunContext(ctx, []string{"verify", "--corpus", root, "--progress=plain"}, &bytes.Buffer{}, &stderr)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("RunContext error=%v want context.Canceled", err)
	}
	if !strings.Contains(stderr.String(), "state=cancelled") {
		t.Fatalf("cancellation progress missing: %q", stderr.String())
	}
}

func TestProgressRendererConcurrentObserveAndIdempotentClose(t *testing.T) {
	var stderr bytes.Buffer
	r := newProgressRenderer(&stderr, progressJSON)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.Observe(ahaprogress.Event{Kind: ahaprogress.Advanced, Phase: ahaprogress.PhaseUpload})
		}()
	}
	wg.Wait()
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	r.Observe(ahaprogress.Event{Kind: ahaprogress.Completed, Phase: ahaprogress.PhaseUpload})
}

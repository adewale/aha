package corpus_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/adewale/aha/internal/adapters"
	"github.com/adewale/aha/internal/archive"
	"github.com/adewale/aha/internal/config"
	"github.com/adewale/aha/internal/corpus"
	"github.com/adewale/aha/internal/model"
)

// writeClaudeSession writes a Claude Code project/session JSONL file with the
// given lines under a fake projects root, returning the root.
func writeClaudeSession(t *testing.T, root, sessionFile string, lines []string) {
	t.Helper()
	projDir := filepath.Join(root, "-Users-me-proj")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := ""
	for _, l := range lines {
		body += l + "\n"
	}
	if err := os.WriteFile(filepath.Join(projDir, sessionFile), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func captureBundle(t *testing.T, claudeRoot, dest, bundleID, capturedAt string) string {
	t.Helper()
	cfg := config.Default()
	cfg.MachineID = "test-machine"
	cfg.Sources = []model.SourceConfig{{Type: "claude-code", Root: claudeRoot, Enabled: true}}
	b, err := archive.Capture(t.Context(), cfg, adapters.Builtins(), archive.Options{CapturedAt: capturedAt, BundleID: bundleID})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dest, bundleID+".tar.zst")
	if _, err := archive.Write(path, b); err != nil {
		t.Fatal(err)
	}
	return path
}

const (
	claudeOpenerFail = `{"type":"assistant","uuid":"c1","timestamp":"2026-01-01T00:00:01Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"tu1","name":"Bash","input":{"command":"confluent topic describe foo"}}]}}`
	claudeResultFail = `{"type":"user","uuid":"r1","parentUuid":"c1","timestamp":"2026-01-01T00:00:02Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu1","is_error":true,"content":"unknown topic or partition"}]}}`
	claudeRetryOK    = `{"type":"assistant","uuid":"c2","timestamp":"2026-01-01T00:00:03Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"tu2","name":"Bash","input":{"command":"confluent topic describe foo"}}]}}`
	claudeResultOK   = `{"type":"user","uuid":"r2","parentUuid":"c2","timestamp":"2026-01-01T00:00:04Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu2","is_error":false,"content":"ok"}]}}`
)

// TestIngestProducesResolvedSkillCandidate drives the real ingest pipeline
// (capture → bundle → IngestBundle) end-to-end and asserts the in-transaction
// failure-episode hook produced a resolved skill candidate. This exercises the
// production path (corpusWriter.insertFailureEpisodes) that the unit tests,
// which seed tool_invocations directly, do not.
func TestIngestProducesResolvedSkillCandidate(t *testing.T) {
	root := t.TempDir()
	claudeRoot := filepath.Join(root, "claude")
	writeClaudeSession(t, claudeRoot, "sess.jsonl", []string{
		claudeOpenerFail, claudeResultFail, claudeRetryOK, claudeResultOK,
	})
	bundle := captureBundle(t, claudeRoot, root, "full-arc", "2026-01-03T00:00:00Z")

	store, err := corpus.Open(filepath.Join(root, "corpus"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := corpus.IngestBundle(store, adapters.Builtins(), bundle); err != nil {
		t.Fatalf("ingest: %v", err)
	}

	candidates, err := corpus.Incidents(store.DB, corpus.IncidentFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 {
		t.Fatalf("ingest hook should have produced one resolved candidate, got %d: %+v", len(candidates), candidates)
	}
	if candidates[0].CommandFamily != "confluent topic describe" || candidates[0].Resolved != 1 {
		t.Fatalf("unexpected candidate from ingest: %+v", candidates[0])
	}
}

// TestIngestResumeFlipsAbandonedToResolved is the end-to-end resume contract:
// a first bundle captures only the failing opener (no fix yet → no candidate);
// a second bundle of the grown session adds the resolving success, and
// re-ingesting must flip the episode to resolved rather than keep a stale row.
func TestIngestResumeFlipsAbandonedToResolved(t *testing.T) {
	root := t.TempDir()
	claudeRoot := filepath.Join(root, "claude")

	// First capture: only the failing opener exists.
	writeClaudeSession(t, claudeRoot, "sess.jsonl", []string{claudeOpenerFail, claudeResultFail})
	bundle1 := captureBundle(t, claudeRoot, root, "open-only", "2026-01-03T00:00:00Z")

	store, err := corpus.Open(filepath.Join(root, "corpus"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := corpus.IngestBundle(store, adapters.Builtins(), bundle1); err != nil {
		t.Fatalf("ingest bundle1: %v", err)
	}
	incidents, err := corpus.Incidents(store.DB, corpus.IncidentFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(incidents) != 1 || incidents[0].State != "unresolved" {
		t.Fatalf("the opener should be one unresolved incident before the fix: %+v", incidents)
	}

	// Resume: the session grows with the resolving success; re-capture + ingest.
	writeClaudeSession(t, claudeRoot, "sess.jsonl", []string{
		claudeOpenerFail, claudeResultFail, claudeRetryOK, claudeResultOK,
	})
	bundle2 := captureBundle(t, claudeRoot, root, "with-fix", "2026-01-03T01:00:00Z")
	if _, err := corpus.IngestBundle(store, adapters.Builtins(), bundle2); err != nil {
		t.Fatalf("ingest bundle2: %v", err)
	}
	incidents, err = corpus.Incidents(store.DB, corpus.IncidentFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(incidents) != 1 || incidents[0].State != "resolved" {
		t.Fatalf("after resume the episode must flip to resolved: %+v", incidents)
	}
	if incidents[0].Resolved != 1 || incidents[0].Episodes != 1 {
		t.Fatalf("resume should yield exactly one resolved episode, got resolved=%d episodes=%d", incidents[0].Resolved, incidents[0].Episodes)
	}
}

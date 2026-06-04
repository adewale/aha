package corpus_test

import (
	"path/filepath"
	"testing"

	"github.com/adewale/aha/internal/adapters"
	"github.com/adewale/aha/internal/archive"
	"github.com/adewale/aha/internal/corpus"
	"github.com/adewale/aha/internal/hash"
	"github.com/adewale/aha/internal/model"
)

// TestLiveContextRespectsCompaction pins priority 3 from
// docs/research/cross-agent-data-capture.md: when a Pi `compaction`
// entry sits on the parent_id path between a leaf and the root,
// LiveContext returns the compaction's summary plus every entry from
// firstKeptEntryId forward to the leaf — and *not* the entries before
// firstKeptEntryId that the compaction replaced.
func TestLiveContextRespectsCompaction(t *testing.T) {
	// Conversation:
	//   u1 (user) → a1 (assistant) → t1 (tool result) → u2 (user, kept)
	//   → comp1 (compaction, firstKeptEntryId=u2) → a3 (assistant, leaf)
	// LiveContext(leaf=a3) should be [comp1, u2, a3] —
	// u1/a1/t1 were compacted out; comp1 stands in for them.
	store, key := corpusWithCompactedSession(t)
	defer store.Close()

	got, err := corpus.LiveContext(store.DB, key.String(), "a3")
	if err != nil {
		t.Fatal(err)
	}
	wantIDs := []string{"comp1", "u2", "a3"}
	assertLiveContextEntries(t, got, wantIDs)
}

// TestLiveContextSkipsCustomNonParticipatingEntries pins the
// participates_in_context bit: Pi `custom` entries (extension state
// that does not enter the LLM context) are excluded from LiveContext,
// while `custom_message` entries (extension state that DOES enter
// context) are included.
func TestLiveContextSkipsCustomNonParticipatingEntries(t *testing.T) {
	store, key := corpusWithCustomEntries(t)
	defer store.Close()

	got, err := corpus.LiveContext(store.DB, key.String(), "leaf")
	if err != nil {
		t.Fatal(err)
	}
	// Tree: u1 → cust-state (custom, skip) → cust-msg (custom_message, keep) → leaf
	wantIDs := []string{"u1", "cust-msg", "leaf"}
	assertLiveContextEntries(t, got, wantIDs)
}

func TestLiveContextRejectsCompactionAnchorOutsideBranch(t *testing.T) {
	lines := []string{
		`{"type":"session","version":3,"id":"bad-anchor","timestamp":"2026-01-01T00:00:00Z","cwd":"/tmp"}`,
		`{"type":"message","id":"u1","timestamp":"2026-01-01T00:00:01Z","message":{"role":"user","content":"first"}}`,
		`{"type":"compaction","id":"comp1","parentId":"u1","timestamp":"2026-01-01T00:00:02Z","summary":"bad compaction","firstKeptEntryId":"missing-anchor","tokensBefore":10}`,
		`{"type":"message","id":"leaf","parentId":"comp1","timestamp":"2026-01-01T00:00:03Z","message":{"role":"assistant","content":"answer"}}`,
	}
	store, key := ingestPiLines(t, "bad-anchor", lines)
	defer store.Close()

	_, err := corpus.LiveContext(store.DB, key.String(), "leaf")
	if err == nil {
		t.Fatalf("LiveContext accepted a compaction whose firstKeptEntryId is not on the branch")
	}
}

func assertLiveContextEntries(t *testing.T, got []corpus.ReadEntry, wantIDs []string) {
	t.Helper()
	if len(got) != len(wantIDs) {
		t.Fatalf("LiveContext returned %d entries, want %d: %+v", len(got), len(wantIDs), got)
	}
	for i, w := range wantIDs {
		if got[i].EntryID != w {
			t.Fatalf("entry %d EntryID=%q want %q (full path: %+v)", i, got[i].EntryID, w, got)
		}
	}
}

func corpusWithCompactedSession(t *testing.T) (*corpus.Store, model.SessionKey) {
	t.Helper()
	lines := []string{
		`{"type":"session","version":3,"id":"compacted","timestamp":"2026-01-01T00:00:00Z","cwd":"/tmp"}`,
		`{"type":"message","id":"u1","timestamp":"2026-01-01T00:00:01Z","message":{"role":"user","content":[{"type":"text","text":"first turn"}]}}`,
		`{"type":"message","id":"a1","parentId":"u1","timestamp":"2026-01-01T00:00:02Z","message":{"role":"assistant","content":[{"type":"text","text":"first answer"}]}}`,
		`{"type":"message","id":"t1","parentId":"a1","timestamp":"2026-01-01T00:00:03Z","message":{"role":"toolResult","toolName":"Bash","content":[{"type":"text","text":"first tool output"}]}}`,
		`{"type":"message","id":"u2","parentId":"t1","timestamp":"2026-01-01T00:00:04Z","message":{"role":"user","content":[{"type":"text","text":"kept user prompt"}]}}`,
		`{"type":"compaction","id":"comp1","parentId":"u2","timestamp":"2026-01-01T00:00:05Z","summary":"context compacted","firstKeptEntryId":"u2","tokensBefore":40000}`,
		`{"type":"message","id":"a3","parentId":"comp1","timestamp":"2026-01-01T00:00:06Z","message":{"role":"assistant","content":[{"type":"text","text":"third answer"}]}}`,
	}
	return ingestPiLines(t, "compacted", lines)
}

func corpusWithCustomEntries(t *testing.T) (*corpus.Store, model.SessionKey) {
	t.Helper()
	lines := []string{
		`{"type":"session","version":3,"id":"custom","timestamp":"2026-01-01T00:00:00Z","cwd":"/tmp"}`,
		`{"type":"message","id":"u1","timestamp":"2026-01-01T00:00:01Z","message":{"role":"user","content":[{"type":"text","text":"hi"}]}}`,
		`{"type":"custom","id":"cust-state","parentId":"u1","timestamp":"2026-01-01T00:00:02Z","customType":"plugin_state","data":{"counter":3}}`,
		`{"type":"custom_message","id":"cust-msg","parentId":"cust-state","timestamp":"2026-01-01T00:00:03Z","customType":"hint","content":"a hint that goes into context","display":true}`,
		`{"type":"message","id":"leaf","parentId":"cust-msg","timestamp":"2026-01-01T00:00:04Z","message":{"role":"assistant","content":[{"type":"text","text":"answer"}]}}`,
	}
	return ingestPiLines(t, "custom", lines)
}

func ingestPiLines(t *testing.T, sessionID string, lines []string) (*corpus.Store, model.SessionKey) {
	t.Helper()
	var data []byte
	for _, l := range lines {
		data = append(data, []byte(l+"\n")...)
	}
	mf := model.ManifestFile{Source: "pi", Kind: "session", RelativePath: "sources/pi/sessions/" + sessionID + ".jsonl", RawPath: sessionID + ".jsonl", SHA256: hash.SHA256Bytes(data), Bytes: int64(len(data)), SessionID: sessionID, CopyState: "stable"}
	manifest := model.Manifest{Schema: model.BundleSchema, BundleID: "live-context-" + sessionID, MachineID: "m1", CapturedAt: "2026-01-01T00:00:00Z", Policy: model.ManifestPolicy{PathMode: "raw", IncludeSubagents: true, IncludeImages: true, Redaction: "none-v1"}, Files: []model.ManifestFile{mf}}
	bundlePath := filepath.Join(t.TempDir(), "bundle.tar.zst")
	if _, err := archive.Write(bundlePath, archive.Bundle{Manifest: manifest, Files: []model.CapturedFile{{Manifest: mf, Data: data}}}); err != nil {
		t.Fatal(err)
	}
	store, err := corpus.Open(filepath.Join(t.TempDir(), "corpus"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := corpus.IngestBundle(store, adapters.Builtins(), bundlePath); err != nil {
		store.Close()
		t.Fatal(err)
	}
	key, err := model.NewSessionKey("pi", "m1", sessionID)
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	return store, key
}

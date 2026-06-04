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

// TestReadBranchReturnsLeafToRootPath pins the tree-walking read
// behaviour from docs/research/cross-agent-data-capture.md priority 1.
// A branched session has two distinct leaves under a shared root; a
// reader asking for either leaf must see exactly that branch's path —
// not the sibling branch's entries, not the file-order window.
func TestReadBranchReturnsLeafToRootPath(t *testing.T) {
	store, sessionKey := corpusWithBranchedSession(t)
	defer store.Close()

	// Branch A: u1 → a1 → done-1
	got, err := corpus.ReadBranch(store.DB, sessionKey.String(), "done-1")
	if err != nil {
		t.Fatal(err)
	}
	wantIDs := []string{"u1", "a1", "done-1"}
	assertBranchEntries(t, got, wantIDs)

	// Branch B: u1 → alt-a1 → alt-done
	got, err = corpus.ReadBranch(store.DB, sessionKey.String(), "alt-done")
	if err != nil {
		t.Fatal(err)
	}
	wantIDs = []string{"u1", "alt-a1", "alt-done"}
	assertBranchEntries(t, got, wantIDs)
}

func TestReadBranchRejectsUnknownLeaf(t *testing.T) {
	store, sessionKey := corpusWithBranchedSession(t)
	defer store.Close()
	if _, err := corpus.ReadBranch(store.DB, sessionKey.String(), "no-such-entry"); err == nil {
		t.Fatal("ReadBranch accepted unknown entry id")
	}
}

func TestReadBranchRejectsUnknownSession(t *testing.T) {
	store, _ := corpusWithBranchedSession(t)
	defer store.Close()
	if _, err := corpus.ReadBranch(store.DB, "nonexistent:session", "u1"); err == nil {
		t.Fatal("ReadBranch accepted unknown session key")
	}
}

func assertBranchEntries(t *testing.T, got []corpus.ReadEntry, wantIDs []string) {
	t.Helper()
	if len(got) != len(wantIDs) {
		t.Fatalf("ReadBranch returned %d entries, want %d: %+v", len(got), len(wantIDs), got)
	}
	for i, w := range wantIDs {
		if got[i].EntryID != w {
			t.Fatalf("entry %d EntryID=%q want %q (full path: %+v)", i, got[i].EntryID, w, got)
		}
	}
}

// corpusWithBranchedSession builds a Pi-style session with a single
// fork under the user root:
//
//	u1 (user, root)
//	├── a1 (assistant)
//	│   └── done-1 (assistant, leaf of branch A)
//	└── alt-a1 (assistant, alternative)
//	    └── alt-done (assistant, leaf of branch B)
func corpusWithBranchedSession(t *testing.T) (*corpus.Store, model.SessionKey) {
	t.Helper()
	lines := []string{
		`{"type":"session","version":3,"id":"branched","timestamp":"2026-01-01T00:00:00Z","cwd":"/tmp"}`,
		`{"type":"message","id":"u1","timestamp":"2026-01-01T00:00:01Z","message":{"role":"user","content":[{"type":"text","text":"branch test"}]}}`,
		`{"type":"message","id":"a1","parentId":"u1","timestamp":"2026-01-01T00:00:02Z","message":{"role":"assistant","content":[{"type":"text","text":"A path"}]}}`,
		`{"type":"message","id":"done-1","parentId":"a1","timestamp":"2026-01-01T00:00:03Z","message":{"role":"assistant","content":[{"type":"text","text":"A leaf"}]}}`,
		`{"type":"message","id":"alt-a1","parentId":"u1","timestamp":"2026-01-01T00:00:04Z","message":{"role":"assistant","content":[{"type":"text","text":"B path"}]}}`,
		`{"type":"message","id":"alt-done","parentId":"alt-a1","timestamp":"2026-01-01T00:00:05Z","message":{"role":"assistant","content":[{"type":"text","text":"B leaf"}]}}`,
	}
	var data []byte
	for _, l := range lines {
		data = append(data, []byte(l+"\n")...)
	}
	mf := model.ManifestFile{Source: "pi", Kind: "session", RelativePath: "sources/pi/sessions/branched.jsonl", RawPath: "branched.jsonl", SHA256: hash.SHA256Bytes(data), Bytes: int64(len(data)), SessionID: "branched", CopyState: "stable"}
	manifest := model.Manifest{Schema: model.BundleSchema, BundleID: "read-branch", MachineID: "m1", CapturedAt: "2026-01-01T00:00:00Z", Policy: model.ManifestPolicy{PathMode: "raw", IncludeSubagents: true, IncludeImages: true, Redaction: "none-v1"}, Files: []model.ManifestFile{mf}}
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
	key, err := model.NewSessionKey("pi", "m1", "branched")
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	return store, key
}

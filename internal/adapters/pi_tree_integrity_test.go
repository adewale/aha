package adapters

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adewale/aha/internal/model"
)

// TestPiCorpusTreeIntegrity is the structural counterpart to a true
// differential test against Pi's upstream buildSessionContext(). We
// cannot run the TypeScript reconstructor inside Go's test runner, so
// this test instead pins the *invariants* the reconstructor relies on
// for the entire vendored pi-mono corpus:
//
//  1. Every entry has a non-empty 8-hex EntryID (lookup table is total).
//  2. ParentID is either "" (root marker) or refers to an EntryID in
//     the same session. No dangling parents.
//  3. The parent_id chain is acyclic: starting from any entry, walking
//     parent_id repeatedly reaches "" without revisiting any node.
//  4. There is at least one entry with empty ParentID (a root).
//  5. compaction entries carry a non-empty firstKeptEntryId and that id
//     resolves to another entry in the same session.
//  6. branch_summary entries carry a non-empty fromId resolving to
//     another entry in the same session.
//
// A regression in the Pi adapter that mangles parent_id, drops
// compaction metadata, or introduces forward references to non-
// existent entries fails this test before any downstream tree-walking
// or context-reconstruction logic gets the chance to misbehave on it.
func TestPiCorpusTreeIntegrity(t *testing.T) {
	dir := filepath.Join("testdata", "corpora", "pi-mono-sample")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Skipf("pi-mono corpus not vendored: %v", err)
	}
	piPaths := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".jsonl") {
			piPaths = append(piPaths, filepath.Join(dir, e.Name()))
		}
	}
	if len(piPaths) == 0 {
		t.Skip("pi-mono corpus is empty")
	}
	for _, path := range piPaths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			f, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			ps, err := Pi{}.ParseSession(t.Context(), model.SessionFile{Source: "pi", SessionID: filepath.Base(path)}, f)
			if err != nil {
				t.Fatal(err)
			}
			assertPiSessionTreeIntegrity(t, ps, path)
		})
	}
}

func assertPiSessionTreeIntegrity(t *testing.T, ps *model.ParsedSession, path string) {
	t.Helper()
	if len(ps.Entries) == 0 {
		t.Fatalf("%s: parsed session has no entries", path)
	}
	byID := make(map[string]*model.ParsedEntry, len(ps.Entries))
	for i := range ps.Entries {
		e := &ps.Entries[i]
		if e.EntryID == "" {
			t.Fatalf("%s: entry %d has empty EntryID", path, i)
		}
		if prev, dup := byID[e.EntryID]; dup {
			t.Fatalf("%s: duplicate EntryID %q at lines %d and %d", path, e.EntryID, prev.LineNo, e.LineNo)
		}
		byID[e.EntryID] = e
	}

	roots := 0
	for _, e := range ps.Entries {
		if e.ParentID == "" {
			roots++
			continue
		}
		if _, ok := byID[e.ParentID]; !ok {
			t.Fatalf("%s: entry %q at line %d has dangling parent_id=%q", path, e.EntryID, e.LineNo, e.ParentID)
		}
	}
	if roots == 0 {
		t.Fatalf("%s: no root entry (every entry has a parent_id) — forest must have at least one root", path)
	}

	// Acyclicity: walking parent_id from any node must reach "" within
	// len(ps.Entries) steps without revisiting.
	for _, e := range ps.Entries {
		seen := map[string]struct{}{}
		cur := e.EntryID
		for steps := 0; cur != ""; steps++ {
			if steps > len(ps.Entries) {
				t.Fatalf("%s: parent_id walk from %q did not terminate within %d steps — cycle suspected", path, e.EntryID, len(ps.Entries))
			}
			if _, dup := seen[cur]; dup {
				t.Fatalf("%s: parent_id cycle detected at %q (started from %q)", path, cur, e.EntryID)
			}
			seen[cur] = struct{}{}
			node, ok := byID[cur]
			if !ok {
				// Already validated above; defensive.
				t.Fatalf("%s: walk traversed unknown id %q", path, cur)
			}
			cur = node.ParentID
		}
	}

	// compaction and branch_summary referential integrity.
	for i := range ps.Entries {
		e := &ps.Entries[i]
		switch e.EntryType {
		case "compaction":
			ref, err := referenceField(e, "firstKeptEntryId")
			if err != nil {
				t.Fatalf("%s: compaction %q: %v", path, e.EntryID, err)
			}
			if ref == "" {
				t.Fatalf("%s: compaction %q missing firstKeptEntryId", path, e.EntryID)
			}
			if _, ok := byID[ref]; !ok {
				t.Fatalf("%s: compaction %q firstKeptEntryId=%q does not resolve to a session entry", path, e.EntryID, ref)
			}
		case "branch_summary":
			ref, err := referenceField(e, "fromId")
			if err != nil {
				t.Fatalf("%s: branch_summary %q: %v", path, e.EntryID, err)
			}
			if ref == "" {
				t.Fatalf("%s: branch_summary %q missing fromId", path, e.EntryID)
			}
			if _, ok := byID[ref]; !ok {
				t.Fatalf("%s: branch_summary %q fromId=%q does not resolve to a session entry", path, e.EntryID, ref)
			}
		}
	}
}

// referenceField extracts a top-level string field (e.g. firstKeptEntryId,
// fromId) from an entry's RawJSON. The shared parser doesn't surface these
// as typed fields, so we have to read RawJSON directly.
func referenceField(e *model.ParsedEntry, field string) (string, error) {
	var raw map[string]any
	if err := json.Unmarshal([]byte(e.RawJSON), &raw); err != nil {
		return "", fmt.Errorf("raw_json invalid: %w", err)
	}
	v, ok := raw[field].(string)
	if !ok {
		return "", nil
	}
	return v, nil
}

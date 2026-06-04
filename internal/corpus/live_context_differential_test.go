package corpus_test

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/adewale/aha/internal/corpus"
)

// TestLiveContextMatchesReferenceImpl asserts corpus.LiveContext
// matches a clean re-implementation of Pi's buildSessionContext
// algorithm — derived independently from the prose in Pi's
// session-format.md — across every session in the vendored pi-mono
// corpus.
//
// Two implementations of the same spec catches transcription bugs in
// the SQL-based walker: an off-by-one in the compaction stop anchor,
// a wrong summary-before-vs-after ordering, a mis-classified custom
// vs custom_message filter, an incorrect handling of multiple
// compactions on one path. If both implementations agree across
// twenty real sessions, the SQL walker is unlikely to drift from the
// spec by accident.
//
// This is not validation against Pi's actual buildSessionContext()
// output (the reference TypeScript implementation isn't runnable
// here); it is validation that two independent readings of the spec
// agree. A spec-vs-spec match is weaker than a spec-vs-truth match
// but stronger than no differential at all.
func TestLiveContextMatchesReferenceImpl(t *testing.T) {
	corpusDir := filepath.Join("..", "adapters", "testdata", "corpora", "pi-mono-sample")
	dirEntries, err := os.ReadDir(corpusDir)
	if err != nil {
		t.Skipf("pi-mono corpus not vendored: %v", err)
	}
	tested := 0
	for _, de := range dirEntries {
		if de.IsDir() || !strings.HasSuffix(de.Name(), ".jsonl") {
			continue
		}
		t.Run(de.Name(), func(t *testing.T) {
			path := filepath.Join(corpusDir, de.Name())
			raw, err := readJSONLLines(path)
			if err != nil {
				t.Fatal(err)
			}
			ref := parseReferenceSession(t, raw)
			if len(ref.byID) == 0 {
				t.Skip("session has no entries after header strip")
			}
			leaf := pickReferenceLeaf(ref)
			if leaf == "" {
				t.Skip("no leaf with no children — unusual structure")
			}
			refOrder, err := referenceLiveContext(ref, leaf)
			if err != nil {
				t.Fatalf("reference walker: %v", err)
			}

			store, key := ingestPiLines(t, ref.sessionID, raw)
			defer store.Close()
			gotEntries, err := corpus.LiveContext(store.DB, key.String(), leaf)
			if err != nil {
				t.Fatalf("LiveContext: %v", err)
			}
			gotOrder := make([]string, len(gotEntries))
			for i, e := range gotEntries {
				gotOrder[i] = e.EntryID
			}
			if !reflect.DeepEqual(gotOrder, refOrder) {
				t.Fatalf("LiveContext disagrees with reference impl for leaf %q in %s:\n  corpus: %v\n  ref:    %v", leaf, de.Name(), gotOrder, refOrder)
			}
		})
		tested++
	}
	if tested == 0 {
		t.Skip("no .jsonl files in corpus")
	}
}

// readJSONLLines returns each non-blank line from a JSONL file.
func readJSONLLines(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []string
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 1<<20), 8<<20)
	for s.Scan() {
		if strings.TrimSpace(s.Text()) == "" {
			continue
		}
		out = append(out, s.Text())
	}
	return out, s.Err()
}

// referenceEntry holds the minimum fields the buildSessionContext
// algorithm needs. Mirrors the columns LiveContext reads from the DB,
// without going through the parser/ingest pipeline.
type referenceEntry struct {
	id           string
	parentID     string
	entryType    string
	firstKept    string // only set on `compaction` entries
	participates bool
	lineNo       int
}

type referenceSession struct {
	sessionID string
	byID      map[string]referenceEntry
	order     []string // insertion order (for deterministic leaf selection)
}

// parseReferenceSession reads JSONL lines into the minimal graph used
// by the reference walker. It mirrors three pieces of adapter
// behaviour but derives them straight from the source: the Pi session-
// header strip, the parent_id naming variants, and the participates_in_
// context default (false only for `custom` entries, per Pi
// session-format.md).
func parseReferenceSession(t *testing.T, lines []string) referenceSession {
	t.Helper()
	rs := referenceSession{byID: map[string]referenceEntry{}}
	headerStripped := false
	for i, l := range lines {
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("line %d: %v", i+1, err)
		}
		typ := refStr(m, "type")
		if !headerStripped && typ == "session" {
			headerStripped = true
			if id := refStr(m, "id"); id != "" {
				rs.sessionID = id
			}
			continue
		}
		id := refFirstNonEmpty(refStr(m, "id"), refStr(m, "uuid"), refNestedString(m, "message", "id"), refNestedString(m, "message", "uuid"))
		if id == "" {
			continue // anonymous entry; the corpus assigns a stable fallback id but the differential is scoped to nameable entries.
		}
		parent := refFirstNonEmpty(refStr(m, "parentId"), refStr(m, "parent_id"), refStr(m, "parentUuid"))
		e := referenceEntry{
			id:           id,
			parentID:     parent,
			entryType:    typ,
			participates: typ != "custom",
			lineNo:       i + 1,
		}
		if typ == "compaction" {
			e.firstKept = refStr(m, "firstKeptEntryId")
		}
		rs.byID[id] = e
		rs.order = append(rs.order, id)
	}
	if rs.sessionID == "" {
		rs.sessionID = "ref-" + filepathBaseTrim(lines)
	}
	return rs
}

// pickReferenceLeaf returns the natural leaf for a Pi session: the
// last-inserted entry that no other entry's parent_id references. If
// the session has multiple branches, pick the one with the highest
// line number — that matches "most recent" in file order, the same
// signal the corpus produces.
func pickReferenceLeaf(rs referenceSession) string {
	children := map[string]bool{}
	for _, e := range rs.byID {
		if e.parentID != "" {
			children[e.parentID] = true
		}
	}
	var candidates []string
	for id := range rs.byID {
		if !children[id] {
			candidates = append(candidates, id)
		}
	}
	if len(candidates) == 0 {
		return ""
	}
	sort.Slice(candidates, func(i, j int) bool {
		return rs.byID[candidates[i]].lineNo > rs.byID[candidates[j]].lineNo
	})
	return candidates[0]
}

// referenceLiveContext is an independent Go port of the Pi
// buildSessionContext algorithm as described in session-format.md:
//
//   - walk leaf → root via parent_id;
//   - drop entries whose participates_in_context flag is false (Pi
//     `custom`);
//   - when a `compaction` entry is encountered, take note of its
//     firstKeptEntryId and stop the walk once that ancestor is
//     visited;
//   - emit the compaction summaries first (most recent first, by
//     walk order), then the non-compaction entries in root → leaf
//     order. The spec phrases this as "Emits the summary first, Then
//     messages from firstKeptEntryId to compaction, Then messages
//     after compaction."
func referenceLiveContext(rs referenceSession, leafID string) ([]string, error) {
	if _, ok := rs.byID[leafID]; !ok {
		return nil, &refMissingErr{kind: "entry", value: leafID}
	}
	var compactions, nonCompaction []string
	stopAfter := ""
	seen := map[string]bool{}
	cur := leafID
	for cur != "" {
		if seen[cur] {
			return nil, &refMissingErr{kind: "cycle", value: cur}
		}
		seen[cur] = true
		e, ok := rs.byID[cur]
		if !ok {
			return nil, &refMissingErr{kind: "dangling", value: cur}
		}
		if e.participates {
			if e.entryType == "compaction" {
				compactions = append(compactions, e.id)
			} else {
				nonCompaction = append(nonCompaction, e.id)
			}
		}
		if e.entryType == "compaction" && e.firstKept != "" && stopAfter == "" {
			stopAfter = e.firstKept
		}
		if stopAfter != "" && cur == stopAfter {
			break
		}
		cur = e.parentID
	}
	for i, j := 0, len(nonCompaction)-1; i < j; i, j = i+1, j-1 {
		nonCompaction[i], nonCompaction[j] = nonCompaction[j], nonCompaction[i]
	}
	return append(compactions, nonCompaction...), nil
}

type refMissingErr struct{ kind, value string }

func (e *refMissingErr) Error() string { return e.kind + ":" + e.value }

// TestLiveContextMatchesReferenceImplSyntheticEdges exercises the
// algorithmic edges the vendored pi-mono corpus does not (none of its
// twenty sessions contain a compaction or custom entry, so the corpus
// differential only validates the linear leaf→root walk). These
// fixtures put the spec's stated edges — compaction collapse, multiple
// compactions on one path, custom filtering, and compaction+custom
// combined — in front of both implementations and assert agreement.
func TestLiveContextMatchesReferenceImplSyntheticEdges(t *testing.T) {
	cases := []struct {
		name  string
		lines []string
		leaf  string
		want  []string // expected entry-id sequence, asserted against both impls
	}{
		{
			name: "single_compaction_collapses_history",
			lines: []string{
				`{"type":"session","version":3,"id":"diff-compact","timestamp":"2026-01-01T00:00:00Z","cwd":"/tmp"}`,
				`{"type":"message","id":"u1","timestamp":"2026-01-01T00:00:01Z","message":{"role":"user","content":[{"type":"text","text":"first"}]}}`,
				`{"type":"message","id":"a1","parentId":"u1","timestamp":"2026-01-01T00:00:02Z","message":{"role":"assistant","content":[{"type":"text","text":"first answer"}]}}`,
				`{"type":"message","id":"u2","parentId":"a1","timestamp":"2026-01-01T00:00:03Z","message":{"role":"user","content":[{"type":"text","text":"kept"}]}}`,
				`{"type":"compaction","id":"comp1","parentId":"u2","timestamp":"2026-01-01T00:00:04Z","summary":"compacted","firstKeptEntryId":"u2"}`,
				`{"type":"message","id":"a2","parentId":"comp1","timestamp":"2026-01-01T00:00:05Z","message":{"role":"assistant","content":[{"type":"text","text":"after"}]}}`,
			},
			leaf: "a2",
			want: []string{"comp1", "u2", "a2"},
		},
		{
			name: "two_compactions_only_outermost_anchor_wins",
			lines: []string{
				`{"type":"session","version":3,"id":"diff-2comp","timestamp":"2026-01-01T00:00:00Z","cwd":"/tmp"}`,
				`{"type":"message","id":"u1","timestamp":"2026-01-01T00:00:01Z","message":{"role":"user","content":[{"type":"text","text":"u1"}]}}`,
				`{"type":"message","id":"u2","parentId":"u1","timestamp":"2026-01-01T00:00:02Z","message":{"role":"user","content":[{"type":"text","text":"u2"}]}}`,
				`{"type":"compaction","id":"comp1","parentId":"u2","timestamp":"2026-01-01T00:00:03Z","summary":"inner","firstKeptEntryId":"u2"}`,
				`{"type":"message","id":"u3","parentId":"comp1","timestamp":"2026-01-01T00:00:04Z","message":{"role":"user","content":[{"type":"text","text":"u3"}]}}`,
				`{"type":"compaction","id":"comp2","parentId":"u3","timestamp":"2026-01-01T00:00:05Z","summary":"outer","firstKeptEntryId":"u3"}`,
				`{"type":"message","id":"leaf","parentId":"comp2","timestamp":"2026-01-01T00:00:06Z","message":{"role":"assistant","content":[{"type":"text","text":"leaf"}]}}`,
			},
			leaf: "leaf",
			// Walk from leaf: comp2 (records stopAfter=u3), leaf, comp2 already, u3 → stop.
			// Result: [comp2] + [u3, leaf] in root→leaf order.
			// comp1 and u1/u2 never entered because we stopped at u3.
			want: []string{"comp2", "u3", "leaf"},
		},
		{
			name: "custom_filtered_custom_message_kept",
			lines: []string{
				`{"type":"session","version":3,"id":"diff-cust","timestamp":"2026-01-01T00:00:00Z","cwd":"/tmp"}`,
				`{"type":"message","id":"u1","timestamp":"2026-01-01T00:00:01Z","message":{"role":"user","content":[{"type":"text","text":"u1"}]}}`,
				`{"type":"custom","id":"state1","parentId":"u1","timestamp":"2026-01-01T00:00:02Z","customType":"plugin"}`,
				`{"type":"custom_message","id":"hint1","parentId":"state1","timestamp":"2026-01-01T00:00:03Z","customType":"hint","content":"hint"}`,
				`{"type":"message","id":"leaf","parentId":"hint1","timestamp":"2026-01-01T00:00:04Z","message":{"role":"assistant","content":[{"type":"text","text":"answer"}]}}`,
			},
			leaf: "leaf",
			want: []string{"u1", "hint1", "leaf"},
		},
		{
			name: "compaction_plus_custom_combined",
			lines: []string{
				`{"type":"session","version":3,"id":"diff-mix","timestamp":"2026-01-01T00:00:00Z","cwd":"/tmp"}`,
				`{"type":"message","id":"u1","timestamp":"2026-01-01T00:00:01Z","message":{"role":"user","content":[{"type":"text","text":"u1"}]}}`,
				`{"type":"custom","id":"st1","parentId":"u1","timestamp":"2026-01-01T00:00:02Z","customType":"plugin"}`,
				`{"type":"message","id":"u2","parentId":"st1","timestamp":"2026-01-01T00:00:03Z","message":{"role":"user","content":[{"type":"text","text":"kept"}]}}`,
				`{"type":"compaction","id":"comp1","parentId":"u2","timestamp":"2026-01-01T00:00:04Z","summary":"compacted","firstKeptEntryId":"u2"}`,
				`{"type":"custom","id":"st2","parentId":"comp1","timestamp":"2026-01-01T00:00:05Z","customType":"plugin"}`,
				`{"type":"message","id":"leaf","parentId":"st2","timestamp":"2026-01-01T00:00:06Z","message":{"role":"assistant","content":[{"type":"text","text":"after"}]}}`,
			},
			leaf: "leaf",
			// Walk: leaf (keep), st2 (custom, drop), comp1 (compaction, keep + stopAfter=u2),
			// u2 (keep + matches anchor, stop). Result: [comp1] + [u2, leaf] root→leaf.
			want: []string{"comp1", "u2", "leaf"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rs := parseReferenceSession(t, tc.lines)
			refOrder, err := referenceLiveContext(rs, tc.leaf)
			if err != nil {
				t.Fatalf("reference: %v", err)
			}
			if !reflect.DeepEqual(refOrder, tc.want) {
				t.Fatalf("reference impl disagrees with stated golden:\n  ref:  %v\n  want: %v", refOrder, tc.want)
			}
			store, key := ingestPiLines(t, rs.sessionID, tc.lines)
			defer store.Close()
			gotEntries, err := corpus.LiveContext(store.DB, key.String(), tc.leaf)
			if err != nil {
				t.Fatalf("LiveContext: %v", err)
			}
			gotOrder := make([]string, len(gotEntries))
			for i, e := range gotEntries {
				gotOrder[i] = e.EntryID
			}
			if !reflect.DeepEqual(gotOrder, tc.want) {
				t.Fatalf("corpus.LiveContext disagrees with stated golden:\n  corpus: %v\n  want:   %v", gotOrder, tc.want)
			}
		})
	}
}

func refStr(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}

func refNestedString(m map[string]any, outer, inner string) string {
	if outerVal, ok := m[outer].(map[string]any); ok {
		return refStr(outerVal, inner)
	}
	return ""
}

func refFirstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

func filepathBaseTrim(lines []string) string {
	if len(lines) == 0 {
		return "empty"
	}
	return "noheader"
}

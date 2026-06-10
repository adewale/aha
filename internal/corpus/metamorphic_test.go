package corpus_test

import (
	"database/sql"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/adewale/aha/internal/corpus"
)

// TestPullHistoryMetamorphicEquivalence pins a metamorphic relation
// (testing-best-practices research: relate outputs of related inputs
// instead of computing an oracle): a corpus that ingested every
// intermediate snapshot of a growing machine answers exactly like a
// corpus that ingested only the final snapshot. History replay and
// latest-state anti-entropy must agree on every user-visible fact —
// provenance rows (session_versions, snapshots) may differ, content
// facts may not.
func TestPullHistoryMetamorphicEquivalence(t *testing.T) {
	stateA := map[string][]byte{"alpha": []byte("alpha entry bytes")}
	stateAB := map[string][]byte{"alpha": []byte("alpha entry bytes"), "beta": []byte("beta entry bytes")}
	stateABC := map[string][]byte{"alpha": []byte("alpha entry bytes"), "beta": []byte("beta entry bytes"), "gamma": []byte("gamma entry bytes")}

	incremental, err := corpus.Open(filepath.Join(t.TempDir(), "corpus-incremental"))
	if err != nil {
		t.Fatal(err)
	}
	defer incremental.Close()
	registryA, _ := countingRegistry()
	ingA := corpus.NewIngestor(incremental, registryA)
	for _, state := range []map[string][]byte{stateA, stateAB, stateABC} {
		manifest, opener := snapshotFixture(state)
		if _, err := ingA.IngestSnapshot(manifest, opener.Open); err != nil {
			t.Fatal(err)
		}
	}

	latestOnly, err := corpus.Open(filepath.Join(t.TempDir(), "corpus-latest"))
	if err != nil {
		t.Fatal(err)
	}
	defer latestOnly.Close()
	registryB, _ := countingRegistry()
	manifest, opener := snapshotFixture(stateABC)
	if _, err := corpus.NewIngestor(latestOnly, registryB).IngestSnapshot(manifest, opener.Open); err != nil {
		t.Fatal(err)
	}

	got := contentFacts(t, incremental.DB)
	want := contentFacts(t, latestOnly.DB)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("incremental and latest-only corpora answer differently:\nincremental: %#v\nlatest-only: %#v", got, want)
	}
}

// contentFacts extracts the user-visible content of a corpus: sessions,
// entries, message texts, and FTS rows — deliberately excluding
// provenance tables (snapshots, session_versions, ingest_attempts),
// which legitimately differ between replayed and latest-only histories.
func contentFacts(t *testing.T, db *sql.DB) map[string]any {
	t.Helper()
	out := map[string]any{}
	for _, table := range []string{"sessions", "entries", "messages", "fts_messages", "artifacts", "tool_invocations", "conflicts"} {
		var n int
		if err := db.QueryRow("select count(*) from " + table).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		out[table] = n
	}
	rows, err := db.Query(`select s.source_session_id, m.text from messages m join sessions s on s.session_key=m.session_key order by s.source_session_id, m.text`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var texts []string
	for rows.Next() {
		var id, text string
		if err := rows.Scan(&id, &text); err != nil {
			t.Fatal(err)
		}
		texts = append(texts, id+"="+text)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	out["texts"] = texts
	return out
}

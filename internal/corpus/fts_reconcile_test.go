package corpus_test

import (
	"testing"

	"github.com/adewale/aha/internal/corpus"
)

func TestReconcileFTSRepairsMessageDrift(t *testing.T) {
	store, ref := corpusWithOneEntry(t)
	defer store.Close()
	if _, err := store.DB.Exec(`delete from fts_messages`); err != nil {
		t.Fatal(err)
	}
	report, err := corpus.Verify(store)
	if err != nil {
		t.Fatal(err)
	}
	if !report.HasProblem("missing_fts_messages") {
		t.Fatalf("expected missing fts problem, got %+v", report.Problems)
	}
	if err := corpus.ReconcileFTS(store); err != nil {
		t.Fatal(err)
	}
	var n int
	resolved, err := corpus.ResolveHuman(store.DB, ref.SessionKey, ref.EntryID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DB.QueryRow(`select count(*) from fts_messages where session_key=? and entry_id=?`, resolved.SessionKey, resolved.EntryID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("reconciled fts rows=%d want 1", n)
	}
}

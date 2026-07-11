package corpus_test

import (
	"context"
	"errors"
	"testing"

	"github.com/adewale/aha/internal/corpus"
)

func TestReconcileFTSHonorsCancellationWithoutDeletingIndex(t *testing.T) {
	store, _ := corpusWithOneEntry(t)
	defer store.Close()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := corpus.ReconcileFTSWithReportContext(ctx, store); !errors.Is(err, context.Canceled) {
		t.Fatalf("ReconcileFTSWithReportContext error=%v want context.Canceled", err)
	}
	var rows int
	if err := store.DB.QueryRow(`select count(*) from fts_messages`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("fts rows=%d want 1 after cancelled repair", rows)
	}
}

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
	repair, err := corpus.ReconcileFTSWithReport(store)
	if err != nil {
		t.Fatal(err)
	}
	if repair.DeletedMessageRows != 0 || repair.InsertedMessageRows != 1 {
		t.Fatalf("unexpected FTS repair counters: %+v", repair)
	}
	var n int
	if err := store.DB.QueryRow(`select count(*) from fts_messages where session_key=? and entry_id=?`, ref.Session.String(), ref.Entry.String()).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("reconciled fts rows=%d want 1", n)
	}
}

package corpus_test

import (
	"sync"
	"testing"

	"github.com/adewale/aha/internal/adapters"
	"github.com/adewale/aha/internal/corpus"
)

func TestConcurrentDuplicateIngestIsSerializedAndIdempotent(t *testing.T) {
	root := t.TempDir()
	bundlePath, corpusDir := makeBundle(t, root)
	storeA, err := corpus.Open(corpusDir)
	if err != nil {
		t.Fatal(err)
	}
	defer storeA.Close()
	storeB, err := corpus.Open(corpusDir)
	if err != nil {
		t.Fatal(err)
	}
	defer storeB.Close()
	stores := []*corpus.Store{storeA, storeB}

	var wg sync.WaitGroup
	reports := make([]corpus.IngestReport, 2)
	errs := make([]error, 2)
	for i := range reports {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			reports[i], errs[i] = corpus.IngestBundle(stores[i], adapters.Builtins(), bundlePath)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("ingest %d failed: %v", i, err)
		}
	}
	if got := queryCount(t, storeA, `select count(*) from snapshots`); got != 1 {
		t.Fatalf("bundles=%d want 1", got)
	}
	if got := queryCount(t, storeA, `select count(*) from ingest_attempts`); got != 2 {
		t.Fatalf("attempts=%d want 2", got)
	}
	duplicates := 0
	for _, rep := range reports {
		if rep.Duplicate {
			duplicates++
		}
	}
	if duplicates != 1 {
		t.Fatalf("duplicate reports=%d want 1: %+v", duplicates, reports)
	}
}

func queryCount(t *testing.T, store *corpus.Store, query string) int {
	t.Helper()
	var n int
	if err := store.DB.QueryRow(query).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

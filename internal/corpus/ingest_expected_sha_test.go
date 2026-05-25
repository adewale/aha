package corpus_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/adewale/aha/internal/adapters"
	"github.com/adewale/aha/internal/corpus"
)

func TestIngestBundleWithExpectedSHARejectsMismatchBeforePromotion(t *testing.T) {
	root := t.TempDir()
	path := writeCorpusBenchBundle(t, root, 1, 1)
	store, err := corpus.Open(filepath.Join(root, "corpus"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	_, err = corpus.IngestBundleWithExpectedSHA(store, adapters.Builtins(), path, strings.Repeat("0", 64))
	if err == nil || !strings.Contains(err.Error(), "bundle sha mismatch") {
		t.Fatalf("err=%v, want bundle sha mismatch", err)
	}
	var bundles int
	if scanErr := store.DB.QueryRow(`select count(*) from bundles`).Scan(&bundles); scanErr != nil {
		t.Fatal(scanErr)
	}
	if bundles != 0 {
		t.Fatalf("mismatched bundle was promoted: bundles=%d", bundles)
	}
}

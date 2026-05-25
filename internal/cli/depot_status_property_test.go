package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"testing"
	"testing/quick"

	"github.com/adewale/aha/internal/corpus"
	"github.com/adewale/aha/internal/depot"
)

func TestDepotBehindFromRefsCountsUniqueCatalogMinusCorpusProperty(t *testing.T) {
	prop := func(catalogInputs, duplicateInputs, ingestedInputs []string) bool {
		var refs []depot.BundleRef
		catalogSet := map[string]bool{}
		for _, input := range append(append([]string{}, catalogInputs...), duplicateInputs...) {
			sha := statusPropSHA(input)
			catalogSet[sha] = true
			refs = append(refs, depot.BundleRef{BundleSHA256: sha, Key: depot.BundleKey(sha)})
		}
		// Add duplicates deliberately: many trivial bundle refs should affect metadata cardinality, not work units.
		for _, input := range duplicateInputs {
			sha := statusPropSHA(input)
			refs = append(refs, depot.BundleRef{BundleSHA256: sha, Key: depot.BundleKey(sha)})
		}
		ingested := map[string]bool{}
		for _, input := range ingestedInputs {
			ingested[statusPropSHA(input)] = true
		}
		want := 0
		for sha := range catalogSet {
			if !ingested[sha] {
				want++
			}
		}
		return depotBehindFromRefs(refs, ingested) == want
	}
	if err := quick.Check(prop, &quick.Config{MaxCount: 100}); err != nil {
		t.Fatal(err)
	}
}

func TestDepotBehindCountFromDriverListsMetadataWithoutFetchingBundles(t *testing.T) {
	store, err := corpus.Open(filepath.Join(t.TempDir(), "corpus"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ingestedSHA := statusPropSHA("ingested")
	if _, err := store.DB.Exec(`insert into bundles(bundle_id,bundle_sha256,machine_id,captured_at,ingested_at,manifest_json) values(?,?,?,?,?,?)`, "ingested", ingestedSHA, "machine", "2026-01-01T00:00:00Z", "2026-01-01T00:00:01Z", "{}"); err != nil {
		t.Fatal(err)
	}
	pendingSHA := statusPropSHA("pending")
	drv := &countingStatusDepot{refs: []depot.BundleRef{
		{BundleSHA256: pendingSHA, Key: depot.BundleKey(pendingSHA)},
		{BundleSHA256: pendingSHA, Key: depot.BundleKey(pendingSHA)},
		{BundleSHA256: ingestedSHA, Key: depot.BundleKey(ingestedSHA)},
	}}
	behind, err := depotBehindCountFromDriver(store, drv)
	if err != nil {
		t.Fatal(err)
	}
	if behind != 1 {
		t.Fatalf("behind=%d, want 1", behind)
	}
	if drv.listCalls != 1 || drv.fetchCalls != 0 {
		t.Fatalf("status depot operations list=%d fetch=%d, want list=1 fetch=0", drv.listCalls, drv.fetchCalls)
	}
}

type countingStatusDepot struct {
	refs       []depot.BundleRef
	listCalls  int
	fetchCalls int
}

func (d *countingStatusDepot) Address() depot.Address {
	return depot.Address{Type: "fake", Location: "status"}
}
func (d *countingStatusDepot) Init(context.Context) error { return nil }
func (d *countingStatusDepot) PutBundle(context.Context, string) (depot.BundleRef, bool, error) {
	return depot.BundleRef{}, false, fmt.Errorf("unexpected PutBundle")
}
func (d *countingStatusDepot) List(context.Context) ([]depot.BundleRef, error) {
	d.listCalls++
	return append([]depot.BundleRef(nil), d.refs...), nil
}
func (d *countingStatusDepot) Fetch(context.Context, depot.BundleRef, string) error {
	d.fetchCalls++
	return fmt.Errorf("status must not fetch bundle bytes")
}
func (d *countingStatusDepot) Verify(context.Context, bool) (depot.VerifyReport, error) {
	return depot.VerifyReport{}, fmt.Errorf("unexpected Verify")
}

func statusPropSHA(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

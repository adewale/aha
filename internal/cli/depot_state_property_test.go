package cli

import (
	"context"
	"fmt"
	"testing"

	"github.com/adewale/aha/internal/archive"
	"github.com/adewale/aha/internal/depot"
	"github.com/adewale/aha/internal/model"
)

func TestFindDepotBundleWithSameStateUsesCatalogStateWithoutFetching(t *testing.T) {
	manifest := model.Manifest{Schema: model.BundleSchema, BundleID: "new", MachineID: "machine", CapturedAt: "2026-01-01T00:00:00Z", Policy: model.ManifestPolicy{Redaction: "none-v1"}}
	wantState := archive.ManifestStateSHA256(manifest)
	sha := statusPropSHA("state-match")
	drv := &countingStateDepot{refs: []depot.BundleRef{
		{BundleSHA256: statusPropSHA("other"), Key: depot.BundleKey(statusPropSHA("other")), MachineID: "other", StateSHA256: wantState},
		{BundleSHA256: sha, Key: depot.BundleKey(sha), MachineID: "machine", StateSHA256: wantState},
	}}
	ref, ok, err := findDepotBundleWithSameState(context.Background(), drv, manifest, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !ok || ref.BundleSHA256 != sha {
		t.Fatalf("state match ref=%+v ok=%v", ref, ok)
	}
	if drv.listCalls != 1 || drv.fetchCalls != 0 {
		t.Fatalf("state lookup operations list=%d fetch=%d, want list=1 fetch=0", drv.listCalls, drv.fetchCalls)
	}
}

type countingStateDepot struct {
	refs       []depot.BundleRef
	listCalls  int
	fetchCalls int
}

func (d *countingStateDepot) Address() depot.Address {
	return depot.Address{Type: "fake", Location: "state"}
}
func (d *countingStateDepot) Init(context.Context) error { return nil }
func (d *countingStateDepot) PutBundle(context.Context, string) (depot.BundleRef, bool, error) {
	return depot.BundleRef{}, false, fmt.Errorf("unexpected PutBundle")
}
func (d *countingStateDepot) List(context.Context) ([]depot.BundleRef, error) {
	d.listCalls++
	return append([]depot.BundleRef(nil), d.refs...), nil
}
func (d *countingStateDepot) Fetch(context.Context, depot.BundleRef, string) error {
	d.fetchCalls++
	return fmt.Errorf("state lookup must not fetch bundle bytes when state_sha256 is present")
}
func (d *countingStateDepot) Verify(context.Context, bool) (depot.VerifyReport, error) {
	return depot.VerifyReport{}, fmt.Errorf("unexpected Verify")
}

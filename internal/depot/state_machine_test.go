package depot_test

import (
	"context"
	"path/filepath"
	"sort"
	"testing"

	"github.com/adewale/aha/internal/depot"
	"pgregory.net/rapid"
)

func TestLocalDepotStateMachineSkeleton(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		root := t.TempDir()
		d, err := depot.NewLocal(filepath.Join(root, "depot"))
		if err != nil {
			rt.Fatal(err)
		}
		if err := d.Init(rt.Context()); err != nil {
			rt.Fatal(err)
		}
		bundleA := writeDepotTestBundleWithID(t, filepath.Join(root, "a"), "depot-sm-a")
		bundleB := writeDepotTestBundleWithID(t, filepath.Join(root, "b"), "depot-sm-b")
		known := map[string]bool{}
		ops := rapid.SliceOfN(rapid.SampledFrom([]string{"putA", "putB", "dupA", "list", "verify", "repair"}), 1, 12).Draw(rt, "ops")
		for _, op := range ops {
			switch op {
			case "putA":
				ref, _, err := d.PutBundle(rt.Context(), bundleA)
				if err != nil {
					rt.Fatal(err)
				}
				known[ref.BundleSHA256] = true
			case "putB":
				ref, _, err := d.PutBundle(rt.Context(), bundleB)
				if err != nil {
					rt.Fatal(err)
				}
				known[ref.BundleSHA256] = true
			case "dupA":
				ref, created, err := d.PutBundle(rt.Context(), bundleA)
				if err != nil {
					rt.Fatal(err)
				}
				if known[ref.BundleSHA256] && created {
					rt.Fatalf("duplicate put marked created for %s", ref.BundleSHA256)
				}
				known[ref.BundleSHA256] = true
			case "list":
				assertDepotListMatchesModel(rt.Context(), rt, d, known)
			case "verify":
				report, err := d.Verify(rt.Context(), false)
				if err != nil {
					rt.Fatal(err)
				}
				if len(report.Problems) != 0 {
					rt.Fatalf("verify problems after valid operations: %+v", report.Problems)
				}
			case "repair":
				report, err := d.Verify(rt.Context(), true)
				if err != nil {
					rt.Fatal(err)
				}
				if len(report.Problems) != 0 {
					rt.Fatalf("repair problems after valid operations: %+v", report.Problems)
				}
			}
			assertDepotListMatchesModel(rt.Context(), rt, d, known)
		}
	})
}

func assertDepotListMatchesModel(ctx context.Context, t interface {
	Fatalf(string, ...any)
	Fatal(...any)
}, d *depot.Local, known map[string]bool) {
	refs, err := d.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, ref := range refs {
		got = append(got, ref.BundleSHA256)
	}
	sort.Strings(got)
	var want []string
	for sha := range known {
		want = append(want, sha)
	}
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("depot list got %v want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("depot list got %v want %v", got, want)
		}
	}
}

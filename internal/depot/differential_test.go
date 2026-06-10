package depot_test

import (
	"context"
	"io"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/adewale/aha/internal/depot"
	"github.com/adewale/aha/internal/model"
)

// TestLocalAndR2DriversAreObservationallyEquivalent is the differential
// test from the testing-best-practices research (and the v2 successor of
// the deleted v1 driver contract test): the same scenario applied to the
// local driver and the (fake) R2 driver must produce identical observable
// state through every read surface — machine list, pointers, manifests,
// blob contents, and verify reports. Divergence means one driver is wrong
// even if both suites pass in isolation.
func TestLocalAndR2DriversAreObservationallyEquivalent(t *testing.T) {
	ctx := context.Background()
	local, err := depot.NewLocalV2(filepath.Join(t.TempDir(), "depot"))
	if err != nil {
		t.Fatal(err)
	}
	f := newFakeS3(t)
	defer f.Close()
	remote := depot.NewV2FromR2(f.Depot("bucket"))

	scenario := func(v2 *depot.V2) {
		t.Helper()
		if err := v2.Init(ctx); err != nil {
			t.Fatal(err)
		}
		pushState(t, ctx, v2, "diff-a", map[string]string{"a.jsonl": "alpha content"})
		pushState(t, ctx, v2, "diff-a", map[string]string{"a.jsonl": "alpha content", "b.jsonl": "beta content"})
		pushState(t, ctx, v2, "diff-b", map[string]string{"c.jsonl": "gamma content"})
		// Unchanged re-push: must be recognized on both drivers alike.
		pushState(t, ctx, v2, "diff-b", map[string]string{"c.jsonl": "gamma content"})
	}
	scenario(local)
	scenario(remote)

	if got, want := observe(t, ctx, local), observe(t, ctx, remote); !reflect.DeepEqual(got, want) {
		t.Fatalf("drivers diverged:\nlocal:  %#v\nremote: %#v", got, want)
	}
}

// observe extracts everything a reader can see from a depot.
func observe(t *testing.T, ctx context.Context, v2 *depot.V2) map[string]any {
	t.Helper()
	out := map[string]any{}
	machines, err := v2.Machines(ctx)
	if err != nil {
		t.Fatal(err)
	}
	out["machines"] = machines
	for _, machine := range machines {
		sha, ok, err := v2.Latest(ctx, machine)
		if err != nil {
			t.Fatal(err)
		}
		out["latest/"+machine] = map[string]any{"ok": ok, "sha": sha.String()}
		if !ok {
			continue
		}
		manifest, err := v2.Manifest(ctx, machine, sha)
		if err != nil {
			t.Fatal(err)
		}
		out["manifest/"+machine] = manifest
		for _, mf := range manifest.Files {
			key, err := model.NewBlobKey(mf.SHA256)
			if err != nil {
				t.Fatal(err)
			}
			rc, err := v2.OpenBlob(ctx, key)
			if err != nil {
				t.Fatal(err)
			}
			b, err := io.ReadAll(rc)
			if cerr := rc.Close(); cerr != nil {
				t.Fatal(cerr)
			}
			if err != nil {
				t.Fatal(err)
			}
			out["blob/"+mf.SHA256] = string(b)
		}
	}
	var problems []string
	counts := map[string]int{}
	for _, deep := range []bool{false, true} {
		report, err := v2.Verify(ctx, deep)
		if err != nil {
			t.Fatal(err)
		}
		problems = append(problems, report.Problems...)
		counts[map[bool]string{false: "quick", true: "deep"}[deep]] = report.Manifests*1000 + report.Machines
	}
	out["verify-problems"] = problems
	out["verify-counts"] = counts
	return out
}

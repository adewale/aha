package depot_test

import (
	"strings"
	"testing"

	"github.com/adewale/aha/internal/depot"
	"github.com/adewale/aha/internal/model"
)

// TestMachineKeySafetyExhaustive exhaustively proves (within bounds) the
// namespace-escape property over the whole domain of short hostile
// machine IDs: every string of length 0-2 over an adversarial alphabet
// must produce keys that stay inside machines/ with no traversal, no
// separators, and no scheme-confusable characters. Exhaustive testing
// from the testing-best-practices research: when the domain is small,
// prove it rather than sample it — sanitization happens rune by rune, so
// the length-2 result composes to all longer inputs.
func TestMachineKeySafetyExhaustive(t *testing.T) {
	alphabet := []rune{'a', 'Z', '0', '.', '-', '_', '/', '\\', ':', '~', '%', '*', '?', '"', '<', '>', '|', ' ', '\n', 0, '‮', '日'}
	sha, err := model.NewManifestSHA256(strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	var inputs []string
	inputs = append(inputs, "")
	for _, a := range alphabet {
		inputs = append(inputs, string(a))
		for _, b := range alphabet {
			inputs = append(inputs, string(a)+string(b))
		}
	}
	for _, machine := range inputs {
		for _, key := range []string{depot.ManifestObjectKey(machine, sha), depot.LatestPointerKey(machine)} {
			if !strings.HasPrefix(key, "machines/") {
				t.Fatalf("machine %q escaped the namespace: %q", machine, key)
			}
			component := strings.TrimPrefix(key, "machines/")
			component = component[:strings.IndexByte(component, '/')]
			if component == "" || component == "." || component == ".." {
				t.Fatalf("machine %q produced traversal component %q", machine, component)
			}
			for _, r := range component {
				ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-'
				if !ok {
					t.Fatalf("machine %q leaked rune %q into key %q", machine, r, key)
				}
			}
			if strings.Contains(key, "..") || strings.Contains(key, "//") || strings.Contains(key, "\\") {
				t.Fatalf("machine %q produced unsafe key %q", machine, key)
			}
		}
	}
}

// TestMachineIDSanitizationCollisionResidual documents the accepted
// residual of rune-level sanitization: distinct raw machine IDs can map
// to the same namespace component ("Work Mac" and "work-mac"). The
// manifest's MachineID field stays authoritative inside the namespace,
// and Manifest() accepts any ID that sanitizes to the requested
// namespace — this test pins that the collision is shared state, not
// corruption: the second machine's push simply becomes the namespace's
// latest, and the manifest read back is identity-verified.
func TestMachineIDSanitizationCollisionResidual(t *testing.T) {
	ctx := t.Context()
	v2 := newLocalV2(t)
	pushState(t, ctx, v2, "Work Mac", map[string]string{"a.jsonl": "from Work Mac"})
	shaB := pushState(t, ctx, v2, "work-mac", map[string]string{"b.jsonl": "from work-mac"})
	latest, ok, err := v2.Latest(ctx, "Work Mac")
	if err != nil || !ok {
		t.Fatalf("Latest ok=%v err=%v", ok, err)
	}
	if latest != shaB {
		t.Fatalf("colliding namespaces must share one pointer: %s want %s", latest, shaB)
	}
	manifest, err := v2.Manifest(ctx, "Work Mac", latest)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.MachineID != "work-mac" {
		t.Fatalf("manifest must keep the raw machine id: %q", manifest.MachineID)
	}
	report, err := v2.Verify(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Problems) != 0 {
		t.Fatalf("collision left verify problems: %v", report.Problems)
	}
}

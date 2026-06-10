package testquality_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDepotV2NeverDeletes enforces invariant I5 of docs/depot-v2-spec.md:
// the depot v2 layout is append-only forever — no GC, no retention, no
// compaction deletes. The objectStore interface already omits a delete
// primitive (construction-time guarantee); this guard pins the residual
// risk that a v2 file reaches for the SDK or filesystem directly.
//
// The v1 driver's repair path is intentionally exempt: it predates v2 and
// is deleted with the rest of v1 in spec phase 7.
func TestDepotV2NeverDeletes(t *testing.T) {
	v2Files := []string{
		"internal/depot/keys_v2.go",
		"internal/depot/objectstore_v2.go",
		"internal/depot/depot_v2.go",
	}
	for _, rel := range v2Files {
		b, err := os.ReadFile(filepath.Join("..", "..", rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		src := string(b)
		if strings.Contains(src, "DeleteObject") {
			t.Fatalf("%s calls DeleteObject; depot v2 never deletes (I5)", rel)
		}
		for _, line := range strings.Split(src, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.Contains(trimmed, "os.Remove") && !strings.Contains(trimmed, "tmpDir") {
				t.Fatalf("%s removes files outside temp staging: %q (I5)", rel, trimmed)
			}
		}
	}
}

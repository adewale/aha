package depot_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func BenchmarkPathologicalLocalDepotLargeCatalog(b *testing.B) {
	refs := depotPathologicalScale("AHA_PATHOLOGICAL_DEPOT_REFS", 250)
	b.Run("list", func(b *testing.B) {
		d := seededLocalDepot(b, refs)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, err := d.List(context.Background()); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("append-growing-catalog", func(b *testing.B) {
		d := seededLocalDepot(b, refs)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			b.StopTimer()
			bundlePath := writeDepotBenchBundle(b, filepath.Join(b.TempDir(), fmt.Sprintf("new-bundle-%d", i)), fmt.Sprintf("depot-pathological-new-%d", i), 1024)
			b.StartTimer()
			if _, _, err := d.PutBundle(context.Background(), bundlePath); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("verify", func(b *testing.B) {
		d := seededLocalDepot(b, refs)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, err := d.Verify(context.Background(), false); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func depotPathologicalScale(env string, fallback int) int {
	if value := os.Getenv(env); value != "" {
		if n, err := strconv.Atoi(value); err == nil && n > 0 {
			return n
		}
	}
	return fallback
}

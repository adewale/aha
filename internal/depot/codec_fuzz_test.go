package depot_test

import (
	"strings"
	"testing"

	"github.com/adewale/aha/internal/depot"
	"github.com/adewale/aha/internal/model"
)

// FuzzDecodeLatestPointer pins the pointer codec: arbitrary bytes never
// panic, and anything that decodes re-encodes to an equivalent pointer.
func FuzzDecodeLatestPointer(f *testing.F) {
	sha, err := model.NewManifestSHA256(strings.Repeat("a", 64))
	if err != nil {
		f.Fatal(err)
	}
	good, err := depot.EncodeLatestPointer(sha)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(good)
	f.Add([]byte(`{"schema":"aha-depot-latest/v2"}`))
	f.Add([]byte(`{}`))
	f.Add([]byte(``))
	f.Fuzz(func(t *testing.T, data []byte) {
		got, err := depot.DecodeLatestPointer(data)
		if err != nil {
			return
		}
		if !got.Valid() {
			t.Fatalf("decode accepted an invalid manifest sha: %q", got)
		}
		reencoded, err := depot.EncodeLatestPointer(got)
		if err != nil {
			t.Fatalf("decoded pointer does not re-encode: %v", err)
		}
		roundTrip, err := depot.DecodeLatestPointer(reencoded)
		if err != nil || roundTrip != got {
			t.Fatalf("pointer round trip unstable: %v %v", roundTrip, err)
		}
	})
}

// FuzzDecodeMachinesIndex pins the index codec: arbitrary bytes never
// panic; anything that decodes is sorted, deduplicated, free of empty
// IDs, and stable under re-encoding.
func FuzzDecodeMachinesIndex(f *testing.F) {
	good, err := depot.EncodeMachinesIndex([]string{"alpha", "beta"})
	if err != nil {
		f.Fatal(err)
	}
	f.Add(good)
	f.Add([]byte(`{"schema":"aha-depot-machines/v2","machines":[]}`))
	f.Add([]byte(`{"schema":"aha-depot-machines/v2","machines":["b","a","b"]}`))
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		machines, err := depot.DecodeMachinesIndex(data)
		if err != nil {
			return
		}
		for i, m := range machines {
			if strings.TrimSpace(m) == "" {
				t.Fatal("decode accepted an empty machine id")
			}
			if i > 0 && machines[i-1] >= m {
				t.Fatalf("decode output not sorted+deduped: %v", machines)
			}
		}
		reencoded, err := depot.EncodeMachinesIndex(machines)
		if err != nil {
			t.Fatalf("decoded index does not re-encode: %v", err)
		}
		roundTrip, err := depot.DecodeMachinesIndex(reencoded)
		if err != nil {
			t.Fatalf("index round trip failed: %v", err)
		}
		if len(roundTrip) != len(machines) {
			t.Fatalf("index round trip unstable: %v vs %v", roundTrip, machines)
		}
	})
}

package model_test

import (
	"testing"

	"github.com/adewale/aha/internal/model"
	"github.com/adewale/aha/internal/testutil"
	"pgregory.net/rapid"
)

func TestHitRefRapidRoundTrip(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		ref := testutil.GenHitRef().Draw(t, "ref")
		text := model.FormatHitRef(ref)
		parsed, err := model.ParseHitRef(text)
		if err != nil {
			t.Fatalf("ParseHitRef(FormatHitRef(%+v)=%q): %v", ref, text, err)
		}
		if parsed != ref {
			t.Fatalf("round trip mismatch: got=%+v want=%+v text=%q", parsed, ref, text)
		}
	})
}

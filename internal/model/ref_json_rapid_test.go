package model_test

import (
	"testing"

	"github.com/adewale/aha/internal/model"
	"github.com/adewale/aha/internal/testutil"
	"pgregory.net/rapid"
)

func TestCanonicalRefRapidJSONRoundTrip(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		ref := testutil.GenRef().Draw(t, "ref")
		text := model.FormatRef(ref)
		parsed, err := model.ParseRef(text)
		if err != nil {
			t.Fatalf("ParseRef(FormatRef(%+v)=%q): %v", ref, text, err)
		}
		if model.FormatRef(parsed) != text {
			t.Fatalf("round trip mismatch got=%q want=%q", model.FormatRef(parsed), text)
		}
	})
}

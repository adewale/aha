package model_test

import (
	"testing"

	"github.com/adewale/aha/internal/model"
	"github.com/adewale/aha/internal/testutil"
	"pgregory.net/rapid"
)

func TestCanonicalRefRapidRoundTrip(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		hit := testutil.GenHitRef().Draw(t, "hit")
		ref, err := model.HitRefToRef(hit)
		if err != nil {
			t.Skip("legacy fixture uses non-sha artifact")
		}
		text := model.FormatRef(ref)
		parsed, err := model.ParseRef(text)
		if err != nil {
			t.Fatalf("ParseRef(%q): %v", text, err)
		}
		if model.FormatRef(parsed) != text {
			t.Fatalf("unstable canonical ref: %q -> %q", text, model.FormatRef(parsed))
		}
	})
}

package model_test

import (
	"strings"
	"testing"

	"github.com/adewale/aha/internal/model"
)

func TestCanonicalArtifactRefRoundTrip(t *testing.T) {
	sha := strings.Repeat("a", 64)
	parsedSHA, err := model.ParseSHA256Hex(sha)
	if err != nil {
		t.Fatal(err)
	}
	ref := model.ArtifactRef{SHA: parsedSHA}
	parsed, err := model.ParseRef(model.FormatRef(ref))
	if err != nil {
		t.Fatal(err)
	}
	artifact, ok := parsed.(model.ArtifactRef)
	if !ok || artifact.SHA.String() != sha {
		t.Fatalf("parsed artifact as %+v", parsed)
	}
}

func TestInvalidRefsDoNotFormat(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("FormatRef accepted invalid ref")
		}
	}()
	_ = model.FormatRef(model.MessageRef{})
}

func TestLegacyRefsAreRejected(t *testing.T) {
	for _, legacy := range []string{"pi:m:s#e", "artifact:" + strings.Repeat("a", 64)} {
		if _, err := model.ParseRef(legacy); err == nil {
			t.Fatalf("legacy ref accepted: %q", legacy)
		}
	}
}

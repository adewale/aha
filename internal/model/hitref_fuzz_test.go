package model_test

import (
	"strings"
	"testing"

	"github.com/adewale/aha/internal/model"
)

func TestHitRefParseFormatRoundTrip(t *testing.T) {
	refs := []model.HitRef{
		{Kind: model.HitKindMessage, SessionKey: "pi:m:abc", EntryID: "entry-1"},
		{Kind: model.HitKindMessage, SessionKey: "codex:m:sess", EntryID: ""},
		{Kind: model.HitKindArtifact, SessionKey: model.ArtifactSessionKey("abc123"), ArtifactSHA: "abc123", EntryID: "abc123"},
	}
	for _, ref := range refs {
		parsed, err := model.ParseHitRef(model.FormatHitRef(ref))
		if err != nil {
			t.Fatalf("ParseHitRef(%q): %v", model.FormatHitRef(ref), err)
		}
		if parsed.SessionKey != ref.SessionKey || parsed.EntryID != ref.EntryID || parsed.Kind != ref.Kind || parsed.ArtifactSHA != ref.ArtifactSHA {
			t.Fatalf("round trip mismatch got=%+v want=%+v", parsed, ref)
		}
	}
}

func FuzzHitRefParseFormat(f *testing.F) {
	for _, seed := range []string{"pi:m:abc#entry-1", "artifact:abc123#abc123", "codex:m:session", "", "#", "a#b#c"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if strings.ContainsAny(s, "\x00\n\r\t") {
			return
		}
		ref, err := model.ParseHitRef(s)
		if err != nil {
			return
		}
		formatted := model.FormatHitRef(ref)
		ref2, err := model.ParseHitRef(formatted)
		if err != nil {
			t.Fatalf("formatted ref did not parse: %q: %v", formatted, err)
		}
		if formatted != model.FormatHitRef(ref2) {
			t.Fatalf("format not stable: %q -> %+v -> %q", s, ref2, model.FormatHitRef(ref2))
		}
	})
}

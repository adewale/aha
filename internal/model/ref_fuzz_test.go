package model_test

import (
	"strings"
	"testing"

	"github.com/adewale/aha/internal/model"
)

func TestRefParseFormatRoundTrip(t *testing.T) {
	session, _ := model.NewSessionKey("pi", "m", "abc")
	entry, _ := model.NewEntryID("entry-1")
	sha, _ := model.ParseSHA256Hex(strings.Repeat("b", 64))
	refs := []model.Ref{
		model.MessageRef{Session: session, Entry: entry},
		model.SessionRef{Session: session},
		model.ArtifactRef{SHA: sha},
	}
	for _, ref := range refs {
		parsed, err := model.ParseRef(model.FormatRef(ref))
		if err != nil {
			t.Fatalf("ParseRef(%q): %v", model.FormatRef(ref), err)
		}
		if model.FormatRef(parsed) != model.FormatRef(ref) {
			t.Fatalf("round trip mismatch got=%q want=%q", model.FormatRef(parsed), model.FormatRef(ref))
		}
	}
}

func FuzzRefParseFormat(f *testing.F) {
	validSession := "c2sxX2FhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWE"
	f.Add("msg:v1:" + validSession + ":ZW50cnktMQ")
	f.Add("artifact:v1:" + strings.Repeat("a", 64))
	f.Add("session:v1:" + validSession)
	f.Add("")
	f.Add("pi:m:abc#entry-1")
	f.Fuzz(func(t *testing.T, s string) {
		if strings.ContainsAny(s, "\x00\n\r\t") {
			return
		}
		ref, err := model.ParseRef(s)
		if err != nil {
			return
		}
		formatted := model.FormatRef(ref)
		ref2, err := model.ParseRef(formatted)
		if err != nil {
			t.Fatalf("formatted ref did not parse: %q: %v", formatted, err)
		}
		if formatted != model.FormatRef(ref2) {
			t.Fatalf("format not stable: %q -> %q", s, model.FormatRef(ref2))
		}
	})
}

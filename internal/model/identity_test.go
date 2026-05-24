package model_test

import (
	"strings"
	"testing"

	"github.com/adewale/aha/internal/model"
)

func TestSessionKeyConstructors(t *testing.T) {
	legacy, err := model.NewLegacySessionKey("pi", "m:colon", "s#hash")
	if err != nil {
		t.Fatal(err)
	}
	if legacy.String() != "pi:m:colon:s#hash" {
		t.Fatalf("legacy key=%q", legacy.String())
	}
	v2a, err := model.NewSessionKey("pi", "m:colon", "s#hash")
	if err != nil {
		t.Fatal(err)
	}
	v2b, err := model.NewSessionKey("pi", "m:colon", "s#hash")
	if err != nil {
		t.Fatal(err)
	}
	if v2a != v2b || !strings.HasPrefix(v2a.String(), "sk1_") || len(v2a.String()) != len("sk1_")+64 {
		t.Fatalf("bad v2 keys: %q %q", v2a.String(), v2b.String())
	}
	if v2a.String() == legacy.String() {
		t.Fatal("v2 key equals legacy delimiter key")
	}
}

func TestConstructorsRejectEmptyIdentityParts(t *testing.T) {
	if _, err := model.NewEntryID(""); err == nil {
		t.Fatal("empty entry id accepted")
	}
	if _, err := model.NewLegacySessionKey("pi", "", "s"); err == nil {
		t.Fatal("empty machine id accepted")
	}
	if _, err := model.ParseSHA256Hex(strings.Repeat("A", 64)); err == nil {
		t.Fatal("uppercase sha accepted")
	}
}

func TestRefCodecCanonicalAndLegacy(t *testing.T) {
	session, _ := model.ParseSessionKey("pi:m:s")
	entry, _ := model.NewEntryID("e#1")
	ref := model.MessageRef{Session: session, Entry: entry}
	text := model.FormatRef(ref)
	parsed, err := model.ParseRef(text)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.(model.MessageRef) != ref {
		t.Fatalf("parsed=%+v want %+v", parsed, ref)
	}
	legacy, err := model.ParseRef("pi:m:s#e1")
	if err != nil {
		t.Fatal(err)
	}
	if legacy.AsHitRef().SessionKey != "pi:m:s" || legacy.AsHitRef().EntryID != "e1" {
		t.Fatalf("bad legacy parse: %+v", legacy.AsHitRef())
	}
}

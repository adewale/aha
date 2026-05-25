package model_test

import (
	"strings"
	"testing"

	"github.com/adewale/aha/internal/model"
)

func TestSessionKeyConstructorIsV2Only(t *testing.T) {
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
	if strings.Contains(v2a.String(), ":") || strings.Contains(v2a.String(), "#") {
		t.Fatalf("v2 key contains legacy delimiters: %q", v2a.String())
	}
}

func TestConstructorsRejectEmptyIdentityParts(t *testing.T) {
	if _, err := model.NewEntryID(""); err == nil {
		t.Fatal("empty entry id accepted")
	}
	if _, err := model.NewSessionKey("pi", "", "s"); err == nil {
		t.Fatal("empty machine id accepted")
	}
	if _, err := model.ParseSHA256Hex(strings.Repeat("A", 64)); err == nil {
		t.Fatal("uppercase sha accepted")
	}
}

func TestRefCodecCanonicalOnly(t *testing.T) {
	session, _ := model.ParseSessionKey("sk1_" + strings.Repeat("a", 64))
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
	if _, err := model.ParseRef("pi:m:s#e1"); err == nil {
		t.Fatal("legacy ref accepted")
	}
}

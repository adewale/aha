package model_test

import (
	"testing"

	"github.com/adewale/aha/internal/model"
)

func TestLinkedArtifactHitRefTextParsesAsArtifact(t *testing.T) {
	ref := model.HitRef{Kind: model.HitKindArtifact, SessionKey: "pi:m:s", EntryID: "abc123", ArtifactSHA: "abc123"}
	parsed, err := model.ParseHitRef(model.FormatHitRef(ref))
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Kind != model.HitKindArtifact || parsed.ArtifactSHA != "abc123" {
		t.Fatalf("parsed linked artifact ref as %+v", parsed)
	}
}

func TestArtifactSessionKeyRoundTrip(t *testing.T) {
	key := model.ArtifactSessionKey("abc123")
	sha, ok := model.ParseArtifactSessionKey(key)
	if !ok || sha != "abc123" {
		t.Fatalf("ParseArtifactSessionKey(%q)=(%q,%v), want abc123,true", key, sha, ok)
	}
	if _, ok := model.ParseArtifactSessionKey("pi:m:s"); ok {
		t.Fatalf("ordinary session parsed as artifact")
	}
}

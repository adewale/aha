package model_test

import (
	"testing"

	"github.com/adewale/aha/internal/model"
)

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

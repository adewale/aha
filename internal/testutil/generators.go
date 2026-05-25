package testutil

import (
	"github.com/adewale/aha/internal/model"
	"pgregory.net/rapid"
)

func GenRefToken() *rapid.Generator[string] {
	return rapid.StringMatching(`[A-Za-z0-9][A-Za-z0-9._:/=-]{0,31}`)
}

func GenSHA256Hex() *rapid.Generator[string] {
	return rapid.StringMatching(`[a-f0-9]{64}`)
}

func GenRef() *rapid.Generator[model.Ref] {
	return rapid.Custom(func(t *rapid.T) model.Ref {
		kind := rapid.SampledFrom([]model.RefKind{model.RefKindMessage, model.RefKindSession, model.RefKindArtifact}).Draw(t, "kind")
		switch kind {
		case model.RefKindArtifact:
			shaText := GenSHA256Hex().Draw(t, "artifact_sha")
			sha, err := model.ParseSHA256Hex(shaText)
			if err != nil {
				t.Fatal(err)
			}
			return model.ArtifactRef{SHA: sha}
		default:
			source := rapid.SampledFrom([]string{"pi", "claude-code", "codex"}).Draw(t, "source")
			sessionKey, err := model.NewSessionKey(source, GenRefToken().Draw(t, "machine"), GenRefToken().Draw(t, "source_session"))
			if err != nil {
				t.Fatal(err)
			}
			if kind == model.RefKindSession {
				return model.SessionRef{Session: sessionKey}
			}
			entry, err := model.NewEntryID(GenRefToken().Draw(t, "entry"))
			if err != nil {
				t.Fatal(err)
			}
			return model.MessageRef{Session: sessionKey, Entry: entry}
		}
	})
}

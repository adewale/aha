package testutil

import (
	"strings"

	"github.com/adewale/aha/internal/model"
	"pgregory.net/rapid"
)

func GenRefToken() *rapid.Generator[string] {
	return rapid.StringMatching(`[A-Za-z0-9][A-Za-z0-9._:/=-]{0,31}`)
}

func GenSHA256Hex() *rapid.Generator[string] {
	return rapid.StringMatching(`[a-f0-9]{64}`)
}

func GenHitRef() *rapid.Generator[model.HitRef] {
	return rapid.Custom(func(t *rapid.T) model.HitRef {
		kind := rapid.SampledFrom([]model.HitKind{model.HitKindMessage, model.HitKindArtifact}).Draw(t, "kind")
		switch kind {
		case model.HitKindArtifact:
			sha := GenSHA256Hex().Draw(t, "artifact_sha")
			return model.HitRef{Kind: model.HitKindArtifact, SessionKey: model.ArtifactSessionKey(sha), EntryID: sha, ArtifactSHA: sha}
		default:
			source := rapid.SampledFrom([]string{"pi", "claude-code", "codex"}).Draw(t, "source")
			session := strings.Join([]string{source, GenRefToken().Draw(t, "machine"), GenRefToken().Draw(t, "source_session")}, ":")
			withEntry := rapid.Bool().Draw(t, "with_entry")
			if !withEntry {
				return model.HitRef{Kind: model.HitKindMessage, SessionKey: session}
			}
			return model.HitRef{Kind: model.HitKindMessage, SessionKey: session, EntryID: GenRefToken().Draw(t, "entry")}
		}
	})
}

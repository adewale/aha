package model

import (
	"encoding/base64"
	"fmt"
	"strings"
)

type Ref interface {
	refVariant()
	AsHitRef() HitRef
}

type MessageRef struct {
	Session SessionKey
	Entry   EntryID
}

type SessionRef struct{ Session SessionKey }

type ArtifactRef struct{ SHA SHA256Hex }

func (MessageRef) refVariant()  {}
func (SessionRef) refVariant()  {}
func (ArtifactRef) refVariant() {}

func (r MessageRef) AsHitRef() HitRef {
	return HitRef{Kind: HitKindMessage, SessionKey: r.Session.String(), EntryID: r.Entry.String()}
}
func (r SessionRef) AsHitRef() HitRef {
	return HitRef{Kind: HitKindMessage, SessionKey: r.Session.String()}
}
func (r ArtifactRef) AsHitRef() HitRef {
	sha := r.SHA.String()
	return HitRef{Kind: HitKindArtifact, SessionKey: ArtifactSessionKey(sha), EntryID: sha, ArtifactSHA: sha}
}

func HitRefToRef(hit HitRef) (Ref, error) {
	if hit.Kind == HitKindArtifact {
		sha := firstNonEmpty(hit.ArtifactSHA, hit.EntryID)
		parsed, err := ParseSHA256Hex(sha)
		if err != nil {
			return nil, err
		}
		return ArtifactRef{SHA: parsed}, nil
	}
	session, err := ParseSessionKey(hit.SessionKey)
	if err != nil {
		return nil, err
	}
	if hit.EntryID == "" {
		return SessionRef{Session: session}, nil
	}
	entry, err := NewEntryID(hit.EntryID)
	if err != nil {
		return nil, err
	}
	return MessageRef{Session: session, Entry: entry}, nil
}

func FormatRef(ref Ref) string {
	switch r := ref.(type) {
	case MessageRef:
		return "msg:v1:" + b64(r.Session.String()) + ":" + b64(r.Entry.String())
	case SessionRef:
		return "session:v1:" + b64(r.Session.String())
	case ArtifactRef:
		return "artifact:v1:" + r.SHA.String()
	default:
		return FormatHitRef(ref.AsHitRef())
	}
}

func ParseRef(s string) (Ref, error) {
	if strings.HasPrefix(s, "msg:v1:") {
		parts := strings.Split(strings.TrimPrefix(s, "msg:v1:"), ":")
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid message ref")
		}
		sessionText, err := unb64(parts[0])
		if err != nil {
			return nil, err
		}
		entryText, err := unb64(parts[1])
		if err != nil {
			return nil, err
		}
		session, err := ParseSessionKey(sessionText)
		if err != nil {
			return nil, err
		}
		entry, err := NewEntryID(entryText)
		if err != nil {
			return nil, err
		}
		return MessageRef{Session: session, Entry: entry}, nil
	}
	if strings.HasPrefix(s, "session:v1:") {
		sessionText, err := unb64(strings.TrimPrefix(s, "session:v1:"))
		if err != nil {
			return nil, err
		}
		session, err := ParseSessionKey(sessionText)
		if err != nil {
			return nil, err
		}
		return SessionRef{Session: session}, nil
	}
	if strings.HasPrefix(s, "artifact:v1:") {
		sha, err := ParseSHA256Hex(strings.TrimPrefix(s, "artifact:v1:"))
		if err != nil {
			return nil, err
		}
		return ArtifactRef{SHA: sha}, nil
	}
	legacy, err := ParseHitRef(s)
	if err != nil {
		return nil, err
	}
	return HitRefToRef(legacy)
}

func b64(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }

func unb64(s string) (string, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return "", fmt.Errorf("invalid ref encoding: %w", err)
	}
	return string(b), nil
}

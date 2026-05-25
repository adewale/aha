package model

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

type UnsupportedRefError struct {
	Text string
}

func (e UnsupportedRefError) Error() string { return "unsupported ref format" }

type RefKind string

const (
	RefKindMessage  RefKind = "message"
	RefKindSession  RefKind = "session"
	RefKindArtifact RefKind = "artifact"
)

type Ref interface {
	refVariant()
	Kind() RefKind
	Valid() bool
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

func (MessageRef) Kind() RefKind  { return RefKindMessage }
func (SessionRef) Kind() RefKind  { return RefKindSession }
func (ArtifactRef) Kind() RefKind { return RefKindArtifact }

func (r MessageRef) Valid() bool  { return r.Session.Valid() && r.Entry.Valid() }
func (r SessionRef) Valid() bool  { return r.Session.Valid() }
func (r ArtifactRef) Valid() bool { return r.SHA.Valid() }

func (r MessageRef) MarshalJSON() ([]byte, error) {
	if !r.Valid() {
		return nil, fmt.Errorf("invalid message ref")
	}
	return json.Marshal(struct {
		Kind       RefKind `json:"kind"`
		SessionKey string  `json:"session_key"`
		EntryID    string  `json:"entry_id"`
	}{Kind: r.Kind(), SessionKey: r.Session.String(), EntryID: r.Entry.String()})
}

func (r SessionRef) MarshalJSON() ([]byte, error) {
	if !r.Valid() {
		return nil, fmt.Errorf("invalid session ref")
	}
	return json.Marshal(struct {
		Kind       RefKind `json:"kind"`
		SessionKey string  `json:"session_key"`
	}{Kind: r.Kind(), SessionKey: r.Session.String()})
}

func (r ArtifactRef) MarshalJSON() ([]byte, error) {
	if !r.Valid() {
		return nil, fmt.Errorf("invalid artifact ref")
	}
	return json.Marshal(struct {
		Kind        RefKind `json:"kind"`
		ArtifactSHA string  `json:"artifact_sha256"`
	}{Kind: r.Kind(), ArtifactSHA: r.SHA.String()})
}

func FormatRef(ref Ref) string {
	if ref == nil || !ref.Valid() {
		panic("invalid ref")
	}
	switch r := ref.(type) {
	case MessageRef:
		return "msg:v1:" + b64(r.Session.String()) + ":" + b64(r.Entry.String())
	case SessionRef:
		return "session:v1:" + b64(r.Session.String())
	case ArtifactRef:
		return "artifact:v1:" + r.SHA.String()
	default:
		panic("unknown ref variant")
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
	return nil, UnsupportedRefError{Text: s}
}

func b64(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }

func unb64(s string) (string, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return "", fmt.Errorf("invalid ref encoding: %w", err)
	}
	return string(b), nil
}

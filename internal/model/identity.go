package model

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"
)

type SourceName struct{ value string }
type MachineID struct{ value string }
type SourceSessionID struct{ value string }
type SessionKey struct{ value string }
type EntryID struct{ value string }
type SHA256Hex struct{ value string }

func NewSourceName(s string) (SourceName, error) {
	if strings.TrimSpace(s) == "" {
		return SourceName{}, fmt.Errorf("source name required")
	}
	return SourceName{value: s}, nil
}

func NewMachineID(s string) (MachineID, error) {
	if strings.TrimSpace(s) == "" {
		return MachineID{}, fmt.Errorf("machine id required")
	}
	return MachineID{value: s}, nil
}

func NewSourceSessionID(s string) (SourceSessionID, error) {
	if strings.TrimSpace(s) == "" {
		return SourceSessionID{}, fmt.Errorf("source session id required")
	}
	return SourceSessionID{value: s}, nil
}

func NewEntryID(s string) (EntryID, error) {
	if strings.TrimSpace(s) == "" || strings.ContainsAny(s, "\x00\n\r") {
		return EntryID{}, fmt.Errorf("invalid entry id")
	}
	return EntryID{value: s}, nil
}

func ParseSessionKey(s string) (SessionKey, error) {
	if strings.TrimSpace(s) == "" || strings.ContainsAny(s, "\x00\n\r") {
		return SessionKey{}, fmt.Errorf("invalid session key")
	}
	return SessionKey{value: s}, nil
}

func NewLegacySessionKey(source, machine, sourceSession string) (SessionKey, error) {
	if _, err := NewSourceName(source); err != nil {
		return SessionKey{}, err
	}
	if _, err := NewMachineID(machine); err != nil {
		return SessionKey{}, err
	}
	if _, err := NewSourceSessionID(sourceSession); err != nil {
		return SessionKey{}, err
	}
	return SessionKey{value: strings.Join([]string{source, machine, sourceSession}, ":")}, nil
}

func MustLegacySessionKey(source, machine, sourceSession string) string {
	key, err := NewLegacySessionKey(source, machine, sourceSession)
	if err != nil {
		panic(err)
	}
	return key.String()
}

func NewSessionKey(source, machine, sourceSession string) (SessionKey, error) {
	if _, err := NewSourceName(source); err != nil {
		return SessionKey{}, err
	}
	if _, err := NewMachineID(machine); err != nil {
		return SessionKey{}, err
	}
	if _, err := NewSourceSessionID(sourceSession); err != nil {
		return SessionKey{}, err
	}
	h := sha256.New()
	writeLengthPrefixed(h, source)
	writeLengthPrefixed(h, machine)
	writeLengthPrefixed(h, sourceSession)
	return SessionKey{value: "sk1_" + hex.EncodeToString(h.Sum(nil))}, nil
}

func ParseSHA256Hex(s string) (SHA256Hex, error) {
	if len(s) != 64 {
		return SHA256Hex{}, fmt.Errorf("sha256 must be 64 hex characters")
	}
	if _, err := hex.DecodeString(s); err != nil {
		return SHA256Hex{}, fmt.Errorf("invalid sha256: %w", err)
	}
	if strings.ToLower(s) != s {
		return SHA256Hex{}, fmt.Errorf("sha256 must be lowercase hex")
	}
	return SHA256Hex{value: s}, nil
}

func (v SourceName) String() string      { return v.value }
func (v MachineID) String() string       { return v.value }
func (v SourceSessionID) String() string { return v.value }
func (v SessionKey) String() string      { return v.value }
func (v EntryID) String() string         { return v.value }
func (v SHA256Hex) String() string       { return v.value }

func (v SourceName) Valid() bool      { return v.value != "" }
func (v MachineID) Valid() bool       { return v.value != "" }
func (v SourceSessionID) Valid() bool { return v.value != "" }
func (v SessionKey) Valid() bool      { return v.value != "" }
func (v EntryID) Valid() bool         { return v.value != "" }
func (v SHA256Hex) Valid() bool       { return v.value != "" }

func writeLengthPrefixed(h interface{ Write([]byte) (int, error) }, s string) {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(len(s)))
	_, _ = h.Write(buf[:])
	_, _ = h.Write([]byte(s))
}

package model

import (
	"errors"
	"strings"
)

// ArchiveBinding is a validated durable identity/address pair recorded by a
// Workspace. Its fields are private so callers cannot construct an empty or
// partially bound Workspace state.
type ArchiveBinding struct {
	identity string
	address  string
}

func NewArchiveBinding(identity, address string) (ArchiveBinding, error) {
	identity = strings.TrimSpace(identity)
	address = strings.TrimSpace(address)
	if identity == "" || address == "" {
		return ArchiveBinding{}, errors.New("archive identity and address are required")
	}
	return ArchiveBinding{identity: identity, address: address}, nil
}

func (b ArchiveBinding) Identity() string { return b.identity }
func (b ArchiveBinding) Address() string  { return b.address }
func (b ArchiveBinding) Valid() bool      { return b.identity != "" && b.address != "" }

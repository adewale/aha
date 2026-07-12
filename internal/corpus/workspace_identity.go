package corpus

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/adewale/aha/internal/model"
	"github.com/adewale/aha/internal/paths"
)

const (
	WorkspaceIdentityFilename = "workspace.json"
	workspaceIdentitySchema   = "aha.workspace.identity.v1"
	workspaceDatabaseSchema   = CurrentWorkspaceSchemaVersion
)

var ErrWorkspaceIdentityChecksum = errors.New("Workspace identity checksum is invalid")

type UnsupportedWorkspaceIdentityError struct {
	Schema         string
	DatabaseSchema int
}

func (e *UnsupportedWorkspaceIdentityError) Error() string {
	return fmt.Sprintf("Workspace identity schema %q with database schema %d is not supported; upgrade aha before using this Workspace", e.Schema, e.DatabaseSchema)
}

type workspaceIdentityDocument struct {
	Schema          string `json:"schema"`
	WorkspaceID     string `json:"workspace_id"`
	ArchiveIdentity string `json:"archive_identity"`
	ArchiveAddress  string `json:"archive_address"`
	DatabaseSchema  int    `json:"database_schema"`
	ChecksumSHA256  string `json:"checksum_sha256"`
}

// BindWorkspaceStore durably binds both the SQLite materialisation and its
// external recovery witness. Existing Workspaces without a witness are
// upgraded on their next successful mutating download.
func BindWorkspaceStore(store *Store, binding model.ArchiveBinding) error {
	if store == nil || store.DB == nil || strings.TrimSpace(store.Root) == "" {
		return errors.New("invalid Workspace store")
	}
	existing, ok, err := WorkspaceIdentity(store.Root)
	if err != nil {
		return err
	}
	if ok && existing != binding {
		return ErrArchiveMismatch
	}
	if err := BindWorkspace(store.DB, binding); err != nil {
		return err
	}
	if ok {
		return nil
	}
	doc, err := newWorkspaceIdentityDocument(binding)
	if err != nil {
		return err
	}
	return writeWorkspaceIdentity(store.Root, doc)
}

// WorkspaceIdentity reads and authenticates the external recovery witness.
// It does not open SQLite or mutate the Workspace.
func WorkspaceIdentity(root string) (model.ArchiveBinding, bool, error) {
	expanded, err := paths.Expand(root)
	if err != nil {
		return model.ArchiveBinding{}, false, err
	}
	path := filepath.Join(expanded, WorkspaceIdentityFilename)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return model.ArchiveBinding{}, false, nil
	}
	if err != nil {
		return model.ArchiveBinding{}, false, err
	}
	if !info.Mode().IsRegular() {
		return model.ArchiveBinding{}, false, fmt.Errorf("Workspace identity is not a regular file")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return model.ArchiveBinding{}, false, err
	}
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.DisallowUnknownFields()
	var doc workspaceIdentityDocument
	if err := decoder.Decode(&doc); err != nil {
		return model.ArchiveBinding{}, false, err
	}
	if doc.Schema != workspaceIdentitySchema || doc.DatabaseSchema > workspaceDatabaseSchema || doc.DatabaseSchema <= 0 {
		return model.ArchiveBinding{}, false, &UnsupportedWorkspaceIdentityError{Schema: doc.Schema, DatabaseSchema: doc.DatabaseSchema}
	}
	want := workspaceIdentityChecksum(doc)
	got, err := hex.DecodeString(doc.ChecksumSHA256)
	if err != nil || len(got) != sha256.Size || subtle.ConstantTimeCompare(got, want[:]) != 1 {
		return model.ArchiveBinding{}, false, ErrWorkspaceIdentityChecksum
	}
	if len(doc.WorkspaceID) != 32 {
		return model.ArchiveBinding{}, false, errors.New("Workspace identity has an invalid workspace id")
	}
	if _, err := hex.DecodeString(doc.WorkspaceID); err != nil {
		return model.ArchiveBinding{}, false, errors.New("Workspace identity has an invalid workspace id")
	}
	binding, err := model.NewArchiveBinding(doc.ArchiveIdentity, doc.ArchiveAddress)
	if err != nil {
		return model.ArchiveBinding{}, false, err
	}
	return binding, true, nil
}

func newWorkspaceIdentityDocument(binding model.ArchiveBinding) (workspaceIdentityDocument, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return workspaceIdentityDocument{}, err
	}
	doc := workspaceIdentityDocument{
		Schema:          workspaceIdentitySchema,
		WorkspaceID:     hex.EncodeToString(id[:]),
		ArchiveIdentity: binding.Identity(),
		ArchiveAddress:  binding.Address(),
		DatabaseSchema:  workspaceDatabaseSchema,
	}
	sum := workspaceIdentityChecksum(doc)
	doc.ChecksumSHA256 = hex.EncodeToString(sum[:])
	return doc, nil
}

func workspaceIdentityChecksum(doc workspaceIdentityDocument) [sha256.Size]byte {
	return sha256.Sum256([]byte(strings.Join([]string{
		doc.Schema,
		doc.WorkspaceID,
		doc.ArchiveIdentity,
		doc.ArchiveAddress,
		fmt.Sprintf("%d", doc.DatabaseSchema),
	}, "\x00")))
}

func writeWorkspaceIdentity(root string, doc workspaceIdentityDocument) error {
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	tmp, err := os.CreateTemp(root, ".workspace-identity-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}
	if err := tmp.Chmod(0o600); err != nil {
		cleanup()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, filepath.Join(root, WorkspaceIdentityFilename)); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	dir, err := os.Open(root)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

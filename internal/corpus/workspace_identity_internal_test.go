package corpus

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/adewale/aha/internal/model"
)

func TestWritableMigrationRefreshesWorkspaceIdentitySchemaWitness(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspace")
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := model.NewArchiveBinding("archive-id", "local:/archive")
	if err != nil {
		t.Fatal(err)
	}
	if err := BindWorkspaceStore(store, binding); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, WorkspaceIdentityFilename)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc workspaceIdentityDocument
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	doc.DatabaseSchema = CurrentWorkspaceSchemaVersion - 1
	sum := workspaceIdentityChecksum(doc)
	doc.ChecksumSHA256 = hex.EncodeToString(sum[:])
	if err := writeWorkspaceIdentity(root, doc); err != nil {
		t.Fatal(err)
	}
	migrated, err := OpenExisting(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := migrated.Close(); err != nil {
		t.Fatal(err)
	}
	b, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.DatabaseSchema != CurrentWorkspaceSchemaVersion {
		t.Fatalf("identity database_schema=%d, want %d", doc.DatabaseSchema, CurrentWorkspaceSchemaVersion)
	}
	if _, ok, err := WorkspaceIdentity(root); err != nil || !ok {
		t.Fatalf("refreshed identity invalid: ok=%v err=%v", ok, err)
	}
}

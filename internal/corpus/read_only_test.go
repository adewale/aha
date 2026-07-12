package corpus_test

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/adewale/aha/internal/corpus"
	"github.com/adewale/aha/internal/model"
)

func workspaceTreeDigest(t *testing.T, root string) string {
	t.Helper()
	h := sha256.New()
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		fmt.Fprintf(h, "%s:%s:%d:%d\n", rel, info.Mode(), info.Size(), info.ModTime().UnixNano())
		if info.Mode().IsRegular() {
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			_, _ = h.Write(b)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

func TestWorkspaceDatabaseFilenameFencesPre02Writers(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspace")
	store, err := corpus.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, model.WorkspaceDatabaseFilename)); err != nil {
		t.Fatalf("current Workspace database: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "corpus.db")); !os.IsNotExist(err) {
		t.Fatalf("current Workspace exposed pre-0.2 database filename: %v", err)
	}
}

func TestPre02DatabaseIsNeverOpenedOrMutatedAsCurrentWorkspace(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspace")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	legacyPath := filepath.Join(root, "corpus.db")
	before := []byte("frozen-pre-0.2-database")
	if err := os.WriteFile(legacyPath, before, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := corpus.OpenExisting(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("OpenExisting error=%v, want current database absent", err)
	}
	after, err := os.ReadFile(legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Fatal("current binary mutated pre-0.2 database")
	}
	if _, err := os.Stat(filepath.Join(root, model.WorkspaceDatabaseFilename)); !os.IsNotExist(err) {
		t.Fatalf("refusal created current database: %v", err)
	}
}

func TestReadOnlyWorkspaceURIQuotesReservedPathCharactersWithoutMutation(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspace ?#%")
	writer, err := corpus.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.DB.Exec(`insert into machines(machine_id) values('quoted-path')`); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	before := workspaceTreeDigest(t, root)
	reader, err := corpus.OpenExistingReadOnly(root)
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err := reader.DB.QueryRow(`select count(*) from machines where machine_id='quoted-path'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("read-only Workspace saw %d rows, want 1", count)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	if after := workspaceTreeDigest(t, root); after != before {
		t.Fatalf("read-only open mutated reserved-character Workspace: before=%s after=%s", before, after)
	}
}

func TestReadOnlyWorkspaceRefusesWALWithoutCreatingRecoverySidecars(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspace")
	writer, err := corpus.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if _, err := writer.DB.Exec(`insert into machines(machine_id) values('visible-in-wal')`); err != nil {
		t.Fatal(err)
	}
	before := workspaceTreeDigest(t, root)
	_, err = corpus.OpenExistingReadOnly(root)
	var busy *corpus.WorkspaceReadOnlyBusyError
	if !errors.As(err, &busy) {
		t.Fatalf("OpenExistingReadOnly error=%T %v, want WorkspaceReadOnlyBusyError", err, err)
	}
	if after := workspaceTreeDigest(t, root); after != before {
		t.Fatalf("WAL refusal mutated Workspace: before=%s after=%s", before, after)
	}
}

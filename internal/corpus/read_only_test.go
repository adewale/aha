package corpus_test

import (
	"path/filepath"
	"testing"

	"github.com/adewale/aha/internal/corpus"
)

func TestReadOnlyWorkspaceSeesCommittedWALRows(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspace")
	writer, err := corpus.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if _, err := writer.DB.Exec(`insert into machines(machine_id) values('visible-in-wal')`); err != nil {
		t.Fatal(err)
	}
	reader, err := corpus.OpenExistingReadOnly(root)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	var count int
	if err := reader.DB.QueryRow(`select count(*) from machines where machine_id='visible-in-wal'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("read-only Workspace saw %d committed WAL rows, want 1", count)
	}
}

package corpus_test

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/adewale/aha/internal/corpus"
	_ "modernc.org/sqlite"
)

func TestConflictsAreBoundedAndPaginated(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`create table conflicts(conflict_id integer primary key,session_key text,entry_id text,first_entry_sha256 text,second_entry_sha256 text,details_json text,created_at text)`); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 250; i++ {
		if _, err := db.Exec(`insert into conflicts values(?,?,?,?,?,?,?)`, i, "s", "e", "a", "b", `{}`, "now"); err != nil {
			t.Fatal(err)
		}
	}
	first, err := corpus.Conflicts(db)
	if err != nil || len(first) != corpus.MaxConflictsPageSize {
		t.Fatalf("default conflicts len=%d err=%v", len(first), err)
	}
	second, err := corpus.ConflictsPage(db, 50, 200)
	if err != nil || len(second) != 50 || second[0].ID != 201 {
		t.Fatalf("second page=%v len=%d err=%v", second, len(second), err)
	}
	if _, err := corpus.ConflictsPage(db, corpus.MaxConflictsPageSize+1, 0); err == nil {
		t.Fatal("accepted unbounded conflicts page")
	}
}

func TestStatusReportsSchemaErrors(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	_, err = corpus.Status(db, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "machines") {
		t.Fatalf("Status err=%v, want missing machines table", err)
	}
}

package corpus_test

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/adewale/aha/internal/corpus"
	_ "modernc.org/sqlite"
)

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

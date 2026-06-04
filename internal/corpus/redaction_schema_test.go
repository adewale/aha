package corpus_test

import (
	"path/filepath"
	"testing"

	"github.com/adewale/aha/internal/corpus"
)

// TestRedactionSchemaIsPresent pins migration 11 from
// docs/redaction-spec.md: a fresh corpus has sessions.redaction_level
// (default 'none-v1') and the redactions table.
func TestRedactionSchemaIsPresent(t *testing.T) {
	store, err := corpus.Open(filepath.Join(t.TempDir(), "corpus"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	// sessions.redaction_level column.
	var name, dflt string
	var notnull int
	if err := store.DB.QueryRow(`select name, "notnull", coalesce(dflt_value,'') from pragma_table_info('sessions') where name='redaction_level'`).Scan(&name, &notnull, &dflt); err != nil {
		t.Fatalf("redaction_level column missing: %v", err)
	}
	if dflt != "'none-v1'" {
		t.Fatalf("redaction_level default = %q, want 'none-v1'", dflt)
	}

	// redactions table.
	rows, err := store.DB.Query(`select name from pragma_table_info('redactions')`)
	if err != nil {
		t.Fatalf("redactions table query failed: %v", err)
	}
	defer rows.Close()
	got := map[string]bool{}
	for rows.Next() {
		var col string
		if err := rows.Scan(&col); err != nil {
			t.Fatal(err)
		}
		got[col] = true
	}
	wantCols := []string{"session_key", "entry_id", "pattern", "count"}
	for _, c := range wantCols {
		if !got[c] {
			t.Fatalf("redactions.%s missing; got columns=%v", c, got)
		}
	}

	// Primary key (session_key, entry_id, pattern) plus append-only
	// triggers are what prevent duplicate ingest from mutating counts.

	eventRows, err := store.DB.Query(`select name from pragma_table_info('redaction_events')`)
	if err != nil {
		t.Fatalf("redaction_events table query failed: %v", err)
	}
	defer eventRows.Close()
	eventCols := map[string]bool{}
	for eventRows.Next() {
		var name string
		if err := eventRows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		eventCols[name] = true
	}
	for _, c := range []string{"redaction_id", "session_key", "subject_kind", "subject_id", "entry_id", "artifact_id", "surface", "pattern", "count", "created_at"} {
		if !eventCols[c] {
			t.Fatalf("redaction_events.%s missing; got columns=%v", c, eventCols)
		}
	}
}

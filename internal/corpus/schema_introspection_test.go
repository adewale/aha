package corpus

import (
	"database/sql"
	"strings"
	"testing"
)

func TestSchemaIntrospectionHelpers(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if !schemaHasTable(t, store.DB, "entries") || !schemaHasColumn(t, store.DB, "entries", "entry_id") || !schemaHasIndex(t, store.DB, "idx_entries_session_line") {
		t.Fatal("schema introspection helpers failed to find known table/column/index")
	}
	if !schemaHasTrigger(t, store.DB, "entries_no_update") || !schemaHasForeignKey(t, store.DB, "messages", "entries") || !schemaHasCheck(t, store.DB, "sessions", "sk1_") {
		t.Fatal("schema introspection helpers failed to find known trigger/foreign key/check")
	}
}

func schemaHasTable(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	return schemaCount(t, db, `select count(*) from sqlite_master where type='table' and name=?`, name) > 0
}

func schemaHasColumn(t *testing.T, db *sql.DB, table, column string) bool {
	t.Helper()
	rows, err := db.Query(`pragma table_info(` + table + `)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var dflt any
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			t.Fatal(err)
		}
		if name == column {
			return true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return false
}

func schemaHasIndex(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	return schemaCount(t, db, `select count(*) from sqlite_master where type='index' and name=?`, name) > 0
}

func schemaHasTrigger(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	return schemaCount(t, db, `select count(*) from sqlite_master where type='trigger' and name=?`, name) > 0
}

func schemaHasForeignKey(t *testing.T, db *sql.DB, table, target string) bool {
	t.Helper()
	rows, err := db.Query(`pragma foreign_key_list(` + table + `)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		values := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		for i, col := range cols {
			if col == "table" && stringValue(values[i]) == target {
				return true
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return false
}

func schemaHasCheck(t *testing.T, db *sql.DB, table, fragment string) bool {
	t.Helper()
	var sqlText string
	if err := db.QueryRow(`select sql from sqlite_master where type='table' and name=?`, table).Scan(&sqlText); err != nil {
		t.Fatal(err)
	}
	return strings.Contains(strings.ToLower(sqlText), "check") && strings.Contains(sqlText, fragment)
}

func schemaCount(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func stringValue(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []byte:
		return string(x)
	default:
		return ""
	}
}

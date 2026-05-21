package corpus_test

import (
	"strings"
	"testing"

	"github.com/adewale/aha/internal/corpus"
)

func TestReadContextPrefersExactSessionOverAmbiguousPrefix(t *testing.T) {
	store := openReadResolutionStore(t)
	insertReadSession(t, store, "pi:m:abc", "abc", "exact", 1)
	insertReadSession(t, store, "pi:m:abc2", "abc2", "prefix", 1)

	entries, err := corpus.ReadContext(store.DB, "abc", "", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].EntryID != "exact" {
		t.Fatalf("ReadContext exact precedence entries=%+v", entries)
	}
}

func TestReadContextResolvesLiteralWildcardPrefix(t *testing.T) {
	store := openReadResolutionStore(t)
	insertReadSession(t, store, "pi:m:abc%literal", "abc%literal", "e1", 1)
	insertReadSession(t, store, "pi:m:abc_plain", "abc_plain", "e1", 1)

	entries, err := corpus.ReadContext(store.DB, "abc%", "", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].EntryID != "e1" {
		t.Fatalf("ReadContext literal wildcard prefix entries=%+v", entries)
	}
}

func TestReadContextEscapesSessionPrefixWildcards(t *testing.T) {
	store := openReadResolutionStore(t)
	insertReadSession(t, store, "pi:m:abc1", "abc1", "e1", 1)
	insertReadSession(t, store, "pi:m:abc2", "abc2", "e1", 1)

	_, err := corpus.ReadContext(store.DB, "abc%", "", 0, 0)
	if err == nil || strings.Contains(err.Error(), "ambiguous") || !strings.Contains(err.Error(), "session not found") {
		t.Fatalf("ReadContext session wildcard err=%v, want not found without wildcard expansion", err)
	}
}

func TestReadContextResolvesLiteralEntryWildcardPrefix(t *testing.T) {
	store := openReadResolutionStore(t)
	insertReadSession(t, store, "pi:m:s1", "s1", "entry_1", 1)
	insertReadEntry(t, store, "pi:m:s1", "entryA", 2)

	entries, err := corpus.ReadContext(store.DB, "s1", "entry_", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].EntryID != "entry_1" {
		t.Fatalf("ReadContext literal entry wildcard entries=%+v", entries)
	}
}

func TestReadContextEscapesEntryPrefixWildcards(t *testing.T) {
	store := openReadResolutionStore(t)
	insertReadSession(t, store, "pi:m:s1", "s1", "entryA", 1)
	insertReadEntry(t, store, "pi:m:s1", "entryB", 2)

	_, err := corpus.ReadContext(store.DB, "s1", "entry_", 0, 0)
	if err == nil || strings.Contains(err.Error(), "ambiguous") || !strings.Contains(err.Error(), "entry not found") {
		t.Fatalf("ReadContext entry wildcard err=%v, want not found without wildcard expansion", err)
	}
}

func openReadResolutionStore(t *testing.T) *corpus.Store {
	t.Helper()
	store, err := corpus.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func insertReadSession(t *testing.T, store *corpus.Store, sessionKey, sourceSessionID, entryID string, lineNo int) {
	t.Helper()
	if _, err := store.DB.Exec(`insert into sessions(session_key,source_name,source_session_id,machine_id,raw_cwd,project_key,started_at,source_metadata_json) values(?,?,?,?,?,?,?,?)`, sessionKey, "pi", sourceSessionID, "m", "/tmp", "tmp", "2026-01-01T00:00:00Z", `{}`); err != nil {
		t.Fatal(err)
	}
	insertReadEntry(t, store, sessionKey, entryID, lineNo)
}

func insertReadEntry(t *testing.T, store *corpus.Store, sessionKey, entryID string, lineNo int) {
	t.Helper()
	if _, err := store.DB.Exec(`insert into entries(session_key,entry_id,parent_id,line_no,entry_type,timestamp,role,entry_sha256,raw_json,source_metadata_json) values(?,?,?,?,?,?,?,?,?,?)`, sessionKey, entryID, "", lineNo, "message", "2026-01-01T00:00:00Z", "user", entryID+"sha", `{}`, `{}`); err != nil {
		t.Fatal(err)
	}
}

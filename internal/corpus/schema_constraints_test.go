package corpus_test

import "github.com/adewale/aha/internal/corpus"

import (
	"strings"
	"testing"
)

func TestSchemaRejectsInvalidStates(t *testing.T) {
	store, err := corpus.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.DB.Exec(`insert into entries(session_key,entry_id,line_no,role,entry_sha256,raw_json) values('missing','e',1,'user','h','{}')`); err == nil || !strings.Contains(err.Error(), "entry session missing") {
		t.Fatalf("orphan entry err=%v", err)
	}
	if _, err := store.DB.Exec(`insert into messages(session_key,entry_id,role,text) values('missing','e','user','text')`); err == nil || !strings.Contains(err.Error(), "message entry missing") {
		t.Fatalf("orphan message err=%v", err)
	}
	if _, err := store.DB.Exec(`insert into artifacts(artifact_sha256,source_name,machine_id,bundle_id,kind,raw_path,relative_path) values('a','pi','m','missing','artifact','r','rel')`); err == nil || !strings.Contains(err.Error(), "artifact bundle missing") {
		t.Fatalf("orphan artifact err=%v", err)
	}
}

func TestSchemaAppendOnlyTriggersRejectMutation(t *testing.T) {
	store, legacyRef := corpusWithOneEntry(t)
	defer store.Close()
	ref, err := corpus.ResolveHuman(store.DB, legacyRef.SessionKey, legacyRef.EntryID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.Exec(`update entries set role='assistant' where session_key=? and entry_id=?`, ref.SessionKey, ref.EntryID); err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("update entries err=%v", err)
	}
	if _, err := store.DB.Exec(`delete from entries where session_key=? and entry_id=?`, ref.SessionKey, ref.EntryID); err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("delete entries err=%v", err)
	}
}

func TestSchemaConflictTriggerQuarantinesDirectInsert(t *testing.T) {
	store, legacyRef := corpusWithOneEntry(t)
	defer store.Close()
	ref, err := corpus.ResolveHuman(store.DB, legacyRef.SessionKey, legacyRef.EntryID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.Exec(`insert into entries(session_key,entry_id,line_no,entry_type,timestamp,role,entry_sha256,raw_json,source_metadata_json) values(?,?,?,?,?,?,?,?,?)`, ref.SessionKey, ref.EntryID, 99, "message", "2026", "user", "different", `{"changed":true}`, `{}`); err != nil {
		t.Fatal(err)
	}
	var conflicts int
	if err := store.DB.QueryRow(`select count(*) from conflicts where session_key=? and entry_id=?`, ref.SessionKey, ref.EntryID).Scan(&conflicts); err != nil {
		t.Fatal(err)
	}
	if conflicts != 1 {
		t.Fatalf("conflicts=%d want 1", conflicts)
	}
}

func TestFTSTriggersPopulateSearchTables(t *testing.T) {
	store, legacyRef := corpusWithOneEntry(t)
	defer store.Close()
	ref, err := corpus.ResolveHuman(store.DB, legacyRef.SessionKey, legacyRef.EntryID)
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err := store.DB.QueryRow(`select count(*) from fts_messages where session_key=? and entry_id=?`, ref.SessionKey, ref.EntryID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("fts messages rows=%d want 1", n)
	}
}

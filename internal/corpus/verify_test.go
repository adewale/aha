package corpus_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/adewale/aha/internal/corpus"
)

func TestVerifyReportsSeededCorpusDrift(t *testing.T) {
	store, _ := corpusWithOneEntry(t)
	defer store.Close()
	if _, err := store.DB.Exec(`pragma foreign_keys=off`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.Exec(`drop trigger if exists messages_require_entry`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.Exec(`insert into messages(session_key,entry_id,role,text) values('missing-session','missing-entry','user','orphan')`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.Exec(`pragma foreign_keys=on`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.Exec(`insert into fts_messages(session_key,entry_id,text) values('fts-only','e','ghost')`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.Exec(`insert into bundles(bundle_id,bundle_sha256,machine_id,captured_at,ingested_at,manifest_json) values('missing-blob','` + strings.Repeat("a", 64) + `','m','2026','2026','{}')`); err != nil {
		t.Fatal(err)
	}
	report, err := corpus.Verify(store)
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{"orphan_messages", "orphan_fts_messages", "missing_bundle_blob"} {
		if !report.HasProblem(code) {
			t.Fatalf("Verify missing %s in %+v", code, report.Problems)
		}
	}
}

func TestVerifyCleanCorpus(t *testing.T) {
	store, _ := corpusWithOneEntry(t)
	defer store.Close()
	report, err := corpus.Verify(store)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Problems) != 0 {
		t.Fatalf("Verify clean corpus problems: %+v", report.Problems)
	}
	if !strings.HasPrefix(report.Root, filepath.Clean(store.Root)) {
		t.Fatalf("Verify root=%q want under %q", report.Root, store.Root)
	}
}

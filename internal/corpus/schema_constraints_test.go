package corpus_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/adewale/aha/internal/corpus"
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
	if _, err := store.DB.Exec(`insert into artifacts(artifact_sha256,source_name,machine_id,manifest_sha256,kind,raw_path,relative_path) values('a','pi','m','ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff','artifact','r','rel')`); err == nil || !strings.Contains(err.Error(), "artifact snapshot missing") {
		t.Fatalf("orphan artifact err=%v", err)
	}
	if _, err := store.DB.Exec(`insert into sessions(session_key,source_name,source_session_id,machine_id) values('pi:m:s','pi','s','m')`); err == nil || !strings.Contains(err.Error(), "session key must be sk1") {
		t.Fatalf("legacy session key err=%v", err)
	}
	badHexKey := "sk1_" + strings.Repeat("z", 64)
	if _, err := store.DB.Exec(`insert into sessions(session_key,source_name,source_session_id,machine_id) values(?,'pi','s','m')`, badHexKey); err == nil || !strings.Contains(err.Error(), "session key must be sk1") {
		t.Fatalf("non-hex session key err=%v", err)
	}
}

// TestSchemaAppendOnlyTriggersRejectMutation is the Type B test for the
// append-only tables listed in docs/correctness-by-construction-spec.md: each
// one carries BEFORE UPDATE/DELETE triggers, so direct SQL cannot rewrite or
// remove a row. Every table is seeded with a row first, because a trigger
// never fires for a statement that matches no rows.
func TestSchemaAppendOnlyTriggersRejectMutation(t *testing.T) {
	store, ref := corpusWithOneEntry(t)
	defer store.Close()
	session, entry := ref.Session.String(), ref.Entry.String()
	manifest, artifact := strings.Repeat("b", 64), strings.Repeat("a", 64)
	seeds := []struct {
		query string
		args  []any
	}{
		{`insert into snapshots(manifest_sha256,machine_id,captured_at,ingested_at,manifest_json) values(?,'m','2026','2026','{}')`, []any{manifest}},
		{`insert into artifacts(artifact_sha256,source_name,machine_id,manifest_sha256,kind,raw_path,relative_path,text_preview) values(?,'pi','m',?,'artifact','raw','rel','preview')`, []any{artifact, manifest}},
		{`insert into conflicts(session_key,entry_id,first_entry_sha256,second_entry_sha256,details_json) values(?,?,'first','second','{}')`, []any{session, entry}},
		{`insert into tool_invocations(session_key,entry_id,tool_key,tool_name) values(?,?,'tool-key','bash')`, []any{session, entry}},
		{`insert into redactions(session_key,entry_id,pattern,count) values(?,?,'aws-access-key',1)`, []any{session, entry}},
		{`insert into redaction_events(session_key,subject_kind,subject_id,entry_id,surface,pattern,count) values(?,'entry',?,?,'text','aws-access-key',1)`, []any{session, entry, entry}},
	}
	for _, seed := range seeds {
		if _, err := store.DB.Exec(seed.query, seed.args...); err != nil {
			t.Fatalf("seed %q: %v", seed.query, err)
		}
	}
	cases := []struct {
		table  string
		update string
	}{
		{"entries", `update entries set role='assistant'`},
		{"messages", `update messages set text='rewritten'`},
		{"artifacts", `update artifacts set text_preview='rewritten'`},
		{"conflicts", `update conflicts set details_json='{"rewritten":true}'`},
		{"tool_invocations", `update tool_invocations set tool_name='rewritten'`},
		{"redactions", `update redactions set count=0`},
		{"redaction_events", `update redaction_events set surface='rewritten'`},
	}
	// The schema, not this list, decides which tables are append-only: a new
	// table with append-only triggers must be added to the cases above.
	tested := map[string]bool{}
	for _, tc := range cases {
		tested[tc.table] = true
	}
	declared := map[string]bool{}
	triggers, err := store.DB.Query(`select distinct tbl_name from sqlite_master where type='trigger' and sql like '%are append-only%'`)
	if err != nil {
		t.Fatal(err)
	}
	for triggers.Next() {
		var table string
		if err := triggers.Scan(&table); err != nil {
			t.Fatal(err)
		}
		declared[table] = true
	}
	if err := triggers.Close(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(declared, tested) {
		t.Fatalf("append-only tables in the schema %v, tested %v", declared, tested)
	}
	for _, tc := range cases {
		t.Run(tc.table, func(t *testing.T) {
			rows := func() int {
				t.Helper()
				var n int
				if err := store.DB.QueryRow(`select count(*) from ` + tc.table).Scan(&n); err != nil {
					t.Fatal(err)
				}
				return n
			}
			before := rows()
			if before == 0 {
				t.Fatalf("%s has no rows, so its triggers would never fire", tc.table)
			}
			if _, err := store.DB.Exec(tc.update); err == nil || !strings.Contains(err.Error(), "append-only") {
				t.Errorf("update %s err=%v, want append-only rejection", tc.table, err)
			}
			if _, err := store.DB.Exec(`delete from ` + tc.table); err == nil || !strings.Contains(err.Error(), "append-only") {
				t.Errorf("delete from %s err=%v, want append-only rejection", tc.table, err)
			}
			if after := rows(); after != before {
				t.Errorf("%s rows=%d after rejected mutations, want %d", tc.table, after, before)
			}
		})
	}
}

func TestSchemaConflictTriggerQuarantinesDirectInsert(t *testing.T) {
	store, ref := corpusWithOneEntry(t)
	defer store.Close()
	session, entry := ref.Session.String(), ref.Entry.String()
	if _, err := store.DB.Exec(`insert into entries(session_key,entry_id,line_no,entry_type,timestamp,role,entry_sha256,raw_json,source_metadata_json) values(?,?,?,?,?,?,?,?,?)`, session, entry, 99, "message", "2026", "user", "different", `{"changed":true}`, `{}`); err != nil {
		t.Fatal(err)
	}
	var conflicts int
	if err := store.DB.QueryRow(`select count(*) from conflicts where session_key=? and entry_id=?`, session, entry).Scan(&conflicts); err != nil {
		t.Fatal(err)
	}
	if conflicts != 1 {
		t.Fatalf("conflicts=%d want 1", conflicts)
	}
}

func TestFTSTriggersPopulateSearchTables(t *testing.T) {
	store, ref := corpusWithOneEntry(t)
	defer store.Close()
	session, entry := ref.Session.String(), ref.Entry.String()
	var n int
	if err := store.DB.QueryRow(`select count(*) from fts_messages where session_key=? and entry_id=?`, session, entry).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("fts messages rows=%d want 1", n)
	}
}

func TestArtifactFTSTriggerFallsBackFromEmptyBodyToPreview(t *testing.T) {
	store, err := corpus.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.DB.Exec(`insert into snapshots(manifest_sha256,machine_id,captured_at,ingested_at,manifest_json) values('` + strings.Repeat("b", 64) + `','m','2026','2026','{}')`); err != nil {
		t.Fatal(err)
	}
	res, err := store.DB.Exec(`insert into artifacts(artifact_sha256,source_name,machine_id,manifest_sha256,kind,raw_path,relative_path,text_preview,text_body) values(?,'pi','m','`+strings.Repeat("b", 64)+`','artifact','raw','rel','preview text','')`, strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	artifactID, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	var got string
	if err := store.DB.QueryRow(`select text from fts_artifacts where rowid=?`, artifactID).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != "preview text" {
		t.Fatalf("fts artifact text=%q want preview fallback", got)
	}
	verify, err := corpus.Verify(store)
	if err != nil {
		t.Fatal(err)
	}
	if verify.HasProblem("missing_fts_artifacts") {
		t.Fatalf("verify reports missing FTS artifact after trigger insert: %+v", verify.Problems)
	}
}

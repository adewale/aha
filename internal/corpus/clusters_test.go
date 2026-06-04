package corpus_test

import (
	"strconv"
	"testing"

	"github.com/adewale/aha/internal/corpus"
	"github.com/adewale/aha/internal/model"
)

func TestClustersRanksRecurringFailures(t *testing.T) {
	store, err := corpus.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	// One recurring failure across 3 sessions, one one-off failure, and one
	// success (which must be excluded from clusters).
	insertInvocation(t, store, "s1", "e1", "Bash", "gh pr create", true, "head sha cant be blank")
	insertInvocation(t, store, "s2", "e1", "Bash", "gh pr create", true, "head sha cant be blank")
	insertInvocation(t, store, "s3", "e1", "Bash", "gh pr create", true, "head sha cant be blank")
	insertInvocation(t, store, "s1", "e2", "Bash", "git push", true, "rejected non fast forward")
	insertInvocation(t, store, "s1", "e3", "Bash", "gh pr view", false, "")

	clusters, err := corpus.Clusters(store.DB, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(clusters) != 2 {
		t.Fatalf("want 2 failure clusters, got %d: %+v", len(clusters), clusters)
	}
	top := clusters[0]
	if top.CommandFamily != "gh pr create" {
		t.Fatalf("expected recurring cluster ranked first, got %+v", top)
	}
	if top.Count != 3 || top.Sessions != 3 {
		t.Fatalf("recurring cluster counts wrong: %+v", top)
	}
	if top.SampleRef == "" {
		t.Fatalf("recurring cluster missing sample_ref for drill-in: %+v", top)
	}
	ref, err := model.ParseRef(top.SampleRef)
	if err != nil {
		t.Fatalf("sample_ref is not a valid ref: %v", err)
	}
	// The ref must resolve to a real entry: session_key and entry_id have to
	// come from the *same* invocation row, not independent aggregates.
	entries, err := corpus.ReadCanonical(store.DB, ref, 0, 0)
	if err != nil {
		t.Fatalf("sample_ref does not resolve via read: %v", err)
	}
	if len(entries) == 0 {
		t.Fatalf("sample_ref resolved to no entries: %s", top.SampleRef)
	}
}

func TestClustersOrdersByScoreBeforeCountAndIsDeterministic(t *testing.T) {
	store, err := corpus.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	// Count=3 in one session/project scores 3. Count=2 across two sessions/projects
	// scores 5, so the spread-out failure must win and survive limit=1.
	insertInvocation(t, store, "s1", "e1", "Bash", "single noisy", true, "same error")
	insertInvocation(t, store, "s1", "e2", "Bash", "single noisy", true, "same error")
	insertInvocation(t, store, "s1", "e3", "Bash", "single noisy", true, "same error")
	insertInvocation(t, store, "s2", "e1", "Bash", "spread winner", true, "same error")
	insertInvocation(t, store, "s3", "e1", "Bash", "spread winner", true, "same error")

	clusters, err := corpus.Clusters(store.DB, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(clusters) != 1 || clusters[0].CommandFamily != "spread winner" {
		t.Fatalf("limit=1 should keep score winner, got %+v", clusters)
	}
}

func TestToolInvocationMigrationBackfillsExistingEntries(t *testing.T) {
	root := t.TempDir()
	store, err := corpus.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	key, err := model.NewSessionKey("claude-code", "m", "old-session")
	if err != nil {
		t.Fatal(err)
	}
	sessionKey := key.String()
	if _, err := store.DB.Exec(`insert into sessions(session_key,source_name,source_session_id,machine_id,raw_cwd,project_key,started_at,source_metadata_json) values(?,?,?,?,?,?,?,?)`, sessionKey, "claude-code", "old-session", "m", "/tmp/proj", "proj", "2026-01-01T00:00:00Z", `{}`); err != nil {
		t.Fatal(err)
	}
	callRaw := `{"type":"assistant","id":"call","timestamp":"2026-01-01T00:00:01Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"tu_old","name":"Bash","input":{"command":"gh pr create --title x"}}]}}`
	resultRaw := `{"type":"user","id":"result","timestamp":"2026-01-01T00:00:02Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu_old","is_error":true,"content":"Head sha can't be blank"}]}}`
	if _, err := store.DB.Exec(`insert into entries(session_key,entry_id,parent_id,line_no,entry_type,timestamp,role,entry_sha256,raw_json,source_metadata_json) values(?,?,?,?,?,?,?,?,?,?)`, sessionKey, "call", "", 1, "assistant", "2026-01-01T00:00:01Z", "assistant", "hash-call", callRaw, `{}`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.Exec(`insert into entries(session_key,entry_id,parent_id,line_no,entry_type,timestamp,role,entry_sha256,raw_json,source_metadata_json) values(?,?,?,?,?,?,?,?,?,?)`, sessionKey, "result", "call", 2, "user", "2026-01-01T00:00:02Z", "toolResult", "hash-result", resultRaw, `{}`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.Exec(`drop table tool_invocations`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.Exec(`delete from schema_migrations where version=13`); err != nil {
		t.Fatal(err)
	}
	store.Close()
	store, err = corpus.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	clusters, err := corpus.Clusters(store.DB, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(clusters) != 1 || clusters[0].CommandFamily != "gh pr create" {
		t.Fatalf("backfill did not produce expected cluster: %+v", clusters)
	}
}

func TestClustersDefaultLimitIsBounded(t *testing.T) {
	store, err := corpus.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	for i := 0; i < 60; i++ {
		insertInvocation(t, store, "s-default-limit", "e-limit-"+strconv.Itoa(i), "Bash", "cmd-"+strconv.Itoa(i), true, "same error")
	}
	clusters, err := corpus.Clusters(store.DB, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(clusters) != 50 {
		t.Fatalf("limit=0 should use default page size 50, got %d", len(clusters))
	}
}

func TestClustersExcludesSuccesses(t *testing.T) {
	store, err := corpus.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	insertInvocation(t, store, "s1", "e1", "Bash", "gh pr view", false, "")
	clusters, err := corpus.Clusters(store.DB, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(clusters) != 0 {
		t.Fatalf("successes must not cluster: %+v", clusters)
	}
}

func insertInvocation(t *testing.T, store *corpus.Store, sourceSessionID, entryID, tool, family string, isError bool, errText string) {
	t.Helper()
	key, err := model.NewSessionKey("claude-code", "m", sourceSessionID)
	if err != nil {
		t.Fatal(err)
	}
	sessionKey := key.String()
	if _, err := store.DB.Exec(`insert or ignore into sessions(session_key,source_name,source_session_id,machine_id,raw_cwd,project_key,started_at,source_metadata_json) values(?,?,?,?,?,?,?,?)`,
		sessionKey, "claude-code", sourceSessionID, "m", "/tmp/"+sourceSessionID, sourceSessionID, "2026-01-01T00:00:00Z", `{}`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.Exec(`insert into entries(session_key,entry_id,parent_id,line_no,entry_type,timestamp,role,entry_sha256,raw_json,source_metadata_json) values(?,?,?,?,?,?,?,?,?,?)`,
		sessionKey, entryID, "", 1, "assistant", "2026-01-01T00:00:00Z", "assistant", "h"+entryID+sourceSessionID, `{}`, `{}`); err != nil {
		t.Fatal(err)
	}
	sig := ""
	if isError {
		sig = errText
	}
	if _, err := store.DB.Exec(`insert into tool_invocations(session_key,entry_id,tool_key,tool_use_id,ordinal,tool_name,command_family,command,exit_code,is_error,error_signature,outcome_text,timestamp,project_key,machine_id) values(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		sessionKey, entryID, "#000", "", 0, tool, family, family, nil, boolToInt(isError), sig, sig, "2026-01-01T00:00:00Z", sourceSessionID, "m"); err != nil {
		t.Fatal(err)
	}
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

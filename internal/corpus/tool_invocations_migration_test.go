package corpus_test

import (
	"testing"

	"github.com/adewale/aha/internal/corpus"
	"github.com/adewale/aha/internal/model"
)

// TestToolInvocationMigrationBackfillsExistingEntries proves the tool_invocations
// migration projects paired tool calls from existing entries' raw_json: dropping
// the table + its migration stamp and reopening rebuilds it, and the resulting
// failure surfaces as an incident.
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
	for _, st := range []string{
		`drop table failure_episodes`,
		`drop table tool_invocations`,
		`delete from schema_migrations where version in (13,14)`,
	} {
		if _, err := store.DB.Exec(st); err != nil {
			t.Fatal(err)
		}
	}
	store.Close()
	store, err = corpus.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	incidents, err := corpus.Incidents(store.DB, corpus.IncidentFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(incidents) != 1 || incidents[0].CommandFamily != "gh pr create" {
		t.Fatalf("backfill did not produce the expected incident: %+v", incidents)
	}
	if incidents[0].State != "unresolved" {
		t.Fatalf("a lone failing call should be an unresolved incident, got %q", incidents[0].State)
	}
}

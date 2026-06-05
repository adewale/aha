package corpus_test

import (
	"testing"

	"github.com/adewale/aha/internal/corpus"
	"github.com/adewale/aha/internal/model"
)

// TestFailureEpisodeMigrationBackfillsFromRawEntries proves the full chain runs
// off preserved raw_json with no re-ingest: dropping the projection tables and
// their migration stamps, then reopening, rebuilds tool_invocations (13) and
// failure_episodes (14) and yields a resolved skill candidate.
func TestFailureEpisodeMigrationBackfillsFromRawEntries(t *testing.T) {
	root := t.TempDir()
	store, err := corpus.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	key, err := model.NewSessionKey("claude-code", "m", "confluent-session")
	if err != nil {
		t.Fatal(err)
	}
	sk := key.String()
	if _, err := store.DB.Exec(`insert into sessions(session_key,source_name,source_session_id,machine_id,raw_cwd,project_key,started_at,source_metadata_json) values(?,?,?,?,?,?,?,?)`,
		sk, "claude-code", "confluent-session", "m", "/tmp/proj", "proj", "2026-01-01T00:00:00Z", `{}`); err != nil {
		t.Fatal(err)
	}

	// A failing command, then the same command family succeeding: one resolved
	// episode mined entirely from raw transcript bytes.
	entries := []struct{ id, ts, role, raw string }{
		{"c1", "2026-01-01T00:00:01Z", "assistant", `{"type":"assistant","id":"c1","timestamp":"2026-01-01T00:00:01Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"tu1","name":"Bash","input":{"command":"confluent topic describe foo"}}]}}`},
		{"r1", "2026-01-01T00:00:02Z", "toolResult", `{"type":"user","id":"r1","timestamp":"2026-01-01T00:00:02Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu1","is_error":true,"content":"unknown topic or partition"}]}}`},
		{"c2", "2026-01-01T00:00:03Z", "assistant", `{"type":"assistant","id":"c2","timestamp":"2026-01-01T00:00:03Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"tu2","name":"Bash","input":{"command":"confluent topic describe foo"}}]}}`},
		{"r2", "2026-01-01T00:00:04Z", "toolResult", `{"type":"user","id":"r2","timestamp":"2026-01-01T00:00:04Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu2","is_error":false,"content":"ok"}]}}`},
	}
	for i, e := range entries {
		if _, err := store.DB.Exec(`insert into entries(session_key,entry_id,parent_id,line_no,entry_type,timestamp,role,entry_sha256,raw_json,source_metadata_json) values(?,?,?,?,?,?,?,?,?,?)`,
			sk, e.id, "", i+1, "assistant", e.ts, e.role, "hash-"+e.id, e.raw, `{}`); err != nil {
			t.Fatal(err)
		}
	}

	// Drop both projections and their migration stamps so a reopen rebuilds them
	// from raw_json alone.
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

	candidates, err := corpus.SkillCandidates(store.DB, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 {
		t.Fatalf("want one resolved candidate mined from raw entries, got %d: %+v", len(candidates), candidates)
	}
	cand := candidates[0]
	if cand.CommandFamily != "confluent topic describe" {
		t.Fatalf("unexpected command family: %+v", cand)
	}
	if cand.Resolved != 1 || cand.Episodes != 1 {
		t.Fatalf("want 1/1 resolved episode, got resolved=%d episodes=%d", cand.Resolved, cand.Episodes)
	}
	if cand.Tier != "tentative" {
		t.Fatalf("a single resolved episode should be tentative, got %q", cand.Tier)
	}
	if len(cand.Paths) != 1 || len(cand.Paths[0].Families) == 0 {
		t.Fatalf("want one resolution path with families, got %+v", cand.Paths)
	}
	if got := cand.Paths[0].Families[len(cand.Paths[0].Families)-1]; got != "confluent topic describe" {
		t.Fatalf("resolution path must end at the resolving family, got %q", got)
	}
}

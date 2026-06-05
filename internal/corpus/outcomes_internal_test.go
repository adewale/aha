package corpus

import (
	"database/sql"
	"fmt"
	"testing"

	"github.com/adewale/aha/internal/model"
)

// seedToolInvocation inserts a session (once), a distinct entry, and one
// tool_invocation row, so failure-episode backfill has real rows to project.
func seedToolInvocation(t *testing.T, db *sql.DB, srcSession, entryID string, ordinal int, tool, family string, isError bool, sig, ts string) {
	t.Helper()
	key, err := model.NewSessionKey("claude-code", "m", srcSession)
	if err != nil {
		t.Fatal(err)
	}
	sk := key.String()
	if _, err := db.Exec(`insert or ignore into sessions(session_key,source_name,source_session_id,machine_id,raw_cwd,project_key,started_at,source_metadata_json) values(?,?,?,?,?,?,?,?)`,
		sk, "claude-code", srcSession, "m", "/tmp/"+srcSession, srcSession, "2026-01-01T00:00:00Z", `{}`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`insert or ignore into entries(session_key,entry_id,parent_id,line_no,entry_type,timestamp,role,entry_sha256,raw_json,source_metadata_json) values(?,?,?,?,?,?,?,?,?,?)`,
		sk, entryID, "", ordinal, "assistant", ts, "assistant", "h-"+srcSession+"-"+entryID, `{}`, `{}`); err != nil {
		t.Fatal(err)
	}
	var exit any
	if _, err := db.Exec(`insert into tool_invocations(session_key,entry_id,tool_key,tool_use_id,ordinal,tool_name,command_family,command,exit_code,is_error,error_signature,outcome_text,timestamp,project_key,machine_id) values(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		sk, entryID, "#000", "", ordinal, tool, family, family, exit, boolInt(isError), sig, sig, ts, srcSession, "m"); err != nil {
		t.Fatal(err)
	}
}

// TestSkillCandidatesWorkedExample pins the canonical Confluent case from
// docs/outcome-weighting-spec.md: six failure episodes of the same cluster,
// four resolved, two abandoned — and the outcome-weighted winner is the
// two-step fix taken in three sessions, not the over-specified variant.
func TestSkillCandidatesWorkedExample(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	const (
		fam = "confluent topic describe" // the failing command family F
		a   = "config set mechanism"
		b   = "config set jaas"
		c   = "config set protocol"
		sig = "unknown topic or partition"
	)
	tool := "Bash"

	// Episode 1: retry-only, never resolves -> abandoned.
	seedToolInvocation(t, store.DB, "s1", "e1", 0, tool, fam, true, sig, "2026-01-01T00:00:00Z")
	seedToolInvocation(t, store.DB, "s1", "e2", 1, tool, fam, true, sig, "2026-01-01T00:00:10Z")

	// Episodes 2,4,6: the two-step fix -> resolved, path [a,b,fam].
	for _, s := range []string{"s2", "s4", "s6"} {
		seedToolInvocation(t, store.DB, s, "e1", 0, tool, fam, true, sig, "2026-01-01T00:00:00Z")
		seedToolInvocation(t, store.DB, s, "e2", 1, tool, a, false, "", "2026-01-01T00:00:01Z")
		seedToolInvocation(t, store.DB, s, "e3", 2, tool, b, false, "", "2026-01-01T00:00:02Z")
		seedToolInvocation(t, store.DB, s, "e4", 3, tool, fam, false, "", "2026-01-01T00:00:03Z")
	}

	// Episode 3: three-step variant -> resolved, path [a,b,c,fam].
	seedToolInvocation(t, store.DB, "s3", "e1", 0, tool, fam, true, sig, "2026-01-01T00:00:00Z")
	seedToolInvocation(t, store.DB, "s3", "e2", 1, tool, a, false, "", "2026-01-01T00:00:01Z")
	seedToolInvocation(t, store.DB, "s3", "e3", 2, tool, b, false, "", "2026-01-01T00:00:02Z")
	seedToolInvocation(t, store.DB, "s3", "e4", 3, tool, c, false, "", "2026-01-01T00:00:03Z")
	seedToolInvocation(t, store.DB, "s3", "e5", 4, tool, fam, false, "", "2026-01-01T00:00:04Z")

	// Episode 5: switched away to a different family, fam never succeeds -> abandoned.
	seedToolInvocation(t, store.DB, "s5", "e1", 0, tool, fam, true, sig, "2026-01-01T00:00:00Z")
	seedToolInvocation(t, store.DB, "s5", "e2", 1, tool, "kafka local up", false, "", "2026-01-01T00:00:01Z")

	if err := backfillFailureEpisodes(store.DB); err != nil {
		t.Fatal(err)
	}

	candidates, err := SkillCandidates(store.DB, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 {
		t.Fatalf("want exactly one skill candidate (the resolved cluster), got %d: %+v", len(candidates), candidates)
	}
	cand := candidates[0]
	if cand.CommandFamily != fam || cand.ErrorSignature != sig {
		t.Fatalf("unexpected candidate identity: %+v", cand)
	}
	if cand.Episodes != 6 {
		t.Fatalf("want 6 episodes, got %d", cand.Episodes)
	}
	if cand.Resolved != 4 {
		t.Fatalf("want 4 resolved episodes, got %d", cand.Resolved)
	}
	if cand.ResolutionRate < 0.66 || cand.ResolutionRate > 0.67 {
		t.Fatalf("want resolution_rate ~0.667, got %v", cand.ResolutionRate)
	}
	if cand.Tier != "established" {
		t.Fatalf("4 resolved across 4 sessions should be established, got %q", cand.Tier)
	}
	if len(cand.Paths) < 2 {
		t.Fatalf("want both the two-step and three-step paths surfaced, got %+v", cand.Paths)
	}
	top := cand.Paths[0]
	wantTop := []string{a, b, fam}
	if fmt.Sprint(top.Families) != fmt.Sprint(wantTop) {
		t.Fatalf("top path should be the two-step fix %v, got %v", wantTop, top.Families)
	}
	if top.Support != 3 {
		t.Fatalf("top path support should be 3, got %d", top.Support)
	}
	// Outcome-weighting, not raw rate: the 1/1 variant must rank below the 3/4 path.
	if top.Confidence <= cand.Paths[1].Confidence {
		t.Fatalf("two-step fix (3/4) should outrank the variant (1/4): %v vs %v", top.Confidence, cand.Paths[1].Confidence)
	}
	if top.SampleRef == "" {
		t.Fatalf("top path should carry a sample_ref to a resolving success")
	}
	if _, err := model.ParseRef(top.SampleRef); err != nil {
		t.Fatalf("sample_ref must be a valid ref: %v", err)
	}
}

// TestUnresolvedClusterIsNotASkillCandidate proves a pure pain-point (no
// resolved episode) is excluded — that is the Clusters surface's job, not this.
func TestUnresolvedClusterIsNotASkillCandidate(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	seedToolInvocation(t, store.DB, "s1", "e1", 0, "Bash", "git push", true, "rejected", "2026-01-01T00:00:00Z")
	seedToolInvocation(t, store.DB, "s2", "e1", 0, "Bash", "git push", true, "rejected", "2026-01-01T00:00:00Z")
	if err := backfillFailureEpisodes(store.DB); err != nil {
		t.Fatal(err)
	}
	candidates, err := SkillCandidates(store.DB, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 0 {
		t.Fatalf("an unresolved cluster must not be a skill candidate: %+v", candidates)
	}
}

// TestFailureEpisodesAreAppendOnly proves the no-update / no-delete triggers
// and the resolved-shape CHECK hold, making invalid rows unrepresentable.
func TestFailureEpisodesAreAppendOnly(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	// A real entry to satisfy the require_entry trigger / FK.
	seedToolInvocation(t, store.DB, "s1", "e1", 0, "Bash", "git push", true, "rejected", "2026-01-01T00:00:00Z")
	key, _ := model.NewSessionKey("claude-code", "m", "s1")
	sk := key.String()

	// A valid abandoned episode inserts fine.
	if _, err := store.DB.Exec(`insert into failure_episodes(session_key,open_entry_id,open_ordinal,tool_name,command_family,error_signature,resolved,resolve_entry_id,resolution_path,project_key,opened_at,resolved_at) values(?,?,?,?,?,?,?,?,?,?,?,?)`,
		sk, "e1", 0, "Bash", "git push", "rejected", 0, nil, nil, "s1", "2026-01-01T00:00:00Z", nil); err != nil {
		t.Fatalf("valid abandoned episode should insert: %v", err)
	}

	// CHECK: an abandoned episode carrying a phantom fix is rejected.
	if _, err := store.DB.Exec(`insert into failure_episodes(session_key,open_entry_id,open_ordinal,resolved,resolution_path,opened_at) values(?,?,?,?,?,?)`,
		sk, "e1", 99, 0, `["x"]`, "2026-01-01T00:00:00Z"); err == nil {
		t.Fatal("CHECK should reject resolved=0 with a resolution_path")
	}
	// CHECK: a resolved episode missing its evidence is rejected.
	if _, err := store.DB.Exec(`insert into failure_episodes(session_key,open_entry_id,open_ordinal,resolved,opened_at) values(?,?,?,?,?)`,
		sk, "e1", 98, 1, "2026-01-01T00:00:00Z"); err == nil {
		t.Fatal("CHECK should reject resolved=1 without resolve_entry_id/resolution_path/resolved_at")
	}
	// Append-only: update and delete are refused.
	if _, err := store.DB.Exec(`update failure_episodes set tool_name='x' where session_key=?`, sk); err == nil {
		t.Fatal("failure_episodes update should be refused")
	}
	if _, err := store.DB.Exec(`delete from failure_episodes where session_key=?`, sk); err == nil {
		t.Fatal("failure_episodes delete should be refused")
	}
}

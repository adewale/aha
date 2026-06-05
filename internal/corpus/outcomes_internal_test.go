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

// TestIncidentsWorkedExample pins the canonical Confluent case from
// docs/outcome-weighting-spec.md: six failure episodes of one cluster, four
// resolved, two abandoned — a partial incident whose outcome-weighted top fix
// is the two-step path taken in three sessions, not the over-specified variant.
func TestIncidentsWorkedExample(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	const (
		fam = "confluent topic describe"
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

	incidents, err := Incidents(store.DB, IncidentFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(incidents) != 1 {
		t.Fatalf("want one incident, got %d: %+v", len(incidents), incidents)
	}
	in := incidents[0]
	if in.CommandFamily != fam || in.ErrorSignature != sig {
		t.Fatalf("unexpected identity: %+v", in)
	}
	if in.Episodes != 6 || in.Resolved != 4 {
		t.Fatalf("want 6 episodes / 4 resolved, got episodes=%d resolved=%d", in.Episodes, in.Resolved)
	}
	if in.State != "partial" {
		t.Fatalf("4 of 6 resolved should be partial, got %q", in.State)
	}
	if in.Tier != "established" {
		t.Fatalf("4 resolved across 4 sessions should be established, got %q", in.Tier)
	}
	if len(in.Paths) < 2 {
		t.Fatalf("want both the two-step and three-step paths, got %+v", in.Paths)
	}
	top := in.Paths[0]
	if fmt.Sprint(top.Families) != fmt.Sprint([]string{a, b, fam}) {
		t.Fatalf("top path should be the two-step fix, got %v", top.Families)
	}
	if top.Support != 3 {
		t.Fatalf("top path support should be 3, got %d", top.Support)
	}
	if top.Confidence <= in.Paths[1].Confidence {
		t.Fatalf("two-step fix (3/4) should outrank the variant (1/4): %v vs %v", top.Confidence, in.Paths[1].Confidence)
	}
	if top.SampleRef == "" {
		t.Fatalf("top path should carry a sample_ref to a resolving success")
	}
	if _, err := model.ParseRef(top.SampleRef); err != nil {
		t.Fatalf("sample_ref must be a valid ref: %v", err)
	}
}

// TestIncidentsTopKAndTieBreak covers top-K truncation (defaultPathsPerCandidate)
// and the multi-path ranking + deterministic tie-break.
func TestIncidentsTopKAndTieBreak(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	const fam = "confluent topic describe"
	const sig = "unknown topic or partition"
	seedPath := func(session, step string) {
		seedToolInvocation(t, store.DB, session, "e1", 0, "Bash", fam, true, sig, "2026-01-01T00:00:00Z")
		seedToolInvocation(t, store.DB, session, "e2", 1, "Bash", step, false, "", "2026-01-01T00:00:01Z")
		seedToolInvocation(t, store.DB, session, "e3", 2, "Bash", fam, false, "", "2026-01-01T00:00:02Z")
	}
	seedPath("s1", "a")
	seedPath("s2", "a")
	seedPath("s3", "a") // path [a,fam] support 3
	seedPath("s4", "b")
	seedPath("s5", "b") // path [b,fam] support 2
	seedPath("s6", "c") // path [c,fam] support 1
	seedPath("s7", "d") // path [d,fam] support 1
	if err := backfillFailureEpisodes(store.DB); err != nil {
		t.Fatal(err)
	}
	incidents, err := Incidents(store.DB, IncidentFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(incidents) != 1 || incidents[0].State != "resolved" {
		t.Fatalf("want one fully-resolved incident, got %+v", incidents)
	}
	paths := incidents[0].Paths
	if len(paths) != defaultPathsPerCandidate {
		t.Fatalf("want top-%d paths, got %d: %+v", defaultPathsPerCandidate, len(paths), paths)
	}
	wantOrder := [][]string{{"a", fam}, {"b", fam}, {"c", fam}}
	for i, want := range wantOrder {
		if fmt.Sprint(paths[i].Families) != fmt.Sprint(want) {
			t.Fatalf("path[%d] = %v, want %v", i, paths[i].Families, want)
		}
	}
	for _, p := range paths {
		if len(p.Families) == 2 && p.Families[0] == "d" {
			t.Fatalf("[d,fam] should have been truncated below [c,fam] by tie-break: %+v", paths)
		}
	}
}

// TestIncidentsLimitClampAndOrdering covers the limit policy and score ordering.
func TestIncidentsLimitClampAndOrdering(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	for _, s := range []string{"s1", "s2"} {
		seedToolInvocation(t, store.DB, s, "e1", 0, "Bash", "big cmd", true, "boom", "2026-01-01T00:00:00Z")
		seedToolInvocation(t, store.DB, s, "e2", 1, "Bash", "big cmd", false, "", "2026-01-01T00:00:01Z")
	}
	seedToolInvocation(t, store.DB, "s3", "e1", 0, "Bash", "small cmd", true, "boom", "2026-01-01T00:00:00Z")
	seedToolInvocation(t, store.DB, "s3", "e2", 1, "Bash", "small cmd", false, "", "2026-01-01T00:00:01Z")
	if err := backfillFailureEpisodes(store.DB); err != nil {
		t.Fatal(err)
	}

	all, err := Incidents(store.DB, IncidentFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 || all[0].CommandFamily != "big cmd" {
		t.Fatalf("default limit should return both, higher-scored first: %+v", all)
	}
	one, err := Incidents(store.DB, IncidentFilter{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(one) != 1 || one[0].CommandFamily != "big cmd" {
		t.Fatalf("limit=1 should keep only the top-scored, got %+v", one)
	}
	clamped, err := Incidents(store.DB, IncidentFilter{Limit: 100000})
	if err != nil {
		t.Fatalf("over-max limit must not error: %v", err)
	}
	if len(clamped) != 2 {
		t.Fatalf("over-max limit should still return all available, got %d", len(clamped))
	}
}

// TestResolutionPathSampleRefBestEffort pins the documented contract: when a
// stored resolve coordinate can't form a ref (defensive — the CHECK guarantees
// non-null, not well-formed), the incident is still returned, with that path's
// SampleRef left empty rather than failing the whole query.
func TestResolutionPathSampleRefBestEffort(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	seedToolInvocation(t, store.DB, "s1", "e1", 0, "Bash", "git push", true, "rejected", "2026-01-01T00:00:00Z")
	key, _ := model.NewSessionKey("claude-code", "m", "s1")
	sk := key.String()
	if _, err := store.DB.Exec(`insert into failure_episodes(session_key,open_entry_id,open_ordinal,tool_name,command_family,error_signature,resolved,resolve_entry_id,resolution_path,project_key,opened_at,resolved_at) values(?,?,?,?,?,?,?,?,?,?,?,?)`,
		sk, "e1", 0, "Bash", "git push", "rejected", 1, "bad\nid", `["git push"]`, "s1", "2026-01-01T00:00:00Z", "2026-01-01T00:00:05Z"); err != nil {
		t.Fatalf("seed resolved episode: %v", err)
	}
	incidents, err := Incidents(store.DB, IncidentFilter{})
	if err != nil {
		t.Fatalf("Incidents must not fail on a malformed resolve coordinate: %v", err)
	}
	if len(incidents) != 1 || len(incidents[0].Paths) != 1 {
		t.Fatalf("incident with one path expected, got %+v", incidents)
	}
	if incidents[0].Paths[0].SampleRef != "" {
		t.Fatalf("malformed coordinate must degrade to empty path SampleRef, got %q", incidents[0].Paths[0].SampleRef)
	}
}

// TestCandidateTierBoundary pins the established/tentative decision boundary
// (resolved>=3 AND sessions>=2), not just the extremes.
func TestCandidateTierBoundary(t *testing.T) {
	cases := []struct {
		resolved, sessions int
		want               string
	}{
		{1, 1, "tentative"},
		{3, 1, "tentative"}, // enough resolved, too few sessions
		{2, 2, "tentative"}, // enough sessions, too few resolved
		{3, 2, "established"},
		{10, 5, "established"},
	}
	for _, c := range cases {
		if got := candidateTier(c.resolved, c.sessions); got != c.want {
			t.Errorf("candidateTier(resolved=%d, sessions=%d) = %q, want %q", c.resolved, c.sessions, got, c.want)
		}
	}
}

// TestFailureEpisodesRecomputedOnResume pins the resume correctness contract:
// failure_episodes is a derived view of tool_invocations, so when a session is
// re-ingested after its resolving success finally arrives, the previously
// abandoned episode must flip to resolved — not stay stale.
func TestFailureEpisodesRecomputedOnResume(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	const fam = "confluent topic describe"
	const sig = "unknown topic or partition"

	// First ingest: only the failing opener exists; no fix yet.
	seedToolInvocation(t, store.DB, "s1", "e1", 0, "Bash", fam, true, sig, "2026-01-01T00:00:00Z")
	if err := backfillFailureEpisodes(store.DB); err != nil {
		t.Fatal(err)
	}
	incidents, err := Incidents(store.DB, IncidentFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(incidents) != 1 || incidents[0].State != "unresolved" {
		t.Fatalf("an unresolved opener should be one unresolved incident: %+v", incidents)
	}

	// Resume: the same command family later succeeds; re-ingest the session.
	seedToolInvocation(t, store.DB, "s1", "e2", 1, "Bash", fam, false, "", "2026-01-01T00:00:05Z")
	if err := backfillFailureEpisodes(store.DB); err != nil {
		t.Fatal(err)
	}
	incidents, err = Incidents(store.DB, IncidentFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(incidents) != 1 || incidents[0].State != "resolved" || incidents[0].Resolved != 1 {
		t.Fatalf("after the fix arrives the episode must flip to resolved: %+v", incidents)
	}
}

// TestFailureEpisodesSchemaRejectsInvalidShape proves the resolved-shape CHECK
// makes an invalid row unrepresentable, and that — because failure_episodes is
// a recomputable derived view, not append-only — a full per-session recompute
// (delete + reinsert) is permitted.
func TestFailureEpisodesSchemaRejectsInvalidShape(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	seedToolInvocation(t, store.DB, "s1", "e1", 0, "Bash", "git push", true, "rejected", "2026-01-01T00:00:00Z")
	key, _ := model.NewSessionKey("claude-code", "m", "s1")
	sk := key.String()

	if _, err := store.DB.Exec(`insert into failure_episodes(session_key,open_entry_id,open_ordinal,tool_name,command_family,error_signature,resolved,resolve_entry_id,resolution_path,project_key,opened_at,resolved_at) values(?,?,?,?,?,?,?,?,?,?,?,?)`,
		sk, "e1", 0, "Bash", "git push", "rejected", 0, nil, nil, "s1", "2026-01-01T00:00:00Z", nil); err != nil {
		t.Fatalf("valid abandoned episode should insert: %v", err)
	}
	if _, err := store.DB.Exec(`insert into failure_episodes(session_key,open_entry_id,open_ordinal,resolved,resolution_path,opened_at) values(?,?,?,?,?,?)`,
		sk, "e1", 99, 0, `["x"]`, "2026-01-01T00:00:00Z"); err == nil {
		t.Fatal("CHECK should reject resolved=0 with a resolution_path")
	}
	if _, err := store.DB.Exec(`insert into failure_episodes(session_key,open_entry_id,open_ordinal,resolved,opened_at) values(?,?,?,?,?)`,
		sk, "e1", 98, 1, "2026-01-01T00:00:00Z"); err == nil {
		t.Fatal("CHECK should reject resolved=1 without resolve_entry_id/resolution_path/resolved_at")
	}
	if _, err := store.DB.Exec(`delete from failure_episodes where session_key=?`, sk); err != nil {
		t.Fatalf("recompute delete should be allowed for a derived view: %v", err)
	}
	if _, err := store.DB.Exec(`insert into failure_episodes(session_key,open_entry_id,open_ordinal,tool_name,command_family,error_signature,resolved,resolve_entry_id,resolution_path,project_key,opened_at,resolved_at) values(?,?,?,?,?,?,?,?,?,?,?,?)`,
		sk, "e1", 0, "Bash", "git push", "rejected", 1, "e1", `["git push"]`, "s1", "2026-01-01T00:00:00Z", "2026-01-01T00:00:05Z"); err != nil {
		t.Fatalf("reinsert after recompute delete should succeed: %v", err)
	}
}

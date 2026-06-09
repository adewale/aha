package corpus

import (
	"fmt"
	"testing"

	"github.com/adewale/aha/internal/model"
)

// TestIncidentsUnifiesFailureAndResolution proves one incident row carries both
// the recurrence (failure) side and the resolution (fix) side, with a derived
// state, so the dashboard no longer needs two overlapping lists.
func TestIncidentsUnifiesFailureAndResolution(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	// Cluster A: recurring and always fixed (Edit fails, Read+Edit fixes).
	for _, s := range []string{"a1", "a2", "a3"} {
		seedToolInvocation(t, store.DB, s, "e1", 0, "Edit", "Edit", true, "tool_error", "2026-01-01T00:00:00Z")
		seedToolInvocation(t, store.DB, s, "e2", 1, "Read", "Read", false, "", "2026-01-01T00:00:01Z")
		seedToolInvocation(t, store.DB, s, "e3", 2, "Edit", "Edit", false, "", "2026-01-01T00:00:02Z")
	}
	// Cluster B: recurring but NEVER fixed (git push rejected, no success).
	seedToolInvocation(t, store.DB, "b1", "e1", 0, "Bash", "git push", true, "rejected", "2026-02-01T00:00:00Z")
	seedToolInvocation(t, store.DB, "b2", "e1", 0, "Bash", "git push", true, "rejected", "2026-02-02T00:00:00Z")
	if err := backfillFailureEpisodes(store.DB); err != nil {
		t.Fatal(err)
	}

	incidents, err := Incidents(store.DB, IncidentFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(incidents) != 2 {
		t.Fatalf("want two incidents (one resolved, one unresolved), got %d: %+v", len(incidents), incidents)
	}
	byFamily := map[string]Incident{}
	for _, in := range incidents {
		byFamily[in.CommandFamily] = in
	}
	edit := byFamily["Edit"]
	if edit.State != "resolved" || edit.Resolved != 3 || edit.Episodes != 3 {
		t.Fatalf("Edit incident should be fully resolved 3/3: %+v", edit)
	}
	if len(edit.Paths) == 0 || edit.SampleRef == "" {
		t.Fatalf("resolved incident must carry fix paths + a sample ref: %+v", edit)
	}
	push := byFamily["git push"]
	if push.State != "unresolved" || push.Resolved != 0 || push.Episodes != 2 {
		t.Fatalf("git push incident should be unresolved 0/2: %+v", push)
	}
	if push.Tier != "" {
		t.Fatalf("unresolved incident should have no tier, got %q", push.Tier)
	}
	if push.SampleRef == "" {
		t.Fatalf("even an unresolved incident must drill into a failing opener: %+v", push)
	}
	if _, err := model.ParseRef(push.SampleRef); err != nil {
		t.Fatalf("unresolved sample_ref must be a valid ref: %v", err)
	}
	// Every incident carries a sparkline of the fixed bucket length.
	for _, in := range incidents {
		if len(in.Spark) != SparkBuckets {
			t.Fatalf("incident %q spark length = %d, want %d", in.CommandFamily, len(in.Spark), SparkBuckets)
		}
	}
}

// TestIncidentsPartialState covers the partially-resolved classification.
func TestIncidentsPartialState(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	// Two episodes of the same cluster: one resolves, one abandons.
	seedToolInvocation(t, store.DB, "s1", "e1", 0, "Bash", "flaky cmd", true, "boom", "2026-01-01T00:00:00Z")
	seedToolInvocation(t, store.DB, "s1", "e2", 1, "Bash", "flaky cmd", false, "", "2026-01-01T00:00:01Z")
	seedToolInvocation(t, store.DB, "s2", "e1", 0, "Bash", "flaky cmd", true, "boom", "2026-01-02T00:00:00Z")
	if err := backfillFailureEpisodes(store.DB); err != nil {
		t.Fatal(err)
	}
	incidents, err := Incidents(store.DB, IncidentFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(incidents) != 1 {
		t.Fatalf("want one incident, got %d", len(incidents))
	}
	if incidents[0].State != "partial" || incidents[0].Resolved != 1 || incidents[0].Episodes != 2 {
		t.Fatalf("want partial 1/2, got %+v", incidents[0])
	}
}

// TestIncidentsFacetFilters covers project / source / machine / tool scoping.
func TestIncidentsFacetFilters(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	// seedToolInvocation sets project_key = source session id; both sessions are
	// source claude-code, machine m. Differentiate by project (s-alpha/s-beta).
	seedToolInvocation(t, store.DB, "s-alpha", "e1", 0, "Bash", "alpha cmd", true, "x", "2026-01-01T00:00:00Z")
	seedToolInvocation(t, store.DB, "s-beta", "e1", 0, "Bash", "beta cmd", true, "y", "2026-01-01T00:00:00Z")
	if err := backfillFailureEpisodes(store.DB); err != nil {
		t.Fatal(err)
	}

	all, err := Incidents(store.DB, IncidentFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("unfiltered should see both, got %d", len(all))
	}
	scoped, err := Incidents(store.DB, IncidentFilter{Project: "s-alpha"})
	if err != nil {
		t.Fatal(err)
	}
	if len(scoped) != 1 || scoped[0].CommandFamily != "alpha cmd" {
		t.Fatalf("project filter should isolate alpha, got %+v", scoped)
	}
	bySource, err := Incidents(store.DB, IncidentFilter{Source: "claude-code"})
	if err != nil {
		t.Fatal(err)
	}
	if len(bySource) != 2 {
		t.Fatalf("source filter claude-code should see both, got %d", len(bySource))
	}
	none, err := Incidents(store.DB, IncidentFilter{Machine: "no-such-machine"})
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 {
		t.Fatalf("unknown machine should yield nothing, got %+v", none)
	}
}

// TestIncidentsStateFilterAppliesBeforeLimit proves state is a corpus-side
// filter, not a client-side trim of the first mixed-state page.
func TestIncidentsStateFilterAppliesBeforeLimit(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	// The resolved incident has the higher score and would win limit=1 without
	// the state predicate being applied before pagination.
	for _, s := range []string{"resolved-a", "resolved-b", "resolved-c"} {
		seedToolInvocation(t, store.DB, s, "e1", 0, "Bash", "high score resolved", true, "boom", "2026-01-01T00:00:00Z")
		seedToolInvocation(t, store.DB, s, "e2", 1, "Bash", "high score resolved", false, "", "2026-01-01T00:00:01Z")
	}
	seedToolInvocation(t, store.DB, "unresolved-a", "e1", 0, "Bash", "lower score unresolved", true, "boom", "2026-01-02T00:00:00Z")
	if err := backfillFailureEpisodes(store.DB); err != nil {
		t.Fatal(err)
	}

	unresolved, err := Incidents(store.DB, IncidentFilter{State: "unresolved", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(unresolved) != 1 || unresolved[0].State != "unresolved" || unresolved[0].CommandFamily != "lower score unresolved" {
		t.Fatalf("state filter must run before limit, got %+v", unresolved)
	}
}

// TestIncidentsResolutionPathsRespectFacetFilters proves a scoped incident row
// cannot borrow fix paths, confidence, or sample refs from another facet that
// shares the same tool/family/error identity.
func TestIncidentsResolutionPathsRespectFacetFilters(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	seedToolInvocation(t, store.DB, "s-alpha", "e1", 0, "Bash", "deploy", true, "boom", "2026-01-01T00:00:00Z")
	seedToolInvocation(t, store.DB, "s-alpha", "e2", 1, "Bash", "alpha-fix", false, "", "2026-01-01T00:00:01Z")
	seedToolInvocation(t, store.DB, "s-alpha", "e3", 2, "Bash", "deploy", false, "", "2026-01-01T00:00:02Z")
	seedToolInvocation(t, store.DB, "s-beta", "e1", 0, "Bash", "deploy", true, "boom", "2026-01-01T00:00:00Z")
	seedToolInvocation(t, store.DB, "s-beta", "e2", 1, "Bash", "beta-fix", false, "", "2026-01-01T00:00:01Z")
	seedToolInvocation(t, store.DB, "s-beta", "e3", 2, "Bash", "deploy", false, "", "2026-01-01T00:00:02Z")
	if err := backfillFailureEpisodes(store.DB); err != nil {
		t.Fatal(err)
	}

	alpha, err := Incidents(store.DB, IncidentFilter{Project: "s-alpha"})
	if err != nil {
		t.Fatal(err)
	}
	if len(alpha) != 1 || alpha[0].Projects != 1 || alpha[0].Resolved != 1 {
		t.Fatalf("project-scoped incident should contain only alpha's episode: %+v", alpha)
	}
	if len(alpha[0].Paths) != 1 {
		t.Fatalf("project-scoped paths must not include beta's fix path: %+v", alpha[0].Paths)
	}
	families := fmt.Sprint(alpha[0].Paths[0].Families)
	if families != fmt.Sprint([]string{"alpha-fix", "deploy"}) {
		t.Fatalf("project-scoped path = %s, want only alpha fix", families)
	}
}

// TestIncidentTrajectoryReconstructsArc proves the fail→fix arc is rebuilt with
// a ref per step, from a resolving-success ref.
func TestIncidentTrajectoryReconstructsArc(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	seedToolInvocation(t, store.DB, "s1", "e1", 0, "Bash", "deploy", true, "boom", "2026-01-01T00:00:00Z")
	seedToolInvocation(t, store.DB, "s1", "e2", 1, "Read", "Read", false, "", "2026-01-01T00:00:01Z")
	seedToolInvocation(t, store.DB, "s1", "e3", 2, "Bash", "deploy", false, "", "2026-01-01T00:00:02Z")
	if err := backfillFailureEpisodes(store.DB); err != nil {
		t.Fatal(err)
	}
	incidents, err := Incidents(store.DB, IncidentFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(incidents) != 1 || incidents[0].SampleRef == "" {
		t.Fatalf("expected one resolved incident with a sample ref: %+v", incidents)
	}
	steps, err := IncidentTrajectory(store.DB, incidents[0].SampleRef, &incidents[0].Paths[0].SampleOrdinal)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 3 {
		t.Fatalf("trajectory should be opener→intervening→resolver (3 steps), got %d: %+v", len(steps), steps)
	}
	if !steps[0].IsError {
		t.Fatalf("first step should be the failing opener: %+v", steps[0])
	}
	if steps[len(steps)-1].IsError {
		t.Fatalf("last step should be the resolving success: %+v", steps[len(steps)-1])
	}
	wantFamilies := []string{"deploy", "Read", "deploy"}
	for i, w := range wantFamilies {
		if steps[i].Family != w {
			t.Fatalf("step[%d].Family=%q want %q", i, steps[i].Family, w)
		}
		if steps[i].Ref == "" {
			t.Fatalf("step[%d] missing ref", i)
		}
	}
}

// TestIncidentTrajectoryDisambiguatesSharedResolvingEntry covers entries with
// multiple tool calls: a message ref alone identifies the transcript entry, so
// the incident path also carries the resolving invocation ordinal to select the
// exact fail→fix arc.
func TestIncidentTrajectoryDisambiguatesSharedResolvingEntry(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	seedToolInvocation(t, store.DB, "s1", "open", 0, "Bash", "cmd a", true, "boom-a", "2026-01-01T00:00:00Z")
	seedToolInvocation(t, store.DB, "s1", "open", 1, "Bash", "cmd b", true, "boom-b", "2026-01-01T00:00:00Z")
	seedToolInvocation(t, store.DB, "s1", "resolve", 0, "Bash", "cmd a", false, "", "2026-01-01T00:00:01Z")
	seedToolInvocation(t, store.DB, "s1", "resolve", 1, "Bash", "cmd b", false, "", "2026-01-01T00:00:01Z")
	if err := backfillFailureEpisodes(store.DB); err != nil {
		t.Fatal(err)
	}

	incidents, err := Incidents(store.DB, IncidentFilter{})
	if err != nil {
		t.Fatal(err)
	}
	byFamily := map[string]Incident{}
	for _, in := range incidents {
		byFamily[in.CommandFamily] = in
	}
	for _, family := range []string{"cmd a", "cmd b"} {
		in := byFamily[family]
		if len(in.Paths) != 1 || in.Paths[0].SampleRef == "" {
			t.Fatalf("%s incident missing path sample: %+v", family, in)
		}
		steps, err := IncidentTrajectory(store.DB, in.Paths[0].SampleRef, &in.Paths[0].SampleOrdinal)
		if err != nil {
			t.Fatalf("trajectory for %s: %v", family, err)
		}
		if len(steps) == 0 || steps[0].Family != family || steps[len(steps)-1].Family != family {
			t.Fatalf("trajectory for %s selected the wrong arc: %+v", family, steps)
		}
	}
	if byFamily["cmd a"].Paths[0].SampleRef != byFamily["cmd b"].Paths[0].SampleRef {
		t.Fatalf("test setup expected shared resolving entry refs: a=%q b=%q", byFamily["cmd a"].Paths[0].SampleRef, byFamily["cmd b"].Paths[0].SampleRef)
	}
	if byFamily["cmd a"].Paths[0].SampleOrdinal == byFamily["cmd b"].Paths[0].SampleOrdinal {
		t.Fatalf("sample ordinals must disambiguate shared ref: a=%d b=%d", byFamily["cmd a"].Paths[0].SampleOrdinal, byFamily["cmd b"].Paths[0].SampleOrdinal)
	}
	if _, err := IncidentTrajectory(store.DB, byFamily["cmd a"].Paths[0].SampleRef, nil); err == nil {
		t.Fatal("ambiguous resolving entry without ordinal should fail instead of returning an arbitrary arc")
	}
}

// TestCorpusOverviewComposition covers the orientation summary.
func TestCorpusOverviewComposition(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	seedToolInvocation(t, store.DB, "s1", "e1", 0, "Bash", "x", true, "boom", "2026-01-01T00:00:00Z")
	seedToolInvocation(t, store.DB, "s2", "e1", 0, "Bash", "y", true, "boom", "2026-03-01T00:00:00Z")
	// seedToolInvocation stamps a constant session started_at; give the two
	// sessions distinct starts so the min/max span is exercised.
	if _, err := store.DB.Exec(`update sessions set started_at=? where source_session_id=?`, "2026-03-01T00:00:00Z", "s2"); err != nil {
		t.Fatal(err)
	}
	o, err := CorpusOverview(store.DB)
	if err != nil {
		t.Fatal(err)
	}
	if o.Sessions != 2 || o.Entries != 2 || o.ToolCalls != 2 {
		t.Fatalf("unexpected counts: %+v", o)
	}
	if len(o.Sources) != 1 || o.Sources[0].Name != "claude-code" || o.Sources[0].Count != 2 {
		t.Fatalf("sources breakdown wrong: %+v", o.Sources)
	}
	if o.FirstSession == "" || o.LastSession == "" || o.FirstSession >= o.LastSession {
		t.Fatalf("time span wrong: first=%q last=%q", o.FirstSession, o.LastSession)
	}
	if o.IndexSizeBytes <= 0 {
		t.Fatalf("index size should be positive")
	}
}

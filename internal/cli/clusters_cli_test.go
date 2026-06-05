package cli_test

import (
	"bytes"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adewale/aha/internal/cli"
	"github.com/adewale/aha/internal/corpus"
	"github.com/adewale/aha/internal/model"
)

// TestClustersWithFixesSurfacesResolutionPaths drives the full read-only CLI
// path: a corpus mined from raw entries yields a resolved skill candidate, and
// `aha clusters --with-fixes --json` returns it with its resolution path.
// seedResolvedClusterCorpus builds a corpus at dir whose only session is a
// fail→same-family-success arc, then forces a migration rebuild so
// failure_episodes is populated from raw entries. Returns the corpus dir.
func seedResolvedClusterCorpus(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "corpus")
	store, err := corpus.Open(dir)
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
	for _, st := range []string{`drop table failure_episodes`, `drop table tool_invocations`, `delete from schema_migrations where version in (13,14,15)`} {
		if _, err := store.DB.Exec(st); err != nil {
			t.Fatal(err)
		}
	}
	store.Close()
	return dir
}

func TestClustersWithFixesSurfacesResolutionPaths(t *testing.T) {
	dir := seedResolvedClusterCorpus(t)

	var out bytes.Buffer
	if err := cli.Run([]string{"clusters", "--repo", dir, "--with-fixes", "--json"}, &out, io.Discard); err != nil {
		t.Fatalf("clusters --with-fixes: %v", err)
	}
	var candidates []corpus.SkillCandidate
	if err := json.Unmarshal(out.Bytes(), &candidates); err != nil {
		t.Fatalf("output is not a SkillCandidate array: %v\n%s", err, out.String())
	}
	if len(candidates) != 1 {
		t.Fatalf("want one candidate, got %d: %s", len(candidates), out.String())
	}
	c := candidates[0]
	if c.CommandFamily != "confluent topic describe" || c.Resolved != 1 {
		t.Fatalf("unexpected candidate: %+v", c)
	}
	if len(c.Paths) != 1 || len(c.Paths[0].Families) == 0 {
		t.Fatalf("expected a resolution path with families: %+v", c.Paths)
	}

	// Without --with-fixes the surface is the failure-cluster list (different shape).
	var plain bytes.Buffer
	if err := cli.Run([]string{"clusters", "--repo", dir, "--json"}, &plain, io.Discard); err != nil {
		t.Fatalf("clusters: %v", err)
	}
	if strings.Contains(plain.String(), "resolution_rate") {
		t.Fatalf("plain clusters output must not carry skill-candidate fields: %s", plain.String())
	}
}

// TestClustersWithFixesHumanOutput exercises the non-JSON rendering path: the
// candidate header line and the indented per-path "fix conf=… ref=…" lines.
func TestClustersWithFixesHumanOutput(t *testing.T) {
	dir := seedResolvedClusterCorpus(t)

	var out bytes.Buffer
	if err := cli.Run([]string{"clusters", "--repo", dir, "--with-fixes"}, &out, io.Discard); err != nil {
		t.Fatalf("clusters --with-fixes (human): %v", err)
	}
	got := out.String()
	for _, want := range []string{"tentative", "resolved 1/1", "confluent topic describe", "fix conf=", "ref=msg:"} {
		if !strings.Contains(got, want) {
			t.Fatalf("human --with-fixes output missing %q:\n%s", want, got)
		}
	}
}

// TestClustersWithFixesEmptyHumanOutput covers the empty-corpus human branch.
func TestClustersWithFixesEmptyHumanOutput(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "corpus")
	store, err := corpus.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	store.Close()

	var out bytes.Buffer
	if err := cli.Run([]string{"clusters", "--repo", dir, "--with-fixes"}, &out, io.Discard); err != nil {
		t.Fatalf("clusters --with-fixes on empty corpus: %v", err)
	}
	if !strings.Contains(out.String(), "no resolved error clusters found") {
		t.Fatalf("expected empty-corpus message, got: %q", out.String())
	}
}

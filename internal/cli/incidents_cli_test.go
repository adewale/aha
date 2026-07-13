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

// seedResolvedIncidentCorpus builds a corpus at dir whose only session is a
// fail→same-family-success arc, then forces a migration rebuild so
// failure_episodes is populated from raw entries. Returns the corpus dir.
func seedResolvedIncidentCorpus(t *testing.T) string {
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
	for _, st := range []string{`drop table failure_episodes`, `drop table tool_invocations`, `delete from schema_migrations where version in (13,14)`} {
		if _, err := store.DB.Exec(st); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = corpus.OpenExisting(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestIncidentsCLIJSON(t *testing.T) {
	dir := seedResolvedIncidentCorpus(t)

	var out bytes.Buffer
	if err := cli.Run([]string{"analyse", "failures", "--workspace", dir, "--config", filepath.Join(t.TempDir(), "missing.jsonc"), "--json"}, &out, io.Discard); err != nil {
		t.Fatalf("analyse failures --json: %v", err)
	}
	var incidents []corpus.Incident
	if err := json.Unmarshal(out.Bytes(), &incidents); err != nil {
		t.Fatalf("output is not an Incident array: %v\n%s", err, out.String())
	}
	if len(incidents) != 1 {
		t.Fatalf("want one incident, got %d: %s", len(incidents), out.String())
	}
	in := incidents[0]
	if in.CommandFamily != "confluent topic describe" || in.State != "resolved" || len(in.Paths) != 1 {
		t.Fatalf("unexpected incident: %+v", in)
	}
}

func TestIncidentsCLIHumanOutput(t *testing.T) {
	dir := seedResolvedIncidentCorpus(t)

	var out bytes.Buffer
	if err := cli.Run([]string{"analyse", "failures", "--workspace", dir, "--config", filepath.Join(t.TempDir(), "missing.jsonc")}, &out, io.Discard); err != nil {
		t.Fatalf("analyse failures (human): %v", err)
	}
	got := out.String()
	for _, want := range []string{"resolved", "confluent topic describe", "fix conf=", "ref=msg:"} {
		if !strings.Contains(got, want) {
			t.Fatalf("human incidents output missing %q:\n%s", want, got)
		}
	}
}

func TestIncidentsCLIStateFilter(t *testing.T) {
	dir := seedResolvedIncidentCorpus(t)

	// The only incident is resolved; filtering to unresolved yields nothing.
	var out bytes.Buffer
	if err := cli.Run([]string{"analyse", "failures", "--workspace", dir, "--config", filepath.Join(t.TempDir(), "missing.jsonc"), "--state", "unresolved"}, &out, io.Discard); err != nil {
		t.Fatalf("analyse failures --state unresolved: %v", err)
	}
	if !strings.Contains(out.String(), "no recurring failures found") {
		t.Fatalf("unresolved filter should be empty here, got: %q", out.String())
	}
}

func TestIncidentsCLIEmptyCorpus(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "corpus")
	store, err := corpus.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	store.Close()

	var out bytes.Buffer
	if err := cli.Run([]string{"analyse", "failures", "--workspace", dir, "--config", filepath.Join(t.TempDir(), "missing.jsonc")}, &out, io.Discard); err != nil {
		t.Fatalf("analyse failures on empty Workspace: %v", err)
	}
	if !strings.Contains(out.String(), "no recurring failures found") {
		t.Fatalf("expected empty message, got: %q", out.String())
	}
}

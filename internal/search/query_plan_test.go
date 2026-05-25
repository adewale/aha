package search_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/adewale/aha/internal/corpus"
	"github.com/adewale/aha/internal/model"
	"github.com/adewale/aha/internal/search"
)

func TestProjectFilterHasIndexAvailable(t *testing.T) {
	store := buildSearchBenchCorpus(t, 3, 2)
	defer store.Close()
	rows, err := store.DB.Query(`explain query plan select session_key from sessions where project_key=?`, "project-001")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan strings.Builder
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatal(err)
		}
		plan.WriteString(detail)
		plan.WriteByte('\n')
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.String(), "idx_sessions_project") {
		t.Fatalf("project filter plan does not use idx_sessions_project:\n%s", plan.String())
	}
}

func TestPathTokenFilterHasIndexAvailable(t *testing.T) {
	store := buildSearchBenchCorpus(t, 3, 2)
	defer store.Close()
	rows, err := store.DB.Query(`explain query plan select session_key from session_path_tokens where token=?`, "project-001")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan strings.Builder
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatal(err)
		}
		plan.WriteString(detail)
		plan.WriteByte('\n')
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.String(), "idx_session_path_tokens_token_session") {
		t.Fatalf("path-token filter plan does not use token index:\n%s", plan.String())
	}
}

func TestPathTokenFilterFindsIndexedPathSegments(t *testing.T) {
	store := buildSearchBenchCorpus(t, 3, 2)
	defer store.Close()
	results, err := search.Query(store.DB, "bench", search.Filters{PathToken: "project-001", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("path-token filter returned no results")
	}
	for _, r := range results {
		if r.Project != "/bench/project-001" && r.Project != "project-001" {
			t.Fatalf("unexpected project/path for token result: %+v", r)
		}
	}
}

func TestArtifactPathTokenFilterFindsIndexedPathSegments(t *testing.T) {
	store, err := corpus.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	sha := fmt.Sprintf("%064x", 42)
	sessionKey, err := model.NewSessionKey("pi", "machine", "artifact-parent")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.Exec(`insert into bundles(bundle_id,bundle_sha256,machine_id,captured_at,ingested_at,manifest_json) values(?,?,?,?,?,?)`, "bundle", sha, "machine", "2026-01-01T00:00:00Z", "2026-01-01T00:00:01Z", `{}`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.Exec(`insert into sessions(session_key,source_name,source_session_id,machine_id,raw_cwd,project_key,started_at,source_metadata_json) values(?,?,?,?,?,?,?,?)`, sessionKey.String(), "pi", "artifact-parent", "machine", "/repo/project", "project", "2026-01-01T00:00:00Z", `{}`); err != nil {
		t.Fatal(err)
	}
	res, err := store.DB.Exec(`insert into artifacts(artifact_sha256,source_name,machine_id,bundle_id,kind,parent_session_key,raw_path,relative_path,text_preview,text_body) values(?,?,?,?,?,?,?,?,?,?)`, sha, "pi", "machine", "bundle", "artifact", sessionKey.String(), "/repo/project/artifact-target/file.txt", "artifact-target/file.txt", "artifactneedle", "artifactneedle")
	if err != nil {
		t.Fatal(err)
	}
	artifactID, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.Exec(`insert into artifact_path_tokens(artifact_id,token) values(?,?)`, artifactID, "artifact-target"); err != nil {
		t.Fatal(err)
	}
	results, err := search.Query(store.DB, "artifactneedle", search.Filters{Role: "artifact", PathToken: "artifact-target", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Role != "artifact" {
		t.Fatalf("artifact path-token results: %+v", results)
	}
}

func TestSearchLimitIsCapped(t *testing.T) {
	store := buildSearchBenchCorpus(t, 5, 100)
	defer store.Close()
	results, err := search.Query(store.DB, "bench", search.Filters{Limit: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) > 200 {
		t.Fatalf("results=%d, want capped at 200", len(results))
	}
}

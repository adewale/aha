package corpus_test

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adewale/aha/internal/adapters"
	"github.com/adewale/aha/internal/archive"
	"github.com/adewale/aha/internal/config"
	"github.com/adewale/aha/internal/corpus"
	"github.com/adewale/aha/internal/model"
	"github.com/adewale/aha/internal/search"
	"github.com/adewale/aha/internal/testutil"
)

func makeBundle(t *testing.T, root string) (string, string) {
	t.Helper()
	fx := testutil.WriteAgentFixtures(t, root)
	cfg := config.Default()
	cfg.MachineID = "test-machine"
	cfg.Sources = []model.SourceConfig{{Type: "pi", Root: fx.PiRoot, Enabled: true}, {Type: "claude-code", Root: fx.ClaudeRoot, Enabled: true}}
	b, err := archive.Capture(t.Context(), cfg, adapters.Builtins(), archive.Options{CapturedAt: "2026-01-03T00:00:00Z", BundleID: "fixed"})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "bundle.tar.zst")
	if _, err := archive.Write(path, b); err != nil {
		t.Fatal(err)
	}
	return path, filepath.Join(root, "corpus")
}

func TestIngestPiIdentityComesFromBundleNotMutableRawPath(t *testing.T) {
	root := t.TempDir()
	fx := testutil.WriteAgentFixtures(t, root)
	bundle := writeBundleFromRoots(t, root, fx, "test-machine", "immutable-pi")
	piFile := filepath.Join(fx.PiRoot, "--Users-me-proj--", "2026_pi.jsonl")
	mutated := `{"type":"session","version":3,"id":"mutated-live-session","timestamp":"2026-01-01T00:00:00Z","cwd":"/tmp/other"}
{"id":"x","type":"user","role":"user","message":{"content":"wrong live file"}}
`
	if err := os.WriteFile(piFile, []byte(mutated), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := corpus.Open(filepath.Join(root, "corpus"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := corpus.IngestBundle(store, adapters.Builtins(), bundle); err != nil {
		t.Fatal(err)
	}
	var sourceID, sessionKey string
	if err := store.DB.QueryRow(`select source_session_id, session_key from sessions where source_name='pi'`).Scan(&sourceID, &sessionKey); err != nil {
		t.Fatal(err)
	}
	if sourceID != "pi-session" || strings.Contains(sessionKey, "mutated-live-session") {
		t.Fatalf("ingest used mutable raw path identity: source_session_id=%q session_key=%q", sourceID, sessionKey)
	}
}

func TestSchemaMigratesOldArtifactTable(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(root, "corpus.db"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`create table artifacts(artifact_id integer primary key,artifact_sha256 text,source_name text,machine_id text,manifest_sha256 text,kind text,parent_session_key text,parent_entry_id text,raw_path text,relative_path text,text_preview text,unique(artifact_sha256,manifest_sha256,relative_path,parent_session_key))`)
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	store, err := corpus.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var found int
	rows, err := store.DB.Query(`pragma table_info(artifacts)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var dflt any
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			t.Fatal(err)
		}
		if name == "text_body" {
			found++
		}
	}
	if found != 1 {
		t.Fatalf("text_body migration not applied")
	}
}

func TestIngestHonorsIncludeImagesFalseForImageArtifacts(t *testing.T) {
	root := t.TempDir()
	fx := testutil.WriteAgentFixtures(t, root)
	gifPath := filepath.Join(fx.PiRoot, "--Users-me-proj--", "subagent-artifacts", "prompt.gif")
	gifBytes, err := os.ReadFile(gifPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fx.PiRoot, "--Users-me-proj--", "subagent-artifacts", "prompt_no_ext"), gifBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.MachineID = "test-machine"
	cfg.IncludeImages = true
	cfg.Sources = []model.SourceConfig{{Type: "pi", Root: fx.PiRoot, Enabled: true}, {Type: "claude-code", Root: fx.ClaudeRoot, Enabled: true}}
	b, err := archive.Capture(t.Context(), cfg, adapters.Builtins(), archive.Options{CapturedAt: "2026-01-03T00:00:00Z", BundleID: "no-images"})
	if err != nil {
		t.Fatal(err)
	}
	b.Manifest.Policy.IncludeImages = false
	bundle := filepath.Join(root, "no-images.tar.zst")
	if _, err := archive.Write(bundle, b); err != nil {
		t.Fatal(err)
	}
	store, err := corpus.Open(filepath.Join(root, "corpus"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := corpus.IngestBundle(store, adapters.Builtins(), bundle); err != nil {
		t.Fatal(err)
	}
	assertCount(t, store.DB, "images", 0)
	var imageArtifacts int
	if err := store.DB.QueryRow(`select count(*) from artifacts where raw_path like '%prompt%'`).Scan(&imageArtifacts); err != nil {
		t.Fatal(err)
	}
	if imageArtifacts != 0 {
		t.Fatalf("image artifact rows=%d want 0", imageArtifacts)
	}
}

func TestIngestCapsLargeArtifactTextBody(t *testing.T) {
	root := t.TempDir()
	fx := testutil.WriteAgentFixtures(t, root)
	large := strings.Repeat("x", int(corpus.MaxArtifactTextIndexBytes)+1024)
	if err := os.WriteFile(filepath.Join(fx.PiRoot, "--Users-me-proj--", "subagent-artifacts", "large.txt"), []byte(large), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.MachineID = "test-machine"
	cfg.Sources = []model.SourceConfig{{Type: "pi", Root: fx.PiRoot, Enabled: true}}
	b, err := archive.Capture(t.Context(), cfg, adapters.Builtins(), archive.Options{CapturedAt: "2026-01-03T00:00:00Z", BundleID: "large-artifact"})
	if err != nil {
		t.Fatal(err)
	}
	bundle := filepath.Join(root, "large.tar.zst")
	if _, err := archive.Write(bundle, b); err != nil {
		t.Fatal(err)
	}
	store, err := corpus.Open(filepath.Join(root, "corpus"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := corpus.IngestBundle(store, adapters.Builtins(), bundle); err != nil {
		t.Fatal(err)
	}
	var n int64
	if err := store.DB.QueryRow(`select length(text_body) from artifacts where raw_path like '%large.txt%'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != corpus.MaxArtifactTextIndexBytes {
		t.Fatalf("text_body length=%d want cap %d", n, corpus.MaxArtifactTextIndexBytes)
	}
}

func TestIngestPopulatesToolInvocationClusters(t *testing.T) {
	root := t.TempDir()
	bundle, corpusDir := makeBundle(t, root)
	store, err := corpus.Open(corpusDir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := corpus.IngestBundle(store, adapters.Builtins(), bundle); err != nil {
		t.Fatal(err)
	}
	// The claude fixture carries one failing `gh pr create` tool call.
	clusters, err := corpus.Incidents(store.DB, corpus.IncidentFilter{})
	if err != nil {
		t.Fatal(err)
	}
	var found *corpus.Incident
	for i := range clusters {
		if clusters[i].CommandFamily == "gh pr create" {
			found = &clusters[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("expected a gh pr create failure incident, got %+v", clusters)
	}
	if found.Episodes != 1 || found.SampleRef == "" {
		t.Fatalf("incident missing episodes/ref: %+v", *found)
	}
	if _, err := model.ParseRef(found.SampleRef); err != nil {
		t.Fatalf("incident sample_ref invalid: %v", err)
	}
}

func TestClustersDoNotExposeRawToolOutputWhenIndexToolOutputFalse(t *testing.T) {
	root := t.TempDir()
	fx := testutil.WriteAgentFixtures(t, root)
	claudePath := filepath.Join(fx.ClaudeRoot, "-Users-me-proj", "abc.jsonl")
	body, err := os.ReadFile(claudePath)
	if err != nil {
		t.Fatal(err)
	}
	secret := "abc123def4567890"
	body = []byte(strings.ReplaceAll(string(body), "pull request create failed: Head sha can't be blank", "Authorization: Bearer "+secret+" failed"))
	if err := os.WriteFile(claudePath, body, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.MachineID = "test-machine"
	cfg.IndexToolOutput = false
	cfg.Sources = []model.SourceConfig{{Type: "claude-code", Root: fx.ClaudeRoot, Enabled: true}}
	b, err := archive.Capture(t.Context(), cfg, adapters.Builtins(), archive.Options{CapturedAt: "2026-01-03T00:00:00Z", BundleID: "secret-cluster"})
	if err != nil {
		t.Fatal(err)
	}
	bundle := filepath.Join(root, "secret.tar.zst")
	if _, err := archive.Write(bundle, b); err != nil {
		t.Fatal(err)
	}
	store, err := corpus.Open(filepath.Join(root, "corpus"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := corpus.IngestBundle(store, adapters.Builtins(), bundle); err != nil {
		t.Fatal(err)
	}
	clusters, err := corpus.Incidents(store.DB, corpus.IncidentFilter{})
	if err != nil {
		t.Fatal(err)
	}
	payload := mustJSON(t, clusters)
	if strings.Contains(payload, secret) || strings.Contains(payload, "Bearer "+secret) {
		t.Fatalf("clusters leaked raw tool output despite index_tool_output=false: %s", payload)
	}
	if strings.Contains(payload, "authorization: bearer") {
		t.Fatalf("clusters exposed raw authorization header shape: %s", payload)
	}
}

func TestIngestIdempotentSearchReadAndImages(t *testing.T) {
	root := t.TempDir()
	bundle, corpusDir := makeBundle(t, root)
	store, err := corpus.Open(corpusDir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	rep, err := corpus.IngestBundle(store, adapters.Builtins(), bundle)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Sessions != 3 || rep.Messages != 7 || rep.Images != 1 || rep.Artifacts != 3 {
		t.Fatalf("bad first ingest: %+v", rep)
	}
	rep, err = corpus.IngestBundle(store, adapters.Builtins(), bundle)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Duplicate {
		t.Fatalf("second ingest should be duplicate: %+v", rep)
	}
	assertCount(t, store.DB, "snapshots", 1)
	assertCount(t, store.DB, "sessions", 3)
	assertCount(t, store.DB, "messages", 7)
	assertCount(t, store.DB, "images", 2)
	assertCount(t, store.DB, "entry_assets", 1)
	var subagents int
	if err := store.DB.QueryRow(`select count(*) from sessions where is_subagent=1 and source_name='claude-code'`).Scan(&subagents); err != nil {
		t.Fatal(err)
	}
	if subagents != 1 {
		t.Fatalf("claude subagent sessions=%d", subagents)
	}
	var parent string
	if err := store.DB.QueryRow(`select parent_session_key from artifacts where parent_session_key is not null`).Scan(&parent); err != nil {
		t.Fatal(err)
	}
	wantParent, err := model.NewSessionKey("pi", "test-machine", "pi-session")
	if err != nil {
		t.Fatal(err)
	}
	if parent != wantParent.String() {
		t.Fatalf("artifact parent=%q", parent)
	}
	var unlinked int
	if err := store.DB.QueryRow(`select count(*) from artifacts where parent_session_key is null`).Scan(&unlinked); err != nil {
		t.Fatal(err)
	}
	if unlinked != 2 {
		t.Fatalf("unlinked artifact count=%d", unlinked)
	}
	var width, height int
	if err := store.DB.QueryRow(`select width,height from images`).Scan(&width, &height); err != nil {
		t.Fatal(err)
	}
	if width != 1 || height != 1 {
		t.Fatalf("image dimensions = %dx%d, want 1x1", width, height)
	}
	results, err := search.Query(store.DB, "needle", search.Filters{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) < 3 {
		t.Fatalf("expected message and artifact hits, got %+v", results)
	}
	ctx, err := corpus.ReadContext(store.DB, "abc", "", 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(ctx) == 0 || !strings.Contains(ctx[0].Text, "claude needle") {
		t.Fatalf("bad read context: %+v", ctx)
	}
	var artifactSHA string
	if err := store.DB.QueryRow(`select artifact_sha256 from artifacts limit 1`).Scan(&artifactSHA); err != nil {
		t.Fatal(err)
	}
	parsedArtifactSHA, err := model.ParseSHA256Hex(artifactSHA)
	if err != nil {
		t.Fatal(err)
	}
	artifactCtx, err := corpus.ReadCanonical(store.DB, model.ArtifactRef{SHA: parsedArtifactSHA}, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(artifactCtx) != 1 || !strings.Contains(artifactCtx[0].Text, "artifact needle") {
		t.Fatalf("bad artifact read context: %+v", artifactCtx)
	}
	unlinkedResults, err := search.Query(store.DB, "unlinked", search.Filters{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(unlinkedResults) != 1 || !strings.HasPrefix(unlinkedResults[0].RefText, "artifact:v1:") {
		t.Fatalf("bad unlinked artifact search: %+v", unlinkedResults)
	}
	unlinkedCtx, err := corpus.ReadCanonical(store.DB, unlinkedResults[0].Ref, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(unlinkedCtx) != 1 || !strings.Contains(unlinkedCtx[0].Text, "unlinked artifact") {
		t.Fatalf("bad unlinked artifact read: %+v", unlinkedCtx)
	}
}

func TestLaterBundleAppendOnlyMerge(t *testing.T) {
	root := t.TempDir()
	fx := testutil.WriteAgentFixtures(t, root)
	corpusDir := filepath.Join(root, "corpus")
	bundle1 := writeBundleFromRoots(t, root, fx, "test-machine", "b1")
	store, err := corpus.Open(corpusDir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := corpus.IngestBundle(store, adapters.Builtins(), bundle1); err != nil {
		t.Fatal(err)
	}
	piFile := filepath.Join(fx.PiRoot, "--Users-me-proj--", "2026_pi.jsonl")
	f, err := os.OpenFile(piFile, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString(`{"id":"p3","parentId":"p2","type":"user","role":"user","timestamp":"2026-01-01T00:00:03Z","message":{"content":"new appended needle"}}` + "\n")
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}
	bundle2 := writeBundleFromRoots(t, root, fx, "test-machine", "b2")
	if _, err := corpus.IngestBundle(store, adapters.Builtins(), bundle2); err != nil {
		t.Fatal(err)
	}
	assertCount(t, store.DB, "sessions", 3)
	var versions int
	if err := store.DB.QueryRow(`select count(*) from session_versions sv join sessions s on s.session_key=sv.session_key where s.source_name='pi' and s.machine_id='test-machine' and s.source_session_id='pi-session'`).Scan(&versions); err != nil {
		t.Fatal(err)
	}
	if versions != 2 {
		t.Fatalf("session_versions=%d want 2", versions)
	}
	var text string
	if err := store.DB.QueryRow(`select text from messages where entry_id='p3'`).Scan(&text); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "new appended") {
		t.Fatalf("appended text missing: %q", text)
	}
}

func TestConflictQuarantineFromBundle(t *testing.T) {
	root := t.TempDir()
	fx := testutil.WriteAgentFixtures(t, root)
	bundle1 := writeBundleFromRoots(t, root, fx, "test-machine", "b1")
	store, err := corpus.Open(filepath.Join(root, "corpus"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := corpus.IngestBundle(store, adapters.Builtins(), bundle1); err != nil {
		t.Fatal(err)
	}
	piFile := filepath.Join(fx.PiRoot, "--Users-me-proj--", "2026_pi.jsonl")
	data, err := os.ReadFile(piFile)
	if err != nil {
		t.Fatal(err)
	}
	changed := strings.Replace(string(data), `"hello needle"`, `"changed conflicting needle"`, 1)
	if err := os.WriteFile(piFile, []byte(changed), 0o644); err != nil {
		t.Fatal(err)
	}
	bundle2 := writeBundleFromRoots(t, root, fx, "test-machine", "b2")
	if _, err := corpus.IngestBundle(store, adapters.Builtins(), bundle2); err != nil {
		t.Fatal(err)
	}
	assertCount(t, store.DB, "conflicts", 1)
	var text string
	if err := store.DB.QueryRow(`select text from messages where entry_id='p1'`).Scan(&text); err != nil {
		t.Fatal(err)
	}
	if text != "hello needle" {
		t.Fatalf("original message overwritten: %q", text)
	}
}

func TestLateToolResultCreatesClusterAfterPendingCall(t *testing.T) {
	root := t.TempDir()
	piRoot := filepath.Join(root, "pi")
	sessionDir := filepath.Join(piRoot, "--Users-me-proj--")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(sessionDir, "session.jsonl")
	callOnly := `{"type":"session","version":3,"id":"late-result","timestamp":"2026-01-01T00:00:00Z","cwd":"/tmp"}
{"id":"call","type":"assistant","timestamp":"2026-01-01T00:00:01Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"tu_late","name":"bash","arguments":{"command":"go test ./..."}}]}}
`
	if err := os.WriteFile(path, []byte(callOnly), 0o644); err != nil {
		t.Fatal(err)
	}
	bundle1 := writePiOnlyBundle(t, root, piRoot, "late-1")
	store, err := corpus.Open(filepath.Join(root, "corpus"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := corpus.IngestBundle(store, adapters.Builtins(), bundle1); err != nil {
		t.Fatal(err)
	}
	assertCount(t, store.DB, "tool_invocations", 0)
	withResult := callOnly + `{"id":"result","type":"user","timestamp":"2026-01-01T00:00:02Z","message":{"role":"toolResult","toolCallId":"tu_late","toolName":"bash","content":[{"type":"text","text":"tests failed"}],"isError":true}}
`
	if err := os.WriteFile(path, []byte(withResult), 0o644); err != nil {
		t.Fatal(err)
	}
	bundle2 := writePiOnlyBundle(t, root, piRoot, "late-2")
	if _, err := corpus.IngestBundle(store, adapters.Builtins(), bundle2); err != nil {
		t.Fatal(err)
	}
	clusters, err := corpus.Incidents(store.DB, corpus.IncidentFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(clusters) != 1 || clusters[0].CommandFamily != "go test" {
		t.Fatalf("late tool result did not produce cluster: %+v", clusters)
	}
}

func TestConflictingEntriesDoNotCreateToolInvocations(t *testing.T) {
	root := t.TempDir()
	piRoot := filepath.Join(root, "pi")
	sessionDir := filepath.Join(piRoot, "--Users-me-proj--")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(sessionDir, "session.jsonl")
	first := `{"type":"session","version":3,"id":"conflict-tools","timestamp":"2026-01-01T00:00:00Z","cwd":"/tmp"}
{"id":"same","type":"user","timestamp":"2026-01-01T00:00:01Z","message":{"content":"original"}}
`
	if err := os.WriteFile(path, []byte(first), 0o644); err != nil {
		t.Fatal(err)
	}
	bundle1 := writePiOnlyBundle(t, root, piRoot, "b1")
	store, err := corpus.Open(filepath.Join(root, "corpus"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := corpus.IngestBundle(store, adapters.Builtins(), bundle1); err != nil {
		t.Fatal(err)
	}
	changed := `{"type":"session","version":3,"id":"conflict-tools","timestamp":"2026-01-01T00:00:00Z","cwd":"/tmp"}
{"id":"same","type":"assistant","timestamp":"2026-01-01T00:00:01Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"tu_conflict","name":"bash","arguments":{"command":"gh pr create --title conflict"}}]}}
{"id":"result","type":"user","timestamp":"2026-01-01T00:00:02Z","message":{"role":"toolResult","toolCallId":"tu_conflict","toolName":"bash","content":[{"type":"text","text":"conflicting new output should not cluster"}],"isError":true}}
`
	if err := os.WriteFile(path, []byte(changed), 0o644); err != nil {
		t.Fatal(err)
	}
	bundle2 := writePiOnlyBundle(t, root, piRoot, "b2")
	if _, err := corpus.IngestBundle(store, adapters.Builtins(), bundle2); err != nil {
		t.Fatal(err)
	}
	assertCount(t, store.DB, "conflicts", 1)
	clusters, err := corpus.Incidents(store.DB, corpus.IncidentFilter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range clusters {
		if c.CommandFamily == "gh pr create" || strings.Contains(c.ErrorSignature, "conflicting") {
			t.Fatalf("conflicting entry produced cluster: %+v", c)
		}
	}
}

func TestFullArtifactTailAndSummaryAreIndexed(t *testing.T) {
	root := t.TempDir()
	fx := testutil.WriteAgentFixtures(t, root)
	artifactPath := filepath.Join(fx.PiRoot, "--Users-me-proj--", "subagent-artifacts", "long_unlinked.md")
	if err := os.WriteFile(artifactPath, []byte(strings.Repeat("x", 5000)+" tailneedle"), 0o644); err != nil {
		t.Fatal(err)
	}
	piFile := filepath.Join(fx.PiRoot, "--Users-me-proj--", "2026_pi.jsonl")
	f, err := os.OpenFile(piFile, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString(`{"type":"summary","summary":"summaryneedle"}` + "\n")
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}
	bundle := writeBundleFromRoots(t, root, fx, "test-machine", "fulltext")
	store, err := corpus.Open(filepath.Join(root, "corpus"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := corpus.IngestBundle(store, adapters.Builtins(), bundle); err != nil {
		t.Fatal(err)
	}
	artifactResults, err := search.Query(store.DB, "tailneedle", search.Filters{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(artifactResults) == 0 {
		t.Fatalf("tail artifact text was not indexed")
	}
	artifactCtx, err := corpus.ReadCanonical(store.DB, artifactResults[0].Ref, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(artifactCtx) != 1 || !strings.Contains(artifactCtx[0].Text, "tailneedle") {
		t.Fatalf("tail artifact read was not coherent: %+v", artifactCtx)
	}
	summaryResults, err := search.Query(store.DB, "summaryneedle", search.Filters{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(summaryResults) == 0 {
		t.Fatalf("summary was not indexed")
	}
}

func TestMalformedDiagnosticsPersistAndToolOutputNotIndexed(t *testing.T) {
	root := t.TempDir()
	fx := testutil.WriteAgentFixtures(t, root)
	piFile := filepath.Join(fx.PiRoot, "--Users-me-proj--", "2026_pi.jsonl")
	f, err := os.OpenFile(piFile, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString("{bad json\n" + `{"id":"tool1","type":"toolResult","role":"toolResult","message":{"content":"secret-tool-output-never-index"}}` + "\n")
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}
	bundle := writeBundleFromRoots(t, root, fx, "test-machine", "diag")
	store, err := corpus.Open(filepath.Join(root, "corpus"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := corpus.IngestBundle(store, adapters.Builtins(), bundle); err != nil {
		t.Fatal(err)
	}
	var metadata string
	if err := store.DB.QueryRow(`select source_metadata_json from sessions where source_name='pi'`).Scan(&metadata); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(metadata, "diagnostics") {
		t.Fatalf("diagnostics missing: %s", metadata)
	}
	results, err := search.Query(store.DB, "secret-tool-output-never-index", search.Filters{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("tool output should not be indexed: %+v", results)
	}
}

func TestOpenExistingDoesNotCreateCorpus(t *testing.T) {
	root := t.TempDir()
	missing := filepath.Join(root, "missing")
	if _, err := corpus.OpenExisting(missing); err == nil {
		t.Fatalf("OpenExisting succeeded for missing corpus")
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("OpenExisting created missing dir: %v", err)
	}
	store, err := corpus.Open(missing)
	if err != nil {
		t.Fatal(err)
	}
	_ = store.Close()
	store, err = corpus.OpenExisting(missing)
	if err != nil {
		t.Fatal(err)
	}
	_ = store.Close()
}

func TestSameBundleIDDifferentContentAreDistinctSnapshots(t *testing.T) {
	root := t.TempDir()
	fx := testutil.WriteAgentFixtures(t, root)
	bundle1 := writeBundleFromRoots(t, root, fx, "test-machine", "same-id")
	store, err := corpus.Open(filepath.Join(root, "corpus"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := corpus.IngestBundle(store, adapters.Builtins(), bundle1); err != nil {
		t.Fatal(err)
	}
	piFile := filepath.Join(fx.PiRoot, "--Users-me-proj--", "2026_pi.jsonl")
	f, err := os.OpenFile(piFile, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString(`{"id":"p99","type":"user","role":"user","message":{"content":"different sha"}}` + "\n")
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}
	bundle2 := writeBundleFromRoots(t, root, fx, "test-machine", "same-id")
	// Snapshot identity is the manifest's content hash, not the human
	// bundle_id: same id with different content is simply two snapshots.
	rep, err := corpus.IngestBundle(store, adapters.Builtins(), bundle2)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Duplicate {
		t.Fatalf("different content reported as duplicate: %+v", rep)
	}
	assertCount(t, store.DB, "snapshots", 2)
}

func TestCrossMachineConflict(t *testing.T) {
	root := t.TempDir()
	fx := testutil.WriteAgentFixtures(t, root)
	bundle1 := writeBundleFromRoots(t, root, fx, "machine-a", "b1")
	store, err := corpus.Open(filepath.Join(root, "corpus"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := corpus.IngestBundle(store, adapters.Builtins(), bundle1); err != nil {
		t.Fatal(err)
	}
	piFile := filepath.Join(fx.PiRoot, "--Users-me-proj--", "2026_pi.jsonl")
	data, err := os.ReadFile(piFile)
	if err != nil {
		t.Fatal(err)
	}
	changed := strings.Replace(string(data), `"hello needle"`, `"other machine divergent"`, 1)
	if err := os.WriteFile(piFile, []byte(changed), 0o644); err != nil {
		t.Fatal(err)
	}
	bundle2 := writeBundleFromRoots(t, root, fx, "machine-b", "b2")
	if _, err := corpus.IngestBundle(store, adapters.Builtins(), bundle2); err != nil {
		t.Fatal(err)
	}
	assertCount(t, store.DB, "conflicts", 1)
}

func writePiOnlyBundle(t *testing.T, root, piRoot, bundleID string) string {
	t.Helper()
	cfg := config.Default()
	cfg.MachineID = "test-machine"
	cfg.Sources = []model.SourceConfig{{Type: "pi", Root: piRoot, Enabled: true}}
	b, err := archive.Capture(t.Context(), cfg, adapters.Builtins(), archive.Options{CapturedAt: "2026-01-03T00:00:00Z", BundleID: bundleID})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, bundleID+".tar.zst")
	if _, err := archive.Write(path, b); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeBundleFromRoots(t *testing.T, root string, fx testutil.FixtureRoots, machine, bundleID string) string {
	t.Helper()
	cfg := config.Default()
	cfg.MachineID = machine
	cfg.Sources = []model.SourceConfig{{Type: "pi", Root: fx.PiRoot, Enabled: true}, {Type: "claude-code", Root: fx.ClaudeRoot, Enabled: true}}
	b, err := archive.Capture(t.Context(), cfg, adapters.Builtins(), archive.Options{CapturedAt: "2026-01-03T00:00:00Z", BundleID: bundleID})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, bundleID+".tar.zst")
	if _, err := archive.Write(path, b); err != nil {
		t.Fatal(err)
	}
	return path
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func assertCount(t *testing.T, db *sql.DB, table string, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow("select count(*) from " + table).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("%s count=%d want %d", table, got, want)
	}
}

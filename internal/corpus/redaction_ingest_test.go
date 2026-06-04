package corpus_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/adewale/aha/internal/adapters"
	"github.com/adewale/aha/internal/archive"
	"github.com/adewale/aha/internal/corpus"
	"github.com/adewale/aha/internal/hash"
	"github.com/adewale/aha/internal/model"
	"github.com/adewale/aha/internal/redact"
)

// TestIngestAppliesRedactionToProjectedText pins the v1.1 contract:
// when the Ingestor is built with a Redactor, every projected text
// surface (messages.text, entries.raw_json, FTS) is redacted before
// it lands, the per-pattern hit counts persist in the redactions
// table, and sessions.redaction_level records the level used. No
// secret survives in any projection.
func TestIngestAppliesRedactionToProjectedText(t *testing.T) {
	sessionID := "redact-secret"
	apiKey := "sk-ant-api03-" + strings.Repeat("a", 30)
	awsKey := "AKIAIOSFODNN7EXAMPL2"
	lines := []string{
		`{"type":"session","version":3,"id":"` + sessionID + `","timestamp":"2026-01-01T00:00:00Z","cwd":"/tmp"}`,
		`{"type":"message","id":"u1","timestamp":"2026-01-01T00:00:01Z","message":{"role":"user","content":[{"type":"text","text":"my api key is ` + apiKey + ` please be careful"}]}}`,
		`{"type":"message","id":"a1","parentId":"u1","timestamp":"2026-01-01T00:00:02Z","message":{"role":"assistant","content":[{"type":"text","text":"and the AWS key is ` + awsKey + ` too"}]}}`,
	}
	store := ingestWithRedaction(t, sessionID, lines, redact.NewDefault(), "v1")
	defer store.Close()

	// messages.text must not contain the secret.
	var combined string
	rows, err := store.DB.Query(`select coalesce(text,'') from messages order by entry_id`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			rows.Close()
			t = ""
		}
		combined += t + "\n"
	}
	rows.Close()
	if strings.Contains(combined, apiKey) {
		t.Fatalf("messages.text contains anthropic secret:\n%s", combined)
	}
	if strings.Contains(combined, awsKey) {
		t.Fatalf("messages.text contains aws secret:\n%s", combined)
	}
	if !strings.Contains(combined, "[REDACTED:anthropic_key]") {
		t.Fatalf("messages.text missing anthropic marker:\n%s", combined)
	}
	if !strings.Contains(combined, "[REDACTED:aws_access_key]") {
		t.Fatalf("messages.text missing aws marker:\n%s", combined)
	}

	// entries.raw_json must not contain the secret either.
	var rawCombined string
	rrows, err := store.DB.Query(`select coalesce(raw_json,'') from entries order by line_no`)
	if err != nil {
		t.Fatal(err)
	}
	for rrows.Next() {
		var s string
		if err := rrows.Scan(&s); err != nil {
			rrows.Close()
			s = ""
		}
		rawCombined += s + "\n"
	}
	rrows.Close()
	if strings.Contains(rawCombined, apiKey) {
		t.Fatalf("entries.raw_json contains anthropic secret")
	}
	if strings.Contains(rawCombined, awsKey) {
		t.Fatalf("entries.raw_json contains aws secret")
	}

	// redactions table records the hits.
	var anthroHits, awsHits int
	if err := store.DB.QueryRow(`select coalesce(sum(count),0) from redactions where pattern='anthropic_key'`).Scan(&anthroHits); err != nil {
		t.Fatal(err)
	}
	if err := store.DB.QueryRow(`select coalesce(sum(count),0) from redactions where pattern='aws_access_key'`).Scan(&awsHits); err != nil {
		t.Fatal(err)
	}
	if anthroHits < 1 {
		t.Fatalf("redactions.anthropic_key count=%d, want >=1", anthroHits)
	}
	if awsHits < 1 {
		t.Fatalf("redactions.aws_access_key count=%d, want >=1", awsHits)
	}

	// sessions.redaction_level stamps the level used at ingest.
	var level string
	if err := store.DB.QueryRow(`select redaction_level from sessions`).Scan(&level); err != nil {
		t.Fatal(err)
	}
	if level != "v1" {
		t.Fatalf("sessions.redaction_level=%q, want %q", level, "v1")
	}
}

// TestRedactedTextDoesNotReachFTS pins that the FTS index — populated
// by triggers from messages.text — sees only the redacted bytes, so
// a search for the raw secret returns zero hits and a search for the
// redaction marker returns the entry. This is the load-bearing
// guarantee for the threat "agent runs aha search → secret leaks
// back via tool-result snippet."
func TestRedactedTextDoesNotReachFTS(t *testing.T) {
	sessionID := "redact-fts"
	apiKey := "sk-ant-api03-" + strings.Repeat("f", 30)
	lines := []string{
		`{"type":"session","version":3,"id":"` + sessionID + `","timestamp":"2026-01-01T00:00:00Z","cwd":"/tmp"}`,
		`{"type":"message","id":"u1","timestamp":"2026-01-01T00:00:01Z","message":{"role":"user","content":[{"type":"text","text":"please check my key ` + apiKey + `"}]}}`,
	}
	store := ingestWithRedaction(t, sessionID, lines, redact.NewDefault(), "v1")
	defer store.Close()

	// Search the FTS row text directly with LIKE rather than the MATCH
	// operator: MATCH tokenizes the query (and may reject hyphenated
	// terms), but LIKE scans the literal text column. The load-bearing
	// claim is that the raw byte sequence is absent from the indexed
	// text — LIKE is the right shape for that check.
	var n int
	if err := store.DB.QueryRow(`select count(*) from fts_messages where text like '%' || ? || '%'`, apiKey).Scan(&n); err != nil {
		t.Fatalf("fts like query: %v", err)
	}
	if n != 0 {
		t.Fatalf("fts_messages contains the raw secret in %d rows; redaction did not reach FTS", n)
	}
	if err := store.DB.QueryRow(`select count(*) from fts_messages where text match 'REDACTED'`).Scan(&n); err != nil {
		t.Fatalf("fts redacted query: %v", err)
	}
	if n == 0 {
		t.Fatalf("fts_messages has no rows containing the REDACTED marker; the redacted text never reached FTS")
	}
}

// TestIngestNoneV1IsBackwardsCompatible pins the default behaviour:
// when no Redactor is configured, ingest behaves exactly as before
// v1.1 — text is verbatim, no redactions table rows are written, and
// sessions.redaction_level stamps 'none-v1'.
func TestIngestNoneV1IsBackwardsCompatible(t *testing.T) {
	sessionID := "redact-none"
	apiKey := "sk-ant-api03-" + strings.Repeat("z", 30)
	lines := []string{
		`{"type":"session","version":3,"id":"` + sessionID + `","timestamp":"2026-01-01T00:00:00Z","cwd":"/tmp"}`,
		`{"type":"message","id":"u1","timestamp":"2026-01-01T00:00:01Z","message":{"role":"user","content":[{"type":"text","text":"key=` + apiKey + `"}]}}`,
	}
	store := ingestWithRedaction(t, sessionID, lines, nil, "")
	defer store.Close()

	var text, level string
	if err := store.DB.QueryRow(`select coalesce(text,'') from messages limit 1`).Scan(&text); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, apiKey) {
		t.Fatalf("none-v1 ingest dropped raw text; secret should survive: %q", text)
	}
	if err := store.DB.QueryRow(`select redaction_level from sessions`).Scan(&level); err != nil {
		t.Fatal(err)
	}
	if level != "none-v1" {
		t.Fatalf("sessions.redaction_level=%q, want 'none-v1'", level)
	}
	var rows int
	if err := store.DB.QueryRow(`select count(*) from redactions`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("redactions table has %d rows under none-v1; want 0", rows)
	}
}

func TestClustersDoNotExposeToolOutputWhenIndexToolOutputDisabled(t *testing.T) {
	secretPhrase := "unindexedproprietaryphrase"
	lines := []string{
		`{"type":"session","version":3,"id":"cluster-no-tool-output","timestamp":"2026-01-01T00:00:00Z","cwd":"/tmp"}`,
		`{"type":"message","id":"call","timestamp":"2026-01-01T00:00:01Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"tu_1","name":"bash","arguments":{"command":"gh pr create --title x"}}]}}`,
		`{"type":"message","id":"result","timestamp":"2026-01-01T00:00:02Z","message":{"role":"toolResult","toolCallId":"tu_1","toolName":"bash","content":[{"type":"text","text":"` + secretPhrase + `"}],"isError":true}}`,
	}
	store := ingestWithRedactionPolicy(t, "cluster-no-tool-output", lines, nil, "none-v1", false)
	defer store.Close()
	clusters, err := corpus.Clusters(store.DB, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(clusters) != 1 {
		t.Fatalf("clusters=%+v", clusters)
	}
	if strings.Contains(clusters[0].ErrorSignature, secretPhrase) || strings.Contains(clusters[0].SampleError, secretPhrase) {
		t.Fatalf("cluster exposed non-indexed tool output: %+v", clusters[0])
	}
	if clusters[0].ErrorSignature != "tool_error" || clusters[0].SampleError != "tool_error" {
		t.Fatalf("cluster should fail closed to generic signature, got %+v", clusters[0])
	}
	var persistedText string
	if err := store.DB.QueryRow(`select coalesce(group_concat(text,'\n'),'') from messages`).Scan(&persistedText); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(persistedText, secretPhrase) {
		t.Fatalf("messages.text exposed non-indexed tool output: %q", persistedText)
	}
}

func TestClusterSignaturesApplyExtraRedactionBeforeNormalization(t *testing.T) {
	secret := "ACME-ABCDEFGHIJKLMNOP"
	r, err := redact.NewWithExtras([]redact.ExtraPattern{{Name: "acme_secret", Regex: `ACME-[A-Z]{16}`}})
	if err != nil {
		t.Fatal(err)
	}
	lines := []string{
		`{"type":"session","version":3,"id":"cluster-extra-redaction","timestamp":"2026-01-01T00:00:00Z","cwd":"/tmp"}`,
		`{"type":"message","id":"call","timestamp":"2026-01-01T00:00:01Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"tu_1","name":"bash","arguments":{"command":"deploy"}}]}}`,
		`{"type":"message","id":"result","timestamp":"2026-01-01T00:00:02Z","message":{"role":"toolResult","toolCallId":"tu_1","toolName":"bash","content":[{"type":"text","text":"` + secret + ` failed"}],"isError":true}}`,
	}
	store := ingestWithRedactionPolicy(t, "cluster-extra-redaction", lines, r, "v1", true)
	defer store.Close()
	clusters, err := corpus.Clusters(store.DB, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(clusters) != 1 {
		t.Fatalf("clusters=%+v", clusters)
	}
	combined := clusters[0].ErrorSignature + "\n" + clusters[0].SampleError
	if strings.Contains(combined, strings.ToLower(secret)) || strings.Contains(combined, secret) {
		t.Fatalf("cluster leaked extra-redacted secret: %+v", clusters[0])
	}
	if !strings.Contains(combined, "[redacted:acme_secret]") {
		t.Fatalf("cluster missing extra redaction marker: %+v", clusters[0])
	}
}

func ingestWithRedaction(t *testing.T, sessionID string, lines []string, r *redact.Redactor, level string) *corpus.Store {
	t.Helper()
	return ingestWithRedactionPolicy(t, sessionID, lines, r, level, false)
}

func ingestWithRedactionPolicy(t *testing.T, sessionID string, lines []string, r *redact.Redactor, level string, indexToolOutput bool) *corpus.Store {
	t.Helper()
	var data []byte
	for _, l := range lines {
		data = append(data, []byte(l+"\n")...)
	}
	mf := model.ManifestFile{Source: "pi", Kind: "session", RelativePath: "sources/pi/sessions/" + sessionID + ".jsonl", RawPath: sessionID + ".jsonl", SHA256: hash.SHA256Bytes(data), Bytes: int64(len(data)), SessionID: sessionID, CopyState: "stable"}
	manifest := model.Manifest{Schema: model.BundleSchema, BundleID: "redact-" + sessionID, MachineID: "m1", CapturedAt: "2026-01-01T00:00:00Z", Policy: model.ManifestPolicy{PathMode: "raw", IncludeSubagents: true, IncludeImages: true, IndexToolOutput: indexToolOutput, Redaction: "none-v1"}, Files: []model.ManifestFile{mf}}
	bundlePath := filepath.Join(t.TempDir(), "bundle.tar.zst")
	if _, err := archive.Write(bundlePath, archive.Bundle{Manifest: manifest, Files: []model.CapturedFile{{Manifest: mf, Data: data}}}); err != nil {
		t.Fatal(err)
	}
	store, err := corpus.Open(filepath.Join(t.TempDir(), "corpus"))
	if err != nil {
		t.Fatal(err)
	}
	ing := corpus.NewIngestor(store, adapters.Builtins())
	ing.Redactor = r
	ing.RedactionLevel = level
	if _, err := ing.IngestBundle(bundlePath); err != nil {
		store.Close()
		t.Fatal(err)
	}
	return store
}

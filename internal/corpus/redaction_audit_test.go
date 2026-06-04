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

func TestRedactionLevelIsNotRestampedForExistingSession(t *testing.T) {
	sessionID := "restamp"
	secret := "sk-ant-api03-" + strings.Repeat("r", 30)
	lines := []string{
		`{"type":"session","version":3,"id":"` + sessionID + `","timestamp":"2026-01-01T00:00:00Z","cwd":"/tmp"}`,
		`{"type":"message","id":"u1","timestamp":"2026-01-01T00:00:01Z","message":{"role":"user","content":"` + secret + `"}}`,
	}
	store, err := corpus.Open(filepath.Join(t.TempDir(), "corpus"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if err := ingestPiBundleWithRedactor(t, store, "bundle-none", sessionID, lines, nil, ""); err != nil {
		t.Fatal(err)
	}
	if err := ingestPiBundleWithRedactor(t, store, "bundle-v1", sessionID, lines, redact.NewDefault(), "v1"); err != nil {
		t.Fatal(err)
	}

	var level string
	if err := store.DB.QueryRow(`select redaction_level from sessions`).Scan(&level); err != nil {
		t.Fatal(err)
	}
	if level != "none-v1" {
		t.Fatalf("existing raw session was restamped as %q, want none-v1 until reindex", level)
	}
	var rows int
	if err := store.DB.QueryRow(`select count(*) from redactions`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("redactions were recorded for ignored existing entries: %d", rows)
	}
}

func TestRedactionCountsDoNotInflateOnSameEntryAcrossBundles(t *testing.T) {
	sessionID := "no-inflate"
	secret := "sk-ant-api03-" + strings.Repeat("i", 30)
	lines := []string{
		`{"type":"session","version":3,"id":"` + sessionID + `","timestamp":"2026-01-01T00:00:00Z","cwd":"/tmp"}`,
		`{"type":"message","id":"u1","timestamp":"2026-01-01T00:00:01Z","message":{"role":"user","content":"` + secret + `"}}`,
	}
	store, err := corpus.Open(filepath.Join(t.TempDir(), "corpus"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if err := ingestPiBundleWithRedactor(t, store, "bundle-a", sessionID, lines, redact.NewDefault(), "v1"); err != nil {
		t.Fatal(err)
	}
	var first int
	if err := store.DB.QueryRow(`select coalesce(sum(count),0) from redactions`).Scan(&first); err != nil {
		t.Fatal(err)
	}
	if first == 0 {
		t.Fatal("first ingest recorded no redactions")
	}
	if err := ingestPiBundleWithRedactor(t, store, "bundle-b", sessionID, lines, redact.NewDefault(), "v1"); err != nil {
		t.Fatal(err)
	}
	var second int
	if err := store.DB.QueryRow(`select coalesce(sum(count),0) from redactions`).Scan(&second); err != nil {
		t.Fatal(err)
	}
	if second != first {
		t.Fatalf("redaction count inflated after ignored duplicate entries: first=%d second=%d", first, second)
	}
}

func TestSessionAndArtifactRedactionEventsAreRecorded(t *testing.T) {
	sessionID := "events"
	secret := "sk-ant-api03-" + strings.Repeat("e", 30)
	sessionLines := []string{
		`{"type":"session","version":3,"id":"` + sessionID + `","timestamp":"2026-01-01T00:00:00Z","cwd":"/tmp/` + secret + `"}`,
		`{"type":"message","id":"u1","timestamp":"2026-01-01T00:00:01Z","message":{"role":"user","content":"hello"}}`,
	}
	artifact := []byte("artifact contains " + secret + "\n")
	store, err := corpus.Open(filepath.Join(t.TempDir(), "corpus"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := ingestPiBundleWithArtifact(t, store, "bundle-events", sessionID, sessionLines, artifact, redact.NewDefault(), "v1"); err != nil {
		t.Fatal(err)
	}

	var sessionEvents, artifactEvents int
	if err := store.DB.QueryRow(`select count(*) from redaction_events where subject_kind='session' and pattern='anthropic_key'`).Scan(&sessionEvents); err != nil {
		t.Fatal(err)
	}
	if err := store.DB.QueryRow(`select count(*) from redaction_events where subject_kind='artifact' and pattern='anthropic_key'`).Scan(&artifactEvents); err != nil {
		t.Fatal(err)
	}
	if sessionEvents == 0 {
		t.Fatalf("session metadata redaction event missing")
	}
	if artifactEvents == 0 {
		t.Fatalf("artifact redaction event missing")
	}
	stats, err := corpus.Status(store.DB, store.Root)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := stats["redaction_hits"].(int); got == 0 {
		t.Fatalf("status redaction_hits not surfaced: %+v", stats)
	}
}

func ingestPiBundleWithRedactor(t *testing.T, store *corpus.Store, bundleID, sessionID string, lines []string, r *redact.Redactor, level string) error {
	t.Helper()
	return ingestPiBundleWithArtifact(t, store, bundleID, sessionID, lines, nil, r, level)
}

func ingestPiBundleWithArtifact(t *testing.T, store *corpus.Store, bundleID, sessionID string, lines []string, artifact []byte, r *redact.Redactor, level string) error {
	t.Helper()
	var data []byte
	for _, l := range lines {
		data = append(data, []byte(l+"\n")...)
	}
	sessionMF := model.ManifestFile{Source: "pi", Kind: "session", RelativePath: "sources/pi/sessions/" + sessionID + ".jsonl", RawPath: sessionID + ".jsonl", SHA256: hash.SHA256Bytes(data), Bytes: int64(len(data)), SessionID: sessionID, CopyState: "stable"}
	files := []model.ManifestFile{sessionMF}
	captured := []model.CapturedFile{{Manifest: sessionMF, Data: data}}
	if artifact != nil {
		artifactMF := model.ManifestFile{Source: "pi", Kind: "artifact", RelativePath: "sources/pi/artifacts/" + sessionID + "_artifact.txt", RawPath: sessionID + "_artifact.txt", SHA256: hash.SHA256Bytes(artifact), Bytes: int64(len(artifact)), ParentHint: sessionID, CopyState: "stable"}
		files = append(files, artifactMF)
		captured = append(captured, model.CapturedFile{Manifest: artifactMF, Data: artifact})
	}
	manifest := model.Manifest{Schema: model.BundleSchema, BundleID: bundleID, MachineID: "m1", CapturedAt: "2026-01-01T00:00:00Z", Policy: model.ManifestPolicy{PathMode: "raw", IncludeSubagents: true, IncludeImages: true, Redaction: "none-v1"}, Files: files}
	bundlePath := filepath.Join(t.TempDir(), bundleID+".tar.zst")
	if _, err := archive.Write(bundlePath, archive.Bundle{Manifest: manifest, Files: captured}); err != nil {
		return err
	}
	ing := corpus.NewIngestor(store, adapters.Builtins())
	ing.Redactor = r
	ing.RedactionLevel = level
	_, err := ing.IngestBundle(bundlePath)
	return err
}

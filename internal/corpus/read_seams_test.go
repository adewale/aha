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
)

func TestReadCanonicalUsesExactRefsOnly(t *testing.T) {
	store, ref := corpusWithOneEntry(t)
	defer store.Close()
	entries, err := corpus.ReadCanonical(store.DB, ref, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || !strings.Contains(entries[0].Text, "canonical needle") {
		t.Fatalf("ReadCanonical returned %+v", entries)
	}
	prefixEntry, err := model.NewEntryID("e")
	if err != nil {
		t.Fatal(err)
	}
	prefix := model.MessageRef{Session: ref.Session, Entry: prefixEntry}
	if _, err := corpus.ReadCanonical(store.DB, prefix, 0, 0); err == nil || !strings.Contains(err.Error(), "entry not found") {
		t.Fatalf("ReadCanonical accepted prefix entry: %v", err)
	}
}

func TestResolveHumanOwnsPrefixResolution(t *testing.T) {
	store, ref := corpusWithOneEntry(t)
	defer store.Close()
	resolved, err := corpus.ResolveHuman(store.DB, "sess", "e")
	if err != nil {
		t.Fatal(err)
	}
	message, ok := resolved.(model.MessageRef)
	if !ok {
		t.Fatalf("ResolveHuman returned %T", resolved)
	}
	if message.Entry != ref.Entry || message.Session != ref.Session {
		t.Fatalf("ResolveHuman=%+v want %+v", resolved, ref)
	}
}

func corpusWithOneEntry(t *testing.T) (*corpus.Store, model.MessageRef) {
	t.Helper()
	data := []byte(`{"type":"message","id":"entry-1","timestamp":"2026-01-01T00:00:00Z","message":{"role":"user","content":[{"type":"text","text":"canonical needle"}]}}` + "\n")
	mf := model.ManifestFile{Source: "pi", Kind: "session", RelativePath: "sources/pi/sessions/session.jsonl", RawPath: "session.jsonl", SHA256: hash.SHA256Bytes(data), Bytes: int64(len(data)), SessionID: "sess", CopyState: "stable"}
	manifest := model.Manifest{Schema: model.BundleSchema, BundleID: "read-seams", MachineID: "m1", CapturedAt: "2026-01-01T00:00:00Z", Policy: model.ManifestPolicy{PathMode: "raw", IncludeSubagents: true, IncludeImages: true, Redaction: "none-v1"}, Files: []model.ManifestFile{mf}}
	bundlePath := filepath.Join(t.TempDir(), "bundle.tar.zst")
	if _, err := archive.Write(bundlePath, archive.Bundle{Manifest: manifest, Files: []model.CapturedFile{{Manifest: mf, Data: data}}}); err != nil {
		t.Fatal(err)
	}
	store, err := corpus.Open(filepath.Join(t.TempDir(), "corpus"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := corpus.IngestBundle(store, adapters.Builtins(), bundlePath); err != nil {
		store.Close()
		t.Fatal(err)
	}
	key, err := model.NewSessionKey("pi", "m1", "sess")
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	entry, err := model.NewEntryID("entry-1")
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	return store, model.MessageRef{Session: key, Entry: entry}
}

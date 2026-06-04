package corpus_test

import (
	"path/filepath"
	"testing"

	"github.com/adewale/aha/internal/adapters"
	"github.com/adewale/aha/internal/archive"
	"github.com/adewale/aha/internal/corpus"
	"github.com/adewale/aha/internal/hash"
	"github.com/adewale/aha/internal/model"
)

// TestMessagesPopulatesCacheAndReasoningTokens pins priority 2 from
// docs/research/cross-agent-data-capture.md: the cache_read /
// cache_write / reasoning token splits are no longer summed into a
// single tokens integer and discarded. They land in their own columns
// alongside the rolled-up tokens for callers that still want the sum.
func TestMessagesPopulatesCacheAndReasoningTokens(t *testing.T) {
	// One assistant entry carrying every token split Claude / OTel GenAI
	// flavoured providers emit today.
	data := []byte(`{"type":"assistant","uuid":"ca1","timestamp":"2026-01-01T00:00:01Z","message":{"role":"assistant","content":[{"type":"text","text":"hello"}],"model":"claude-test","usage":{"input_tokens":11,"output_tokens":13,"cache_creation_input_tokens":50,"cache_read_input_tokens":120,"reasoning_output_tokens":17}}}` + "\n")
	mf := model.ManifestFile{Source: "claude-code", Kind: "session", RelativePath: "sources/claude-code/sessions/session.jsonl", RawPath: "session.jsonl", SHA256: hash.SHA256Bytes(data), Bytes: int64(len(data)), SessionID: "sess-tokens", CopyState: "stable"}
	manifest := model.Manifest{Schema: model.BundleSchema, BundleID: "tokens-split", MachineID: "m1", CapturedAt: "2026-01-01T00:00:00Z", Policy: model.ManifestPolicy{PathMode: "raw", IncludeSubagents: true, IncludeImages: true, Redaction: "none-v1"}, Files: []model.ManifestFile{mf}}
	bundlePath := filepath.Join(t.TempDir(), "bundle.tar.zst")
	if _, err := archive.Write(bundlePath, archive.Bundle{Manifest: manifest, Files: []model.CapturedFile{{Manifest: mf, Data: data}}}); err != nil {
		t.Fatal(err)
	}
	store, err := corpus.Open(filepath.Join(t.TempDir(), "corpus"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := corpus.IngestBundle(store, adapters.Builtins(), bundlePath); err != nil {
		t.Fatal(err)
	}
	var tokens, cacheRead, cacheWrite, reasoning int64
	err = store.DB.QueryRow(`select tokens, cache_read_tokens, cache_write_tokens, reasoning_tokens from messages where entry_id=?`, "ca1").Scan(&tokens, &cacheRead, &cacheWrite, &reasoning)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	// tokens stays the sum (back-compat); the splits are new columns.
	if tokens != 11+13+50+120 {
		t.Fatalf("tokens=%d want %d", tokens, 11+13+50+120)
	}
	if cacheRead != 120 {
		t.Fatalf("cache_read_tokens=%d want 120", cacheRead)
	}
	if cacheWrite != 50 {
		t.Fatalf("cache_write_tokens=%d want 50", cacheWrite)
	}
	if reasoning != 17 {
		t.Fatalf("reasoning_tokens=%d want 17", reasoning)
	}
}

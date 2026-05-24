package search_test

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"testing/quick"

	"github.com/adewale/aha/internal/adapters"
	"github.com/adewale/aha/internal/archive"
	"github.com/adewale/aha/internal/corpus"
	"github.com/adewale/aha/internal/hash"
	"github.com/adewale/aha/internal/model"
	"github.com/adewale/aha/internal/search"
)

func TestSearchResultsAreReadableProperty(t *testing.T) {
	prop := func(input string) bool {
		token := searchPropToken(input)
		bundlePath := writeSearchPropertyBundle(t, token)
		store, err := corpus.Open(filepath.Join(t.TempDir(), "corpus"))
		if err != nil {
			return false
		}
		defer store.Close()
		if _, err := corpus.IngestBundle(store, adapters.Builtins(), bundlePath); err != nil {
			return false
		}
		results, err := search.Query(store.DB, token, search.Filters{Limit: 10})
		if err != nil || len(results) == 0 {
			return false
		}
		for _, result := range results {
			entries, err := corpus.ReadRef(store.DB, result.Ref, 0, 0)
			if err != nil || len(entries) == 0 {
				return false
			}
			found := false
			for _, entry := range entries {
				if strings.Contains(entry.Text, token) || strings.Contains(entry.RawJSON, token) {
					found = true
				}
			}
			if !found {
				return false
			}
		}
		return true
	}
	if err := quick.Check(prop, &quick.Config{MaxCount: 20}); err != nil {
		t.Fatal(err)
	}
}

func writeSearchPropertyBundle(t *testing.T, token string) string {
	t.Helper()
	line := fmt.Sprintf(`{"type":"message","id":"e1","timestamp":"2026-01-01T00:00:00Z","message":{"role":"user","content":[{"type":"text","text":"%s"}]}}`+"\n", token)
	data := []byte(line)
	mf := model.ManifestFile{Source: "pi", Kind: "session", RelativePath: "sources/pi/sessions/session.jsonl", RawPath: "session.jsonl", SHA256: hash.SHA256Bytes(data), Bytes: int64(len(data)), SessionID: "session", CopyState: "stable"}
	manifest := model.Manifest{Schema: model.BundleSchema, BundleID: "search-prop-" + token, MachineID: "m1", CapturedAt: "2026-01-01T00:00:00Z", Policy: model.ManifestPolicy{PathMode: "raw", IncludeSubagents: true, IncludeImages: true, Redaction: "none-v1"}, Files: []model.ManifestFile{mf}}
	path := filepath.Join(t.TempDir(), "bundle.tar.zst")
	if _, err := archive.Write(path, archive.Bundle{Manifest: manifest, Files: []model.CapturedFile{{Manifest: mf, Data: data}}}); err != nil {
		t.Fatal(err)
	}
	return path
}

func searchPropToken(input string) string {
	sum := sha256.Sum256([]byte(input))
	return "term" + hex.EncodeToString(sum[:])[:12]
}

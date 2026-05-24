package corpus_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adewale/aha/internal/adapters"
	"github.com/adewale/aha/internal/archive"
	"github.com/adewale/aha/internal/corpus"
	"github.com/adewale/aha/internal/hash"
	"github.com/adewale/aha/internal/model"
	"github.com/adewale/aha/internal/search"
	"pgregory.net/rapid"
)

func TestCorpusStateMachineSkeleton(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		root, err := os.MkdirTemp("", "aha-corpus-sm-*")
		if err != nil {
			rt.Fatal(err)
		}
		rt.Cleanup(func() { _ = os.RemoveAll(root) })
		bundlePath := writeStateMachineBundle(rt, root)
		store, err := corpus.Open(filepath.Join(root, "corpus"))
		if err != nil {
			rt.Fatal(err)
		}
		rt.Cleanup(func() { _ = store.Close() })
		ops := rapid.SliceOfN(rapid.SampledFrom([]string{"ingest", "duplicate", "search", "read", "status", "verify"}), 1, 12).Draw(rt, "ops")
		ingested := false
		ref := model.HitRef{Kind: model.HitKindMessage, SessionKey: "pi:sm-machine:sm-session", EntryID: "e1"}
		for _, op := range ops {
			switch op {
			case "ingest":
				rep, err := corpus.IngestBundle(store, adapters.Builtins(), bundlePath)
				if err != nil {
					rt.Fatal(err)
				}
				if ingested && !rep.Duplicate {
					rt.Fatalf("re-ingest not marked duplicate: %+v", rep)
				}
				ingested = true
			case "duplicate":
				if !ingested {
					continue
				}
				rep, err := corpus.IngestBundle(store, adapters.Builtins(), bundlePath)
				if err != nil {
					rt.Fatal(err)
				}
				if !rep.Duplicate {
					rt.Fatalf("duplicate ingest not marked duplicate: %+v", rep)
				}
			case "search":
				results, err := search.Query(store.DB, "state-machine-needle", search.Filters{Limit: 10})
				if err != nil {
					rt.Fatal(err)
				}
				if ingested && len(results) == 0 {
					rt.Fatal("ingested corpus search returned no results")
				}
				for _, result := range results {
					if _, err := corpus.ReadCanonical(store.DB, result.Ref, 0, 0); err != nil {
						rt.Fatalf("search result not canonically readable: %+v err=%v", result.Ref, err)
					}
				}
			case "read":
				if !ingested {
					continue
				}
				entries, err := corpus.ReadCanonical(store.DB, ref, 0, 0)
				if err != nil {
					rt.Fatal(err)
				}
				if len(entries) != 1 || !strings.Contains(entries[0].Text, "state-machine-needle") {
					rt.Fatalf("bad read entries: %+v", entries)
				}
			case "status":
				stats, err := corpus.Status(store.DB, store.Root)
				if err != nil {
					rt.Fatal(err)
				}
				wantEntries := 0
				if ingested {
					wantEntries = 1
				}
				if stats["entries"] != wantEntries {
					rt.Fatalf("entries status=%v want %d after ops=%v", stats["entries"], wantEntries, ops)
				}
			case "verify":
				report, err := corpus.Verify(store)
				if err != nil {
					rt.Fatal(err)
				}
				if len(report.Problems) != 0 {
					rt.Fatalf("verify problems after valid operations: %+v", report.Problems)
				}
			}
		}
	})
}

func writeStateMachineBundle(t interface{ Fatal(args ...any) }, root string) string {
	data := []byte(`{"type":"message","id":"e1","timestamp":"2026-01-01T00:00:00Z","message":{"role":"user","content":[{"type":"text","text":"state-machine-needle"}]}}` + "\n")
	mf := model.ManifestFile{Source: "pi", Kind: "session", RelativePath: "sources/pi/sessions/session.jsonl", RawPath: "session.jsonl", SHA256: hash.SHA256Bytes(data), Bytes: int64(len(data)), SessionID: "sm-session", CopyState: "stable"}
	manifest := model.Manifest{Schema: model.BundleSchema, BundleID: "state-machine-bundle", MachineID: "sm-machine", CapturedAt: "2026-01-01T00:00:00Z", Policy: model.ManifestPolicy{PathMode: "raw", IncludeSubagents: true, IncludeImages: true, Redaction: "none-v1"}, Files: []model.ManifestFile{mf}}
	path := filepath.Join(root, "bundle.tar.zst")
	if _, err := archive.Write(path, archive.Bundle{Manifest: manifest, Files: []model.CapturedFile{{Manifest: mf, Data: data}}}); err != nil {
		t.Fatal(err)
	}
	return path
}

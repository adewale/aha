package search_test

import (
	"path/filepath"
	"testing"

	"github.com/adewale/aha/internal/adapters"
	"github.com/adewale/aha/internal/archive"
	"github.com/adewale/aha/internal/config"
	"github.com/adewale/aha/internal/corpus"
	"github.com/adewale/aha/internal/model"
	"github.com/adewale/aha/internal/search"
	"github.com/adewale/aha/internal/testutil"
)

func TestQueryFiltersMessagesAndArtifacts(t *testing.T) {
	store := buildSearchCorpus(t)
	results, err := search.Query(store.DB, "needle", search.Filters{Source: "claude-code", Role: "user", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Source != "claude-code" || results[0].Role != "user" || results[0].RefText == "" || results[0].Ref.Kind != model.HitKindMessage {
		t.Fatalf("unexpected filtered results: %+v", results)
	}
	artifacts, err := search.Query(store.DB, "artifact needle", search.Filters{Role: "artifact", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(artifacts) == 0 {
		t.Fatalf("expected artifact results")
	}
	for _, result := range artifacts {
		if result.Role != "artifact" || result.RefText == "" || result.Ref.Kind != model.HitKindArtifact {
			t.Fatalf("bad artifact result: %+v", result)
		}
	}
}

func TestQueryPathFilterEscapesLikeWildcards(t *testing.T) {
	store := buildSearchCorpus(t)
	results, err := search.Query(store.DB, "needle", search.Filters{Path: "%", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("path wildcard filter matched as SQL wildcard: %+v", results)
	}
	results, err = search.Query(store.DB, "needle", search.Filters{Path: "Users_me", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("underscore path filter matched as SQL wildcard: %+v", results)
	}
}

func TestQueryQuotesArbitraryTermsForFTS(t *testing.T) {
	store := buildSearchCorpus(t)
	for _, query := range []string{`needle "quoted"`, `--json`, `needle:`, `path/to/file`, `one OR two`} {
		t.Run(query, func(t *testing.T) {
			if _, err := search.Query(store.DB, query, search.Filters{Limit: 5}); err != nil {
				t.Fatalf("Query(%q): %v", query, err)
			}
		})
	}
}

func buildSearchCorpus(t *testing.T) *corpus.Store {
	t.Helper()
	root := t.TempDir()
	fx := testutil.WriteAgentFixtures(t, root)
	cfg := config.Default()
	cfg.MachineID = "m1"
	cfg.Sources = []model.SourceConfig{{Type: "pi", Root: fx.PiRoot, Enabled: true}, {Type: "claude-code", Root: fx.ClaudeRoot, Enabled: true}}
	bundle, err := archive.Capture(t.Context(), cfg, adapters.Builtins(), archive.Options{CapturedAt: "2026-01-03T00:00:00Z", BundleID: "search"})
	if err != nil {
		t.Fatal(err)
	}
	bundlePath := filepath.Join(root, "bundle.tar.zst")
	if _, err := archive.Write(bundlePath, bundle); err != nil {
		t.Fatal(err)
	}
	store, err := corpus.Open(filepath.Join(root, "corpus"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if _, err := corpus.IngestBundle(store, adapters.Builtins(), bundlePath); err != nil {
		t.Fatal(err)
	}
	return store
}

package adapters_test

import (
	"testing"

	"pgregory.net/rapid"

	"github.com/adewale/aha/internal/adapters"
	"github.com/adewale/aha/internal/adapters/fixtureaudit"
	"github.com/adewale/aha/internal/model"
)

// TestAuditIsInvariantToFixtureOrder pins that the audit is a set, not a
// sequence. Corpus directories are walked by the filesystem and fixtures are
// added in whatever order someone happens to write them; if any of that
// reached the artefact, the regenerate-and-diff gate would produce spurious
// diffs and eventually be disbelieved.
func TestAuditIsInvariantToFixtureOrder(t *testing.T) {
	fixtures, err := fixtureaudit.Inventory(fixtureRoot)
	if err != nil {
		t.Fatal(err)
	}
	readers := fixtureaudit.ReadersFrom(adapters.Builtins())
	want, err := fixtureaudit.BuildAuditFrom(fixtures, readers)
	if err != nil {
		t.Fatal(err)
	}
	wantJSON, err := want.JSON()
	if err != nil {
		t.Fatal(err)
	}
	rapid.Check(t, func(rt *rapid.T) {
		shuffled := rapid.Permutation(fixtures).Draw(rt, "fixture order")
		got, err := fixtureaudit.BuildAuditFrom(shuffled, readers)
		if err != nil {
			rt.Fatal(err)
		}
		gotJSON, err := got.JSON()
		if err != nil {
			rt.Fatal(err)
		}
		if string(gotJSON) != string(wantJSON) {
			rt.Fatalf("audit changed under a reordering of the same fixtures")
		}
		if got.Markdown() != want.Markdown() {
			rt.Fatalf("rendered audit changed under a reordering of the same fixtures")
		}
	})
}

// TestAuditRecordCountsSumAcrossFiles checks the counts are an accounting of
// the corpus rather than a plausible-looking number: every non-blank line of
// every fixture is counted exactly once, under exactly one source.
func TestAuditRecordCountsSumAcrossFiles(t *testing.T) {
	fixtures, err := fixtureaudit.Inventory(fixtureRoot)
	if err != nil {
		t.Fatal(err)
	}
	wantRecords := map[string]int{}
	wantFiles := map[string]int{}
	wantRecordsWithVersion := map[string]int{}
	for _, fx := range fixtures {
		records, err := fixtureaudit.ReadRecords(fx.Path)
		if err != nil {
			t.Fatal(err)
		}
		wantFiles[fx.Source]++
		wantRecords[fx.Source] += len(records)
		adapter := adapters.Builtins()[fx.Source]
		for _, rec := range records {
			if adapter.VersionSignal(model.ParsedEntry{RawJSON: rec.Raw}).Value() != "" {
				wantRecordsWithVersion[fx.Source]++
			}
		}
	}
	audit := buildAudit(t)
	totalRecords := 0
	for _, s := range audit.Sources {
		if s.Files != wantFiles[s.Source] {
			t.Fatalf("%s files=%d want %d", s.Source, s.Files, wantFiles[s.Source])
		}
		if s.Records != wantRecords[s.Source] {
			t.Fatalf("%s records=%d want %d", s.Source, s.Records, wantRecords[s.Source])
		}
		if s.RecordsWithVersion != wantRecordsWithVersion[s.Source] {
			t.Fatalf("%s records with a version=%d want %d", s.Source, s.RecordsWithVersion, wantRecordsWithVersion[s.Source])
		}
		if s.RecordsWithVersion > s.Records {
			t.Fatalf("%s counts more versioned records (%d) than records (%d)", s.Source, s.RecordsWithVersion, s.Records)
		}
		if s.FilesWithVersion > s.Files {
			t.Fatalf("%s counts more versioned files (%d) than files (%d)", s.Source, s.FilesWithVersion, s.Files)
		}
		perVersion := 0
		for _, v := range s.Versions {
			perVersion += v.Records
		}
		if perVersion != s.RecordsWithVersion {
			t.Fatalf("%s per-version record counts sum to %d, but %d records carry a version", s.Source, perVersion, s.RecordsWithVersion)
		}
		for _, p := range s.KeyPaths {
			if p.Files > s.Files {
				t.Fatalf("%s key path %s is carried by %d files but the source has %d", s.Source, p.Path, p.Files, s.Files)
			}
			if p.UnversionedFiles > p.Files {
				t.Fatalf("%s key path %s: %d unversioned files exceeds %d carrying files", s.Source, p.Path, p.UnversionedFiles, p.Files)
			}
		}
		totalRecords += s.Records
	}
	sum := 0
	for _, n := range wantRecords {
		sum += n
	}
	if totalRecords != sum {
		t.Fatalf("audit accounts for %d records; the corpus has %d", totalRecords, sum)
	}
}

// TestAuditBandSpansEveryObservedVersion is the invariant the vendoring gate
// leans on: the band is not a summary that could drift from the list, it is
// the endpoints of it.
func TestAuditBandSpansEveryObservedVersion(t *testing.T) {
	for _, s := range buildAudit(t).Sources {
		if len(s.Versions) == 0 {
			if s.Band != nil {
				t.Fatalf("%s records a band with no observed versions", s.Source)
			}
			continue
		}
		if s.Band == nil {
			t.Fatalf("%s observed %d version(s) but records no band", s.Source, len(s.Versions))
		}
		for _, v := range s.Versions {
			if fixtureaudit.CompareVersions(v.Version, s.Band.First) < 0 {
				t.Fatalf("%s observed %s below its own band start %s", s.Source, v.Version, s.Band.First)
			}
			if fixtureaudit.CompareVersions(v.Version, s.Band.Last) > 0 {
				t.Fatalf("%s observed %s above its own band end %s", s.Source, v.Version, s.Band.Last)
			}
		}
	}
}

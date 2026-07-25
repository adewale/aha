package adapters_test

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/adewale/aha/internal/adapters"
	"github.com/adewale/aha/internal/adapters/fixtureaudit"
	"github.com/adewale/aha/internal/model"
)

func buildAudit(t *testing.T) fixtureaudit.Audit {
	t.Helper()
	audit, err := fixtureaudit.BuildAudit(fixtureRoot, fixtureaudit.ReadersFrom(adapters.Builtins()))
	if err != nil {
		t.Fatal(err)
	}
	return audit
}

// TestGeneratedVersionAuditIsUpToDate is the regenerate-and-diff gate. The
// audit is generated, never hand-written, so the only way it can be wrong is
// by being stale — and a stale audit is worse than none, because it reports a
// calibration that no longer matches the corpus.
func TestGeneratedVersionAuditIsUpToDate(t *testing.T) {
	audit := buildAudit(t)
	generated, err := audit.JSON()
	if err != nil {
		t.Fatal(err)
	}
	onDisk, err := os.ReadFile(auditJSONPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(generated, onDisk) {
		t.Fatalf("%s is stale; re-run `make gen-version-audit`", auditJSONPath)
	}
	markdownOnDisk, err := os.ReadFile(auditMarkdownPath)
	if err != nil {
		t.Fatal(err)
	}
	if audit.Markdown() != string(markdownOnDisk) {
		t.Fatalf("%s is stale; re-run `make gen-version-audit`", auditMarkdownPath)
	}
}

// TestHandEditingTheGeneratedAuditFails is the other half: the gate above must
// fail on an edit, not merely on a missing file. A hand-edited band is exactly
// the change someone would make to quiet a failing version gate.
func TestHandEditingTheGeneratedAuditFails(t *testing.T) {
	onDisk, err := os.ReadFile(auditJSONPath)
	if err != nil {
		t.Fatal(err)
	}
	edited := bytes.Replace(onDisk, []byte(`"first": "2.1.92"`), []byte(`"first": "2.1.0"`), 1)
	if bytes.Equal(edited, onDisk) {
		t.Fatal("expected the committed audit to record claude-code's band as starting at 2.1.92")
	}
	generated, err := buildAudit(t).JSON()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(generated, edited) {
		t.Fatal("a hand-widened version band still matched the regenerated audit; the staleness gate would not catch it")
	}
}

// TestCorpusVersionsAreWithinTheRecordedBand is the vendoring gate. A corpus
// carrying a version outside the recorded band fails here rather than sliding
// in as a diff of unexplained new key paths, which makes re-deriving the
// calibration a deliberate act.
func TestCorpusVersionsAreWithinTheRecordedBand(t *testing.T) {
	committed, err := os.ReadFile(auditJSONPath)
	if err != nil {
		t.Fatal(err)
	}
	audit, err := fixtureaudit.LoadAudit(committed)
	if err != nil {
		t.Fatal(err)
	}
	observed, err := fixtureaudit.ObserveVersions(fixtureRoot, fixtureaudit.ReadersFrom(adapters.Builtins()))
	if err != nil {
		t.Fatal(err)
	}
	if len(observed) == 0 {
		t.Fatal("no versions observed across the corpus, so the band gate proves nothing")
	}
	violations := audit.CheckBands(observed)
	if len(violations) > 0 {
		messages := make([]string, 0, len(violations))
		for _, v := range violations {
			messages = append(messages, v.Error())
		}
		t.Fatalf("%d corpus version(s) outside the recorded band:\n  %s", len(violations), strings.Join(messages, "\n  "))
	}
}

// TestVersionOutsideTheBandIsReportedWithSourceAndBand pins what the gate says
// when it fires. A message naming only "version mismatch" would leave the
// reader to work out which source moved and what it moved from.
func TestVersionOutsideTheBandIsReportedWithSourceAndBand(t *testing.T) {
	audit := buildAudit(t)
	cases := []struct {
		name     string
		observed map[string][]string
		want     []string
	}{
		{
			name:     "newer than the band",
			observed: map[string][]string{"claude-code": {"2.1.206"}},
			want:     []string{"claude-code", "2.1.206", "2.1.92"},
		},
		{
			name:     "older than the band",
			observed: map[string][]string{"codex": {"0.101.0"}},
			want:     []string{"codex", "0.101.0", "0.116.0", "0.130.0"},
		},
		{
			name:     "a source recorded as carrying no version",
			observed: map[string][]string{"opencode": {"1.0.0"}},
			want:     []string{"opencode", "1.0.0", "no version band"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			violations := audit.CheckBands(tc.observed)
			if len(violations) != 1 {
				t.Fatalf("got %d violations, want 1", len(violations))
			}
			message := violations[0].Error()
			for _, want := range tc.want {
				if !strings.Contains(message, want) {
					t.Fatalf("violation message %q must name %q", message, want)
				}
			}
		})
	}
}

// TestVersionsInsideTheBandDoNotFire keeps the gate from being vacuous: a
// version between the recorded endpoints is in band, and the staleness gate —
// not this one — is what reports it as new.
func TestVersionsInsideTheBandDoNotFire(t *testing.T) {
	audit := buildAudit(t)
	for _, observed := range []map[string][]string{
		{"codex": {"0.116.0", "0.130.0"}},
		{"codex": {"0.120.3"}},
		{"claude-code": {"2.1.92"}},
		{"pi": {"3"}},
		{"opencode": nil},
	} {
		if violations := audit.CheckBands(observed); len(violations) != 0 {
			t.Fatalf("in-band observation %v reported %d violation(s): %v", observed, len(violations), violations[0].Error())
		}
	}
}

// TestAuditCoversEveryRegisteredAdapter is the exhaustive check: an adapter
// that exists but is absent from the audit would be an unmeasured source
// reported as no source at all.
func TestAuditCoversEveryRegisteredAdapter(t *testing.T) {
	audit := buildAudit(t)
	covered := map[string]fixtureaudit.SourceAudit{}
	for _, s := range audit.Sources {
		covered[s.Source] = s
	}
	for name := range adapters.Builtins() {
		s, ok := covered[name]
		if !ok {
			t.Fatalf("adapter %q is registered but absent from the audit", name)
		}
		if s.Files == 0 {
			t.Fatalf("adapter %q is in the audit with no fixtures, so its coverage is unmeasured", name)
		}
		if s.Records == 0 {
			t.Fatalf("adapter %q is in the audit with no records", name)
		}
		if len(s.KeyPaths) == 0 {
			t.Fatalf("adapter %q is in the audit with no key paths", name)
		}
	}
	if len(audit.Sources) != len(adapters.Builtins()) {
		t.Fatalf("audit covers %d sources, registry has %d", len(audit.Sources), len(adapters.Builtins()))
	}
}

// TestAuditReportsTheDeclaredKindPerSource ties the audit back to the
// interface declaration, so a source cannot be stratified on one axis while
// claiming another.
func TestAuditReportsTheDeclaredKindPerSource(t *testing.T) {
	audit := buildAudit(t)
	for _, s := range audit.Sources {
		declared := adapters.Builtins()[s.Source].VersionSignal(model.ParsedEntry{}).Kind()
		if s.VersionKind != string(declared) {
			t.Fatalf("audit reports %s as kind %q; the adapter declares %q", s.Source, s.VersionKind, declared)
		}
		if s.VersionKind == string(model.VersionKindNone) && len(s.Versions) > 0 {
			t.Fatalf("%s declares it carries no version but the audit lists %d", s.Source, len(s.Versions))
		}
		if s.VersionKind != string(model.VersionKindNone) && len(s.Versions) == 0 {
			t.Fatalf("%s declares a %s version but the audit observed none — the corpus cannot support any version claim", s.Source, s.VersionKind)
		}
	}
}

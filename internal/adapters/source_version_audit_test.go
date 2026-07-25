package adapters_test

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/adewale/aha/internal/adapters"
	"github.com/adewale/aha/internal/adapters/fixtureaudit"
	"github.com/adewale/aha/internal/model"
)

const (
	auditJSONPath     = "testdata/source-version-audit.json"
	auditMarkdownPath = "../../docs/source-version-audit.md"
)

// The audit is aggregate structure and counts. It is generated from files
// containing prompts, source code, commands, tool output and private paths,
// and one of its two renderings is committed to docs/, so leaking transcript
// content into it would be a worse outcome than having no audit at all. These
// two tests were written before the generator existed.

// TestAuditLeaksNoFixtureContent derives its blacklist from the fixtures
// themselves rather than from a hand-kept list, so a leak introduced by a
// future field is caught without anyone remembering to add it. Every leaf
// string value long enough to be content rather than a discriminator must be
// absent from both renderings; the version values the audit exists to report
// are the sole exception.
func TestAuditLeaksNoFixtureContent(t *testing.T) {
	jsonBody := readAuditFile(t, auditJSONPath)
	markdownBody := readAuditFile(t, auditMarkdownPath)
	versions := observedVersionValues(t)

	fixtures, err := fixtureaudit.Inventory(fixtureRoot)
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, fx := range fixtures {
		records, err := fixtureaudit.ReadRecords(fx.Path)
		if err != nil {
			t.Fatal(err)
		}
		for _, rec := range records {
			for _, leaf := range leafStrings(rec.Value) {
				// Short leaves are discriminators ("message", "user",
				// "tool_use") that collide with field names by design.
				if len(leaf) < 16 || versions[leaf] {
					continue
				}
				checked++
				if strings.Contains(jsonBody, leaf) {
					t.Fatalf("%s contains fixture content %q (from %s)", auditJSONPath, leaf, fx.Label)
				}
				if strings.Contains(markdownBody, leaf) {
					t.Fatalf("%s contains fixture content %q (from %s)", auditMarkdownPath, leaf, fx.Label)
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no fixture leaf values were long enough to check, so this privacy test proves nothing")
	}
}

// TestAuditLeaksNoNamedFixtureContent states the same guarantee against
// specific strings, so the intent survives even if the derivation above is
// ever weakened: prose, tool arguments, file paths, session identifiers and
// working directories are not what an audit of shapes and counts reports.
func TestAuditLeaksNoNamedFixtureContent(t *testing.T) {
	forbidden := map[string]string{
		"opencode fixture needle":              "prose",
		"assistant replies here":               "prose",
		"6b2bb1c0-445d-4678-88bf-72a31d128fec": "session identifier",
		"019d2fac-0b38-70f0-baff-a394265d8291": "session identifier",
		"ses_realish":                          "session identifier",
		"/Users/example/work":                  "working directory",
		"/Users/example/Downloads":             "working directory",
		"fixture-signature":                    "opaque provider token",
	}
	for _, path := range []string{auditJSONPath, auditMarkdownPath} {
		body := readAuditFile(t, path)
		for needle, kind := range forbidden {
			if strings.Contains(body, needle) {
				t.Fatalf("%s contains %s %q; the audit reports aggregate structure and counts only", path, kind, needle)
			}
		}
	}
}

// TestAuditJSONContainsOnlyStructureAndCounts is the whitelist half: rather
// than enumerate what must not appear, it fails on any string that is not one
// of the four things the audit is allowed to say — a source name, a version
// kind, an observed version value, or a key path.
func TestAuditJSONContainsOnlyStructureAndCounts(t *testing.T) {
	var doc any
	if err := json.Unmarshal([]byte(readAuditFile(t, auditJSONPath)), &doc); err != nil {
		t.Fatal(err)
	}
	// The empty string is how the audit says "no version was recorded here";
	// it carries nothing.
	allowed := map[string]bool{"": true}
	for _, source := range fixtureaudit.Sources() {
		allowed[source] = true
	}
	for _, kind := range model.VersionKinds() {
		allowed[string(kind)] = true
	}
	for value := range observedVersionValues(t) {
		allowed[value] = true
	}
	for pair := range observedSourcePaths(t) {
		allowed[pair.Path] = true
	}
	// The document's own explanatory prose is authored here, not derived
	// from fixtures, and is exempt by name rather than by pattern.
	keyPathShape := regexp.MustCompile(`^[A-Za-z0-9_.\[\]-]+$`)
	for _, leaf := range leafStringsExcludingKey(doc, "doc") {
		if allowed[leaf] {
			continue
		}
		t.Fatalf("%s contains string %q, which is not a source name, a version kind, an observed version, or an observed key path (key-path shape: %s)", auditJSONPath, leaf, keyPathShape)
	}
}

func readAuditFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) == 0 {
		t.Fatalf("%s is empty", path)
	}
	return string(b)
}

// observedVersionValues returns every version value the adapters report over
// the committed fixtures. These are the only fixture-derived strings the
// audit is allowed to carry.
func observedVersionValues(t *testing.T) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	fixtures, err := fixtureaudit.Inventory(fixtureRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, fx := range fixtures {
		adapter := adapters.Builtins()[fx.Source]
		if adapter == nil {
			t.Fatalf("fixture %s is attributed to unknown source %q", fx.Label, fx.Source)
		}
		records, err := fixtureaudit.ReadRecords(fx.Path)
		if err != nil {
			t.Fatal(err)
		}
		for _, rec := range records {
			if v := adapter.VersionSignal(model.ParsedEntry{RawJSON: rec.Raw}).Value(); v != "" {
				out[v] = true
			}
		}
	}
	if len(out) == 0 {
		t.Fatal("no version values observed across the fixture corpus")
	}
	return out
}

func leafStrings(v any) []string {
	var out []string
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			for _, child := range x {
				walk(child)
			}
		case []any:
			for _, child := range x {
				walk(child)
			}
		case string:
			out = append(out, x)
		}
	}
	walk(v)
	return out
}

// leafStringsExcludingKey collects leaf strings but skips the value of any
// object member named skip, at any depth.
func leafStringsExcludingKey(v any, skip string) []string {
	var out []string
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, child := range x {
				if k == skip {
					continue
				}
				walk(child)
			}
		case []any:
			for _, child := range x {
				walk(child)
			}
		case string:
			out = append(out, x)
		}
	}
	walk(v)
	return out
}

package fixtureaudit

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/adewale/aha/internal/model"
)

// AuditVersion is the on-disk schema version of the generated audit.
const AuditVersion = 1

// AuditDoc is the audit's own explanation of itself, carried in the generated
// JSON so the artefact is readable without its generator to hand.
const AuditDoc = "Generated record of how much of each source's release history the committed adapter " +
	"fixtures actually cover. Regenerate with `make gen-version-audit`; never hand-edit. " +
	"Per source: the kind of version the adapter declares it can read, every distinct version " +
	"value observed, the band those values span, and the version band in which each key path was " +
	"first and last seen. A source declaring kind 'none' carries no version anywhere, so its " +
	"coverage is known to be unstratified. Counts are of files and records; the audit reports " +
	"aggregate structure only and never fixture content."

// VersionReader is the part of a source adapter the audit needs: where the
// source records its version, and what that version measures. It is satisfied
// by adapters.SourceAdapter and taken as a parameter so this package stays a
// leaf that the adapter tests themselves can import.
type VersionReader interface {
	VersionSignal(entry model.ParsedEntry) model.VersionSignal
}

// ReadersFrom narrows an adapter registry to the interface the audit needs.
// Callers pass adapters.Builtins() directly, so the audit covers exactly the
// registered adapters and a new one cannot be omitted by being left off a
// second list.
func ReadersFrom[T VersionReader](registry map[string]T) map[string]VersionReader {
	out := make(map[string]VersionReader, len(registry))
	for name, adapter := range registry {
		out[name] = adapter
	}
	return out
}

// Audit is the generated source-version audit.
type Audit struct {
	Version int           `json:"version"`
	Doc     string        `json:"doc"`
	Sources []SourceAudit `json:"sources"`
}

// SourceAudit is one adapter's evidence base.
type SourceAudit struct {
	Source string `json:"source"`
	// VersionKind is what this source's version measures, as the adapter
	// declares it: "producer", "schema", or "none".
	VersionKind string `json:"version_kind"`
	// Band spans the observed version values. It is nil when no fixture for
	// this source carries a version, which is the honest report for a source
	// of kind "none" and a gap for any other.
	Band     *VersionBand         `json:"band"`
	Versions []VersionObservation `json:"versions"`
	// Files and Records count the whole evidence base; the *WithVersion
	// counts say how much of it is attributable to a version.
	Files              int               `json:"files"`
	FilesWithVersion   int               `json:"files_with_version"`
	Records            int               `json:"records"`
	RecordsWithVersion int               `json:"records_with_version"`
	KeyPaths           []KeyPathCoverage `json:"key_paths"`
}

// VersionBand is the closed interval of versions the fixtures span.
type VersionBand struct {
	First string `json:"first"`
	Last  string `json:"last"`
}

// VersionObservation is one distinct version value and how much evidence
// carries it.
type VersionObservation struct {
	Version string `json:"version"`
	Files   int    `json:"files"`
	Records int    `json:"records"`
}

// KeyPathCoverage records the version band in which one key path was seen.
// FirstVersion and LastVersion are empty when the path appears only in files
// that carry no version, which is the difference between "this field is old"
// and "we cannot say when this field appeared".
type KeyPathCoverage struct {
	Path             string `json:"path"`
	Files            int    `json:"files"`
	FirstVersion     string `json:"first_version"`
	LastVersion      string `json:"last_version"`
	UnversionedFiles int    `json:"unversioned_files"`
}

// BuildAudit reads every committed fixture beneath root and derives the audit.
// It is a pure function of the fixture set: the result depends on which files
// exist and what they contain, never on the order the filesystem returns them
// in.
func BuildAudit(root string, readers map[string]VersionReader) (Audit, error) {
	fixtures, err := Inventory(root)
	if err != nil {
		return Audit{}, err
	}
	return BuildAuditFrom(fixtures, readers)
}

// BuildAuditFrom derives the audit from an explicit fixture set. The audit is
// a set, not a sequence: the result must not depend on the order fixtures
// arrive in, and taking the order as a parameter is what lets that be tested
// rather than assumed.
func BuildAuditFrom(fixtures []Fixture, readers map[string]VersionReader) (Audit, error) {
	bySource := map[string][]Fixture{}
	for _, fx := range fixtures {
		bySource[fx.Source] = append(bySource[fx.Source], fx)
	}
	audit := Audit{Version: AuditVersion, Doc: AuditDoc}
	for _, source := range Sources() {
		reader, ok := readers[source]
		if !ok {
			return Audit{}, fmt.Errorf("no version reader for source %q", source)
		}
		sa, err := buildSourceAudit(source, reader, bySource[source])
		if err != nil {
			return Audit{}, err
		}
		audit.Sources = append(audit.Sources, sa)
	}
	return audit, nil
}

func buildSourceAudit(source string, reader VersionReader, fixtures []Fixture) (SourceAudit, error) {
	kind := reader.VersionSignal(model.ParsedEntry{}).Kind()
	if !isDeclaredKind(kind) {
		return SourceAudit{}, fmt.Errorf("source %q declares version kind %q, which is outside the closed set %v", source, kind, model.VersionKinds())
	}
	sa := SourceAudit{Source: source, VersionKind: string(kind), Files: len(fixtures)}

	versionFiles := map[string]map[string]struct{}{} // version -> file labels
	versionRecords := map[string]int{}
	pathFiles := map[string]map[string]struct{}{} // key path -> file labels
	fileVersions := map[string][]string{}         // file label -> versions, ordered

	for _, fx := range fixtures {
		records, err := ReadRecords(fx.Path)
		if err != nil {
			return SourceAudit{}, err
		}
		sa.Records += len(records)
		seen := map[string]struct{}{}
		for _, rec := range records {
			signal := reader.VersionSignal(model.ParsedEntry{RawJSON: rec.Raw})
			if signal.Kind() != kind {
				return SourceAudit{}, fmt.Errorf("source %q reported kind %q for one record and %q for another; the kind is a property of the source", source, kind, signal.Kind())
			}
			if v := signal.Value(); v != "" {
				sa.RecordsWithVersion++
				versionRecords[v]++
				if versionFiles[v] == nil {
					versionFiles[v] = map[string]struct{}{}
				}
				versionFiles[v][fx.Label] = struct{}{}
				seen[v] = struct{}{}
			}
			for _, p := range KeyPaths(rec.Value) {
				if pathFiles[p] == nil {
					pathFiles[p] = map[string]struct{}{}
				}
				pathFiles[p][fx.Label] = struct{}{}
			}
		}
		if len(seen) > 0 {
			sa.FilesWithVersion++
			fileVersions[fx.Label] = sortedVersions(seen)
		}
	}

	for _, v := range sortedVersions(versionFiles) {
		sa.Versions = append(sa.Versions, VersionObservation{Version: v, Files: len(versionFiles[v]), Records: versionRecords[v]})
	}
	if len(sa.Versions) > 0 {
		sa.Band = &VersionBand{First: sa.Versions[0].Version, Last: sa.Versions[len(sa.Versions)-1].Version}
	}
	if kind == model.VersionKindNone && len(sa.Versions) > 0 {
		return SourceAudit{}, fmt.Errorf("source %q declares it carries no version but %d version value(s) were observed", source, len(sa.Versions))
	}

	paths := keySet(pathFiles)
	sort.Strings(paths)
	for _, p := range paths {
		cov := KeyPathCoverage{Path: p, Files: len(pathFiles[p])}
		var seen []string
		for label := range pathFiles[p] {
			versions, ok := fileVersions[label]
			if !ok {
				cov.UnversionedFiles++
				continue
			}
			seen = append(seen, versions...)
		}
		if len(seen) > 0 {
			seen = sortedVersions(setOf(seen))
			cov.FirstVersion, cov.LastVersion = seen[0], seen[len(seen)-1]
		}
		sa.KeyPaths = append(sa.KeyPaths, cov)
	}
	return sa, nil
}

func isDeclaredKind(kind model.VersionKind) bool {
	for _, k := range model.VersionKinds() {
		if k == kind {
			return true
		}
	}
	return false
}

func keySet[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func setOf(values []string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, v := range values {
		out[v] = struct{}{}
	}
	return out
}

func sortedVersions[V any](set map[string]V) []string {
	out := keySet(set)
	sort.Slice(out, func(i, j int) bool { return CompareVersions(out[i], out[j]) < 0 })
	return out
}

// JSON renders the audit as the committed artefact: indented, key-ordered by
// the struct definitions, and newline-terminated so it round-trips through
// text tooling unchanged.
func (a Audit) JSON() ([]byte, error) {
	b, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// Markdown renders the audit for docs/. It carries the same facts as the JSON
// in the same order, so the two never disagree about what was observed.
func (a Audit) Markdown() string {
	var b strings.Builder
	b.WriteString("# Source version audit\n\n")
	b.WriteString("<!-- Generated by `make gen-version-audit`. Do not edit; regenerate. -->\n\n")
	b.WriteString("How much of each source's release history the committed adapter fixtures cover.\n")
	b.WriteString("`TestKeyPathCoverage` answers whether every observed field is classified; this\n")
	b.WriteString("answers whether enough has been observed to make that answer worth much.\n\n")
	b.WriteString("A source's version kind says what its version measures. A **producer** version\n")
	b.WriteString("moves when the agent ships and is the axis a shape change travels along. A\n")
	b.WriteString("**schema** version is the on-disk format revision, which moves independently and\n")
	b.WriteString("far more slowly; stratifying by it would give one bucket on the wrong axis.\n")
	b.WriteString("**none** means the source records no version at all, so its coverage is\n")
	b.WriteString("unstratified and known to be.\n\n")
	b.WriteString("Version-stratified coverage records what has been seen, never what exists. It\n")
	b.WriteString("converts an unknown gap into a recorded one; it does not watch upstream\n")
	b.WriteString("releases.\n\n")

	b.WriteString("## Evidence base\n\n")
	b.WriteString("| Source | Version kind | Band | Distinct versions | Files | Files with a version | Records | Records with a version | Key paths |\n")
	b.WriteString("| --- | --- | --- | --- | ---: | ---: | ---: | ---: | ---: |\n")
	for _, s := range a.Sources {
		fmt.Fprintf(&b, "| `%s` | %s | %s | %d | %d | %d | %d | %d | %d |\n",
			s.Source, s.VersionKind, bandText(s.Band), len(s.Versions),
			s.Files, s.FilesWithVersion, s.Records, s.RecordsWithVersion, len(s.KeyPaths))
	}
	b.WriteString("\n## Observed versions\n\n")
	b.WriteString("| Source | Version | Files | Records |\n")
	b.WriteString("| --- | --- | ---: | ---: |\n")
	for _, s := range a.Sources {
		if len(s.Versions) == 0 {
			fmt.Fprintf(&b, "| `%s` | — | 0 | 0 |\n", s.Source)
			continue
		}
		for _, v := range s.Versions {
			fmt.Fprintf(&b, "| `%s` | `%s` | %d | %d |\n", s.Source, v.Version, v.Files, v.Records)
		}
	}
	b.WriteString("\n## Key paths by version band\n\n")
	b.WriteString("`First seen` and `last seen` are the versions of the files carrying the path. A\n")
	b.WriteString("dash means the path appears only in files that record no version, so when it\n")
	b.WriteString("entered the shape cannot be said.\n")
	for _, s := range a.Sources {
		fmt.Fprintf(&b, "\n### %s\n\n", s.Source)
		b.WriteString("| Key path | Files | First seen | Last seen | Files without a version |\n")
		b.WriteString("| --- | ---: | --- | --- | ---: |\n")
		for _, p := range s.KeyPaths {
			fmt.Fprintf(&b, "| `%s` | %d | %s | %s | %d |\n",
				p.Path, p.Files, versionText(p.FirstVersion), versionText(p.LastVersion), p.UnversionedFiles)
		}
	}
	return b.String()
}

func bandText(band *VersionBand) string {
	if band == nil {
		return "—"
	}
	if band.First == band.Last {
		return "`" + band.First + "`"
	}
	return "`" + band.First + "`–`" + band.Last + "`"
}

func versionText(v string) string {
	if v == "" {
		return "—"
	}
	return "`" + v + "`"
}

// BandViolation reports a version observed outside a source's recorded band.
type BandViolation struct {
	Source   string
	Version  string
	Band     *VersionBand
	Recorded []string
}

func (v BandViolation) Error() string {
	if v.Band == nil {
		return fmt.Sprintf("source %s: version %s observed, but the audit records no version band for this source at all (recorded versions: none). Regenerate the audit with `make gen-version-audit` and reclassify anything new.", v.Source, v.Version)
	}
	return fmt.Sprintf("source %s: version %s falls outside the recorded band %s–%s (recorded versions: %s). Regenerate the audit with `make gen-version-audit` and reclassify anything new.", v.Source, v.Version, v.Band.First, v.Band.Last, strings.Join(v.Recorded, ", "))
}

// CheckBands reports every version in observed that falls outside the band the
// audit records for its source. Vendoring a corpus from a newer release is
// meant to fail here: the band is the record of what the projection table was
// calibrated against, and moving it is the deliberate act of re-deriving that
// calibration.
func (a Audit) CheckBands(observed map[string][]string) []BandViolation {
	bySource := map[string]SourceAudit{}
	for _, s := range a.Sources {
		bySource[s.Source] = s
	}
	sources := keySet(observed)
	sort.Strings(sources)
	var out []BandViolation
	for _, source := range sources {
		s := bySource[source]
		recorded := make([]string, 0, len(s.Versions))
		for _, v := range s.Versions {
			recorded = append(recorded, v.Version)
		}
		for _, v := range sortedVersions(setOf(observed[source])) {
			if s.Band != nil && CompareVersions(v, s.Band.First) >= 0 && CompareVersions(v, s.Band.Last) <= 0 {
				continue
			}
			out = append(out, BandViolation{Source: source, Version: v, Band: s.Band, Recorded: recorded})
		}
	}
	return out
}

// ObserveVersions returns every distinct version value each source's fixtures
// carry, which is the input CheckBands compares against the recorded band.
func ObserveVersions(root string, readers map[string]VersionReader) (map[string][]string, error) {
	fixtures, err := Inventory(root)
	if err != nil {
		return nil, err
	}
	seen := map[string]map[string]struct{}{}
	for _, fx := range fixtures {
		reader, ok := readers[fx.Source]
		if !ok {
			return nil, fmt.Errorf("no version reader for source %q", fx.Source)
		}
		records, err := ReadRecords(fx.Path)
		if err != nil {
			return nil, err
		}
		for _, rec := range records {
			v := reader.VersionSignal(model.ParsedEntry{RawJSON: rec.Raw}).Value()
			if v == "" {
				continue
			}
			if seen[fx.Source] == nil {
				seen[fx.Source] = map[string]struct{}{}
			}
			seen[fx.Source][v] = struct{}{}
		}
	}
	out := map[string][]string{}
	for source, set := range seen {
		out[source] = sortedVersions(set)
	}
	return out, nil
}

// LoadAudit decodes a committed audit artefact.
func LoadAudit(data []byte) (Audit, error) {
	var a Audit
	if err := json.Unmarshal(data, &a); err != nil {
		return Audit{}, err
	}
	if a.Version != AuditVersion {
		return Audit{}, fmt.Errorf("audit is version %d, want %d", a.Version, AuditVersion)
	}
	return a, nil
}

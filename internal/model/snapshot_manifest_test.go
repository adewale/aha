package model_test

import (
	"strings"
	"testing"

	"github.com/adewale/aha/internal/model"
)

func validSnapshotManifest() model.SnapshotManifest {
	return model.SnapshotManifest{
		Schema:           model.SnapshotManifestSchema,
		RequiredFeatures: model.RequiredSnapshotFeatures(),
		MachineID:        "test-machine",
		CapturedAt:       "2026-06-09T00:00:00Z",
		CreatedBy:        "aha test",
		Source:           model.ManifestSource{HostOS: "linux"},
		Policy:           model.ManifestPolicy{PathMode: "raw", IncludeSubagents: true, IncludeImages: true, Redaction: "none-v1"},
		Adapters:         []model.ManifestAdapt{{Name: "pi", Version: "test"}},
		Files: []model.ManifestFile{
			{Source: "pi", Kind: "session", RelativePath: "sources/pi/sessions/b.jsonl", RawPath: "/home/u/.pi/agent/sessions/b.jsonl", SHA256: strings.Repeat("b", 64), Bytes: 10, SessionID: "b", CopyState: "stable"},
			{Source: "pi", Kind: "session", RelativePath: "sources/pi/sessions/a.jsonl", RawPath: "/home/u/.pi/agent/sessions/a.jsonl", SHA256: strings.Repeat("a", 64), Bytes: 5, SessionID: "a", CopyState: "stable"},
		},
	}
}

// TestSnapshotManifestEncodeIsDeterministic pins I4: equal logical states
// must encode to byte-equal canonical manifests with equal identities,
// regardless of the order files were appended in.
func TestSnapshotManifestEncodeIsDeterministic(t *testing.T) {
	m1 := validSnapshotManifest()
	m2 := validSnapshotManifest()
	m2.Files[0], m2.Files[1] = m2.Files[1], m2.Files[0]
	b1, sha1, err := model.EncodeSnapshotManifest(m1)
	if err != nil {
		t.Fatal(err)
	}
	b2, sha2, err := model.EncodeSnapshotManifest(m2)
	if err != nil {
		t.Fatal(err)
	}
	if string(b1) != string(b2) {
		t.Fatalf("permuted file order changed canonical bytes:\n%s\n----\n%s", b1, b2)
	}
	if sha1 != sha2 {
		t.Fatalf("permuted file order changed manifest identity: %s vs %s", sha1, sha2)
	}
	if !sha1.Valid() {
		t.Fatalf("encode returned invalid manifest sha %q", sha1)
	}
}

func TestSnapshotManifestV2RemainsByteAndIdentityReadable(t *testing.T) {
	manifest := validSnapshotManifest()
	manifest.Schema = model.SnapshotManifestSchemaV2
	manifest.RequiredFeatures = nil
	b, sha, err := model.EncodeSnapshotManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "required_features") {
		t.Fatalf("legacy canonical bytes gained v3 fields: %s", b)
	}
	got, gotSHA, err := model.DecodeSnapshotManifest(b)
	if err != nil {
		t.Fatal(err)
	}
	if got.Schema != model.SnapshotManifestSchemaV2 || gotSHA != sha {
		t.Fatalf("legacy round trip schema=%q sha=%s want %s", got.Schema, gotSHA, sha)
	}
}

func TestSnapshotManifestUnknownOptionalFeatureRoundTrips(t *testing.T) {
	manifest := validSnapshotManifest()
	manifest.OptionalFeatures = []string{"example.com/advisory-v1"}
	b, _, err := model.EncodeSnapshotManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := model.DecodeSnapshotManifest(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.OptionalFeatures) != 1 || got.OptionalFeatures[0] != "example.com/advisory-v1" {
		t.Fatalf("optional features=%v", got.OptionalFeatures)
	}
}

func TestSnapshotManifestRoundTrip(t *testing.T) {
	b, sha, err := model.EncodeSnapshotManifest(validSnapshotManifest())
	if err != nil {
		t.Fatal(err)
	}
	got, gotSHA, err := model.DecodeSnapshotManifest(b)
	if err != nil {
		t.Fatal(err)
	}
	if gotSHA != sha {
		t.Fatalf("decode sha %s, want %s", gotSHA, sha)
	}
	if got.MachineID != "test-machine" || len(got.Files) != 2 {
		t.Fatalf("round trip lost data: %+v", got)
	}
	if got.Files[0].RelativePath != "sources/pi/sessions/a.jsonl" {
		t.Fatalf("decoded files not in canonical order: %s first", got.Files[0].RelativePath)
	}
	// Re-encoding the decoded manifest must reproduce the exact bytes.
	b2, sha2, err := model.EncodeSnapshotManifest(got)
	if err != nil {
		t.Fatal(err)
	}
	if string(b2) != string(b) || sha2 != sha {
		t.Fatal("decode∘encode is not the identity on canonical bytes")
	}
}

// TestSnapshotManifestEncodeRejectsInvalid pins the smart-constructor
// boundary: an invalid snapshot manifest cannot be serialized, so an
// invalid manifest can never acquire an identity or reach a depot.
func TestSnapshotManifestEncodeRejectsInvalid(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*model.SnapshotManifest)
	}{
		{"wrong schema", func(m *model.SnapshotManifest) { m.Schema = "aha/bundle@1" }},
		{"missing required features", func(m *model.SnapshotManifest) { m.RequiredFeatures = nil }},
		{"unknown required feature", func(m *model.SnapshotManifest) { m.RequiredFeatures = append(m.RequiredFeatures, "future-parser-v9") }},
		{"unknown path mode", func(m *model.SnapshotManifest) { m.Policy.PathMode = "future" }},
		{"unknown redaction policy", func(m *model.SnapshotManifest) { m.Policy.Redaction = "future-v9" }},
		{"unknown copy state", func(m *model.SnapshotManifest) { m.Files[0].CopyState = "future" }},
		{"empty machine", func(m *model.SnapshotManifest) { m.MachineID = " " }},
		{"empty captured_at", func(m *model.SnapshotManifest) { m.CapturedAt = "" }},
		{"bad file sha", func(m *model.SnapshotManifest) { m.Files[0].SHA256 = "nothex" }},
		{"uppercase file sha", func(m *model.SnapshotManifest) {
			m.Files[0].SHA256 = strings.ToUpper(m.Files[0].SHA256)
		}},
		{"negative bytes", func(m *model.SnapshotManifest) { m.Files[0].Bytes = -1 }},
		{"duplicate relative path", func(m *model.SnapshotManifest) {
			m.Files[1].RelativePath = m.Files[0].RelativePath
		}},
		{"empty file source", func(m *model.SnapshotManifest) { m.Files[0].Source = "" }},
		{"unsupported kind", func(m *model.SnapshotManifest) { m.Files[0].Kind = "blob" }},
		{"session outside source prefix", func(m *model.SnapshotManifest) {
			m.Files[0].RelativePath = "sources/other/sessions/a.jsonl"
		}},
		{"path traversal", func(m *model.SnapshotManifest) {
			m.Files[0].RelativePath = "sources/pi/sessions/../../../etc/passwd"
		}},
		{"absolute path", func(m *model.SnapshotManifest) {
			m.Files[0].RelativePath = "/sources/pi/sessions/a.jsonl"
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := validSnapshotManifest()
			tc.mutate(&m)
			if _, _, err := model.EncodeSnapshotManifest(m); err == nil {
				t.Fatalf("EncodeSnapshotManifest accepted %s", tc.name)
			}
		})
	}
}

// TestSnapshotManifestDecodeRejectsNonCanonical pins that only canonical
// bytes carry a manifest identity: alternate serializations of the same
// logical manifest are rejected rather than given a second identity.
func TestSnapshotManifestDecodeRejectsNonCanonical(t *testing.T) {
	b, _, err := model.EncodeSnapshotManifest(validSnapshotManifest())
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string][]byte{
		"reindented":       []byte(strings.ReplaceAll(string(b), "  ", "\t")),
		"trailing garbage": append(append([]byte{}, b...), '\n'),
		"not json":         []byte("not json"),
	}
	for name, mutated := range cases {
		t.Run(name, func(t *testing.T) {
			if _, _, err := model.DecodeSnapshotManifest(mutated); err == nil {
				t.Fatalf("DecodeSnapshotManifest accepted %s bytes", name)
			}
		})
	}
}

func TestManifestSHA256Constructor(t *testing.T) {
	good := strings.Repeat("ab", 32)
	sha, err := model.NewManifestSHA256(good)
	if err != nil {
		t.Fatal(err)
	}
	if sha.String() != good || !sha.Valid() {
		t.Fatalf("round trip: %q", sha)
	}
	for _, bad := range []string{"", "short", strings.Repeat("A", 64), strings.Repeat("g", 64), good + "00"} {
		if _, err := model.NewManifestSHA256(bad); err == nil {
			t.Fatalf("NewManifestSHA256 accepted %q", bad)
		}
	}
	var zero model.ManifestSHA256
	if zero.Valid() {
		t.Fatal("zero ManifestSHA256 must be invalid")
	}
}

func TestBlobKeyConstructor(t *testing.T) {
	good := strings.Repeat("cd", 32)
	k, err := model.NewBlobKey(good)
	if err != nil {
		t.Fatal(err)
	}
	if k.String() != good || !k.Valid() {
		t.Fatalf("round trip: %q", k)
	}
	for _, bad := range []string{"", "short", strings.Repeat("C", 64), strings.Repeat("z", 64)} {
		if _, err := model.NewBlobKey(bad); err == nil {
			t.Fatalf("NewBlobKey accepted %q", bad)
		}
	}
	var zero model.BlobKey
	if zero.Valid() {
		t.Fatal("zero BlobKey must be invalid")
	}
}

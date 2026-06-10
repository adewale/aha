package model_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/adewale/aha/internal/model"
)

// FuzzDecodeSnapshotManifest pins the canonical-codec residual risks (the
// v2 successor of the deleted v1 depot fuzz tests): arbitrary bytes must
// never panic the decoder, and anything that decodes must be EXACTLY
// canonical — re-encoding reproduces the input byte for byte and the same
// identity, so one logical manifest can never carry two identities.
func FuzzDecodeSnapshotManifest(f *testing.F) {
	canonical, _, err := model.EncodeSnapshotManifest(model.SnapshotManifest{
		Schema:     model.SnapshotManifestSchema,
		MachineID:  "fuzz-machine",
		CapturedAt: "2026-06-10T00:00:00Z",
		Files: []model.ManifestFile{
			{Source: "pi", Kind: "session", RelativePath: "sources/pi/sessions/a.jsonl", RawPath: "/r/a.jsonl", SHA256: strings.Repeat("a", 64), Bytes: 3, SessionID: "a", CopyState: "stable"},
		},
	})
	if err != nil {
		f.Fatal(err)
	}
	f.Add(canonical)
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"schema":"aha-snapshot-manifest/v2"}`))
	f.Add([]byte(`null`))
	f.Add([]byte(``))
	f.Add(bytes.Replace(canonical, []byte("  "), []byte("\t"), 1))
	f.Fuzz(func(t *testing.T, data []byte) {
		m, sha, err := model.DecodeSnapshotManifest(data)
		if err != nil {
			return
		}
		reencoded, sha2, err := model.EncodeSnapshotManifest(m)
		if err != nil {
			t.Fatalf("decoded manifest does not re-encode: %v", err)
		}
		if !bytes.Equal(reencoded, data) {
			t.Fatalf("decoder accepted non-canonical bytes:\nin:  %q\nout: %q", data, reencoded)
		}
		if sha2 != sha {
			t.Fatalf("identity not stable across decode/encode: %s vs %s", sha, sha2)
		}
	})
}

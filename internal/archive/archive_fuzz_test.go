package archive_test

import (
	"bytes"
	"io"
	"path/filepath"
	"testing"

	"github.com/adewale/aha/internal/archive"
	"github.com/adewale/aha/internal/hash"
	"github.com/adewale/aha/internal/model"
)

func FuzzWalkBundleRoundTrip(f *testing.F) {
	f.Add("hello", "world")
	f.Add("", "needle")
	f.Fuzz(func(t *testing.T, a, b string) {
		if len(a)+len(b) > 4096 {
			t.Skip()
		}
		files := map[string][]byte{
			"sources/pi/sessions/a.jsonl": []byte(a),
			"sources/pi/sessions/b.jsonl": []byte(b),
		}
		manifest := model.Manifest{Schema: model.BundleSchema, BundleID: "fuzz", MachineID: "m1", CapturedAt: "2026-01-01T00:00:00Z", Policy: model.ManifestPolicy{IncludeImages: true}}
		var captured []model.CapturedFile
		for name, data := range files {
			mf := model.ManifestFile{Source: "pi", Kind: "session", RelativePath: name, SHA256: hash.SHA256Bytes(data), Bytes: int64(len(data)), CopyState: "stable"}
			manifest.Files = append(manifest.Files, mf)
			captured = append(captured, model.CapturedFile{Manifest: mf, Data: data})
		}
		path := filepath.Join(t.TempDir(), "bundle.tar.zst")
		if _, err := archive.Write(path, archive.Bundle{Manifest: manifest, Files: captured}); err != nil {
			t.Fatalf("Write: %v", err)
		}
		seen := map[string][]byte{}
		if err := archive.WalkBundle(path, func(name string, size int64, r io.Reader) error {
			buf := new(bytes.Buffer)
			if _, err := buf.ReadFrom(r); err != nil {
				return err
			}
			if int64(buf.Len()) != size {
				t.Fatalf("size mismatch for %s", name)
			}
			seen[name] = buf.Bytes()
			return nil
		}); err != nil {
			t.Fatalf("WalkBundle: %v", err)
		}
		for name, want := range files {
			if !bytes.Equal(seen[name], want) {
				t.Fatalf("walked bytes mismatch for %s", name)
			}
		}
	})
}

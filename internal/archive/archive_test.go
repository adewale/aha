package archive_test

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/adewale/aha/internal/adapters"
	"github.com/adewale/aha/internal/archive"
	"github.com/adewale/aha/internal/config"
	"github.com/adewale/aha/internal/hash"
	"github.com/adewale/aha/internal/model"
	"github.com/adewale/aha/internal/testutil"
	"github.com/klauspost/compress/zstd"
)

func TestCopyFileHashedLimitRejectsOversizedInput(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	if err := os.WriteFile(src, []byte("abcdef"), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(root, "dst")
	if _, err := archive.CopyFileHashedLimit(src, dst, 3); err == nil {
		t.Fatalf("expected oversized copy error")
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Fatalf("oversized copy left destination behind: %v", err)
	}
}

func TestStreamFilesRejectsSHAMismatch(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "bad-sha.tar.zst")
	mf := model.ManifestFile{Source: "pi", Kind: "session", RelativePath: "sources/pi/sessions/a.jsonl", SHA256: hash.SHA256Bytes([]byte("abc")), Bytes: 3, CopyState: "stable"}
	manifest := model.Manifest{Schema: model.BundleSchema, BundleID: "bad-sha", MachineID: "m1", CapturedAt: "2026-01-01T00:00:00Z", Policy: model.ManifestPolicy{IncludeImages: true}, Files: []model.ManifestFile{mf}}
	if err := writeTestTarZst(path, manifest, map[string][]byte{mf.RelativePath: []byte("xyz")}); err != nil {
		t.Fatal(err)
	}
	if err := archive.StreamFiles(path, func(name string, data []byte) error { return nil }); err == nil || !strings.Contains(err.Error(), "sha mismatch") {
		t.Fatalf("StreamFiles err=%v, want sha mismatch", err)
	}
}

func writeTestTarZst(path string, manifest model.Manifest, files map[string][]byte) error {
	out, err := os.Create(path)
	if err != nil {
		return err
	}
	zw, err := zstd.NewWriter(out)
	if err != nil {
		_ = out.Close()
		return err
	}
	tw := tar.NewWriter(zw)
	writeEntry := func(name string, data []byte) error {
		h := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(data)), ModTime: time.Unix(0, 0).UTC(), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(h); err != nil {
			return err
		}
		_, err := tw.Write(data)
		return err
	}
	mb, err := archive.CanonicalManifest(manifest)
	if err != nil {
		return err
	}
	if err := writeEntry("manifest.json", mb); err != nil {
		return err
	}
	for name, data := range files {
		if err := writeEntry(name, data); err != nil {
			return err
		}
	}
	if err := writeEntry("checksums/sha256sums.txt", []byte("")); err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	return out.Close()
}

func TestStreamFilesValidatesManifestFilesArePresent(t *testing.T) {
	root := t.TempDir()
	bundle := archive.Bundle{Manifest: model.Manifest{Schema: model.BundleSchema, BundleID: "missing", MachineID: "m1", CapturedAt: "2026-01-01T00:00:00Z", Policy: model.ManifestPolicy{IncludeImages: true}, Files: []model.ManifestFile{{Source: "pi", Kind: "session", RelativePath: "sources/pi/sessions/missing.jsonl", SHA256: "abc", Bytes: 3}}}}
	path := filepath.Join(root, "bundle.tar.zst")
	if _, err := archive.Write(path, bundle); err != nil {
		t.Fatal(err)
	}
	if err := archive.StreamFiles(path, func(name string, data []byte) error { return nil }); err == nil {
		t.Fatalf("StreamFiles accepted bundle missing manifest file")
	}
}

func TestCaptureSkipsSymlinkedSessions(t *testing.T) {
	root := t.TempDir()
	fx := testutil.WriteAgentFixtures(t, root)
	outside := filepath.Join(root, "outside.jsonl")
	if err := os.WriteFile(outside, []byte(`{"type":"session","id":"outside"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(fx.PiRoot, "--Users-me-proj--", "linked.jsonl")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	cfg := config.Default()
	cfg.MachineID = "m1"
	cfg.Sources = []model.SourceConfig{{Type: "pi", Root: fx.PiRoot, Enabled: true}}
	b, err := archive.Capture(t.Context(), cfg, adapters.Builtins(), archive.Options{CapturedAt: "2026-01-03T00:00:00Z", BundleID: "symlink"})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range b.Manifest.Files {
		if strings.Contains(f.RawPath, "linked.jsonl") || f.SHA256 == "9cd4" {
			t.Fatalf("captured symlinked file: %+v", f)
		}
	}
	if b.Manifest.Counts.SessionFiles != 1 {
		t.Fatalf("sessions=%d", b.Manifest.Counts.SessionFiles)
	}
}

func TestCaptureWriteDeterministicAndReadOnly(t *testing.T) {
	root := t.TempDir()
	fx := testutil.WriteAgentFixtures(t, root)
	sourceFile := filepath.Join(fx.PiRoot, "--Users-me-proj--", "2026_pi.jsonl")
	before, err := os.Stat(sourceFile)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.MachineID = "test-machine"
	cfg.Sources = []model.SourceConfig{{Type: "pi", Root: fx.PiRoot, Enabled: true}, {Type: "claude-code", Root: fx.ClaudeRoot, Enabled: true}}
	b1, err := archive.Capture(t.Context(), cfg, adapters.Builtins(), archive.Options{CapturedAt: "2026-01-03T00:00:00Z", BundleID: "fixed"})
	if err != nil {
		t.Fatal(err)
	}
	b2, err := archive.Capture(t.Context(), cfg, adapters.Builtins(), archive.Options{CapturedAt: "2026-01-03T00:00:00Z", BundleID: "fixed"})
	if err != nil {
		t.Fatal(err)
	}
	p1 := filepath.Join(root, "b1.tar.zst")
	p2 := filepath.Join(root, "b2.tar.zst")
	s1, err := archive.Write(p1, b1)
	if err != nil {
		t.Fatal(err)
	}
	s2, err := archive.Write(p2, b2)
	if err != nil {
		t.Fatal(err)
	}
	bb1, _ := os.ReadFile(p1)
	bb2, _ := os.ReadFile(p2)
	if s1 != s2 || !bytes.Equal(bb1, bb2) {
		t.Fatalf("bundle not deterministic")
	}
	after, err := os.Stat(sourceFile)
	if err != nil {
		t.Fatal(err)
	}
	if before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		t.Fatalf("snapshot modified source file")
	}
	manifest, _, _, _, err := archive.ReadBundle(p1)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.BundleID != "fixed" || manifest.Counts.SessionFiles != 3 || manifest.Counts.ImageFiles != 1 {
		t.Fatalf("bad manifest: %+v", manifest)
	}
}

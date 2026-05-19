package archive

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/adewale/aha/internal/adapters"
	"github.com/adewale/aha/internal/hash"
	"github.com/adewale/aha/internal/model"
	"github.com/adewale/aha/internal/paths"
	"github.com/klauspost/compress/zstd"
)

type Options struct {
	CapturedAt string
	BundleID   string
}

type Bundle struct {
	Manifest model.Manifest
	Files    []model.CapturedFile
}

func Capture(ctx context.Context, cfg model.Config, registry map[string]adapters.SourceAdapter, opts Options) (Bundle, error) {
	if opts.CapturedAt == "" {
		opts.CapturedAt = time.Now().UTC().Format(time.RFC3339)
	}
	if opts.BundleID == "" {
		opts.BundleID = hash.RandomID()
	}
	var sessions []model.SessionFile
	var artifacts []model.ArtifactFile
	artifactByPath := map[string]int{}
	for _, sc := range cfg.Sources {
		if !sc.Enabled {
			continue
		}
		ad, ok := registry[sc.Type]
		if !ok {
			return Bundle{}, fmt.Errorf("unknown source adapter %q", sc.Type)
		}
		root, err := paths.Expand(sc.Root)
		if err != nil {
			return Bundle{}, err
		}
		sc.Root = root
		found, err := ad.Discover(ctx, sc)
		if err != nil {
			return Bundle{}, err
		}
		for _, sf := range found {
			if sf.IsSubagent && !cfg.IncludeSubagents {
				continue
			}
			sessions = append(sessions, sf)
			if cfg.IncludeSubagents {
				as, err := ad.DiscoverArtifacts(ctx, sf)
				if err != nil {
					return Bundle{}, err
				}
				for _, artifact := range as {
					if idx, ok := artifactByPath[artifact.Path]; ok {
						if artifacts[idx].ParentHint == "" && artifact.ParentHint != "" {
							artifacts[idx].ParentHint = artifact.ParentHint
						}
						continue
					}
					artifactByPath[artifact.Path] = len(artifacts)
					artifacts = append(artifacts, artifact)
				}
			}
		}
	}
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].Path < sessions[j].Path })
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].Path < artifacts[j].Path })
	var files []model.CapturedFile
	var total int64
	imageCount := 0
	for _, sf := range sessions {
		data, state, err := StableRead(sf.Path)
		if err != nil {
			return Bundle{}, err
		}
		rel := filepath.ToSlash(filepath.Join("sources", sf.Source, "sessions", sf.RelativePath))
		mf := model.ManifestFile{Source: sf.Source, Kind: "session", RelativePath: rel, RawPath: sf.Path, SHA256: hash.SHA256Bytes(data), Bytes: int64(len(data)), SessionID: sf.SessionID, CWD: sf.CWD, StartedAt: sf.StartedAt, CopyState: state, IsSubagent: sf.IsSubagent}
		if ad := registry[sf.Source]; ad != nil {
			if ps, err := ad.ParseSession(ctx, sf, bytes.NewReader(data)); err == nil {
				mf.Entries = len(ps.Entries)
				if ps.SourceSessionID != "" {
					mf.SessionID = ps.SourceSessionID
				}
				if mf.StartedAt == "" {
					mf.StartedAt = ps.StartedAt
				}
				if mf.CWD == "" {
					mf.CWD = ps.CWD
				}
				if cfg.IncludeImages {
					for _, e := range ps.Entries {
						imageCount += len(e.Assets)
					}
				}
			}
		}
		files = append(files, model.CapturedFile{Manifest: mf, Data: data})
		total += int64(len(data))
	}
	for _, af := range artifacts {
		data, state, err := StableRead(af.Path)
		if err != nil {
			return Bundle{}, err
		}
		rel := filepath.ToSlash(filepath.Join("sources", af.Source, "artifacts", af.RelativePath))
		mf := model.ManifestFile{Source: af.Source, Kind: af.Kind, RelativePath: rel, RawPath: af.Path, SHA256: hash.SHA256Bytes(data), Bytes: int64(len(data)), CopyState: state, ParentHint: af.ParentHint}
		files = append(files, model.CapturedFile{Manifest: mf, Data: data})
		total += int64(len(data))
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Manifest.RelativePath < files[j].Manifest.RelativePath })
	manifestFiles := make([]model.ManifestFile, len(files))
	for i := range files {
		manifestFiles[i] = files[i].Manifest
	}
	var mad []model.ManifestAdapt
	names := make([]string, 0, len(registry))
	for n := range registry {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		ad := registry[name]
		mad = append(mad, model.ManifestAdapt{Name: ad.Name(), Version: ad.Version(), Capabilities: ad.Capabilities()})
	}
	m := model.Manifest{Schema: model.BundleSchema, BundleID: opts.BundleID, MachineID: cfg.MachineID, MachineLabel: cfg.MachineLabel, CapturedAt: opts.CapturedAt, CreatedBy: "aha " + model.Version, Implementation: model.Implementation{Language: "go", Archive: "tar.zst"}, Source: model.ManifestSource{HostOS: runtime.GOOS}, Policy: model.ManifestPolicy{PathMode: cfg.PathMode, IncludeSubagents: cfg.IncludeSubagents, IncludeImages: cfg.IncludeImages, IndexToolOutput: cfg.IndexToolOutput, Redaction: cfg.Redaction}, Counts: model.ManifestCounts{SessionFiles: len(sessions), ArtifactFiles: len(artifacts), ImageFiles: imageCount, BytesUncompressed: total}, Adapters: mad, Files: manifestFiles}
	return Bundle{Manifest: m, Files: files}, nil
}

func StableRead(path string) ([]byte, string, error) {
	for i := 0; i < 2; i++ {
		st1, err := os.Stat(path)
		if err != nil {
			return nil, "", err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, "", err
		}
		st2, err := os.Stat(path)
		if err != nil {
			return nil, "", err
		}
		if st1.Size() == st2.Size() && st1.ModTime().Equal(st2.ModTime()) {
			return b, "stable", nil
		}
	}
	b, err := os.ReadFile(path)
	return b, "unstable", err
}

func Write(path string, b Bundle) (string, error) {
	seenNames := map[string]bool{"manifest.json": true, "checksums/sha256sums.txt": true}
	for _, f := range b.Files {
		if seenNames[f.Manifest.RelativePath] {
			return "", fmt.Errorf("duplicate archive path %s", f.Manifest.RelativePath)
		}
		seenNames[f.Manifest.RelativePath] = true
	}
	tmp := path + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp)
	enc, err := zstd.NewWriter(out, zstd.WithEncoderLevel(zstd.SpeedDefault))
	if err != nil {
		out.Close()
		return "", err
	}
	tw := tar.NewWriter(enc)
	mb, err := CanonicalManifest(b.Manifest)
	if err != nil {
		_ = out.Close()
		return "", err
	}
	if err := addTar(tw, "manifest.json", mb, 0o644); err != nil {
		_ = out.Close()
		return "", err
	}
	var sums []string
	for _, f := range b.Files {
		if err := addTar(tw, f.Manifest.RelativePath, f.Data, 0o644); err != nil {
			_ = out.Close()
			return "", err
		}
		sums = append(sums, fmt.Sprintf("%s  %s", f.Manifest.SHA256, f.Manifest.RelativePath))
	}
	sort.Strings(sums)
	if err := addTar(tw, "checksums/sha256sums.txt", []byte(strings.Join(sums, "\n")+"\n"), 0o644); err != nil {
		_ = out.Close()
		return "", err
	}
	if err := tw.Close(); err != nil {
		_ = out.Close()
		return "", err
	}
	if err := enc.Close(); err != nil {
		_ = out.Close()
		return "", err
	}
	if err := out.Close(); err != nil {
		return "", err
	}
	compressed, err := os.ReadFile(tmp)
	if err != nil {
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		return "", err
	}
	return hash.SHA256Bytes(compressed), nil
}

func CanonicalManifest(m model.Manifest) ([]byte, error) {
	// Manifest is struct-only: encoding/json emits fields in declaration order and no map iteration is involved.
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

func addTar(tw *tar.Writer, name string, data []byte, mode int64) error {
	h := &tar.Header{Name: filepath.ToSlash(name), Mode: mode, Size: int64(len(data)), ModTime: time.Unix(0, 0).UTC(), AccessTime: time.Unix(0, 0).UTC(), ChangeTime: time.Unix(0, 0).UTC(), Uid: 0, Gid: 0, Uname: "", Gname: "", Typeflag: tar.TypeReg, Format: tar.FormatPAX}
	if err := tw.WriteHeader(h); err != nil {
		return err
	}
	_, err := tw.Write(data)
	return err
}

func ReadBundle(path string) (model.Manifest, map[string][]byte, []byte, string, error) {
	bundleBytes, err := os.ReadFile(path)
	if err != nil {
		return model.Manifest{}, nil, nil, "", err
	}
	bundleSHA := hash.SHA256Bytes(bundleBytes)
	zr, err := zstd.NewReader(bytes.NewReader(bundleBytes))
	if err != nil {
		return model.Manifest{}, nil, nil, "", err
	}
	defer zr.Close()
	tr := tar.NewReader(zr)
	entries := map[string][]byte{}
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return model.Manifest{}, nil, nil, "", err
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			return model.Manifest{}, nil, nil, "", err
		}
		if _, exists := entries[h.Name]; exists {
			return model.Manifest{}, nil, nil, "", fmt.Errorf("duplicate tar entry %s", h.Name)
		}
		entries[h.Name] = b
	}
	var manifest model.Manifest
	if err := json.Unmarshal(entries["manifest.json"], &manifest); err != nil {
		return model.Manifest{}, nil, nil, "", err
	}
	if manifest.Schema != model.BundleSchema {
		return model.Manifest{}, nil, nil, "", fmt.Errorf("unsupported schema %q", manifest.Schema)
	}
	return manifest, entries, bundleBytes, bundleSHA, nil
}

package archive

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/adewale/aha/internal/adapters"
	ahaclock "github.com/adewale/aha/internal/clock"
	"github.com/adewale/aha/internal/fileutil"
	"github.com/adewale/aha/internal/hash"
	"github.com/adewale/aha/internal/media"
	"github.com/adewale/aha/internal/model"
	"github.com/adewale/aha/internal/paths"
	"github.com/adewale/aha/internal/safety"
	"github.com/klauspost/compress/zstd"
)

type Options struct {
	CapturedAt     string
	BundleID       string
	SessionFilters []string
	MaxSessions    int
	Clock          ahaclock.Clock
}

const (
	MaxManifestBytes           int64 = 16 << 20
	MaxArchiveEntryBytes       int64 = 4 << 30
	MaxBundleBytes             int64 = 2 << 30
	MaxBundleUncompressedBytes int64 = 8 << 30
	MaxChecksumBytes           int64 = 1 << 20
	MaxManifestFiles                 = 200000
)

type Bundle struct {
	Manifest model.Manifest
	Files    []model.CapturedFile
	TempDir  string
}

func Capture(ctx context.Context, cfg model.Config, registry map[string]adapters.SourceAdapter, opts Options) (Bundle, error) {
	if opts.CapturedAt == "" {
		clk := opts.Clock
		if clk == nil {
			clk = ahaclock.RealClock{}
		}
		opts.CapturedAt = clk.Now().Format(time.RFC3339)
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
		}
	}
	sessions = filterSessions(sessions, opts)
	for _, sf := range sessions {
		ad := registry[sf.Source]
		if ad == nil {
			continue
		}
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
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].Path < sessions[j].Path })
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].Path < artifacts[j].Path })
	tmpDir, err := os.MkdirTemp("", "aha-capture-*")
	if err != nil {
		return Bundle{}, err
	}
	var files []model.CapturedFile
	success := false
	defer func() {
		if !success {
			_ = os.RemoveAll(tmpDir)
		}
	}()
	var total int64
	imageCount := 0
	for _, sf := range sessions {
		if err := safety.EnsureUnderRoot(sf.Root, sf.Path); err != nil {
			return Bundle{}, err
		}
		copyPath, sha, size, state, err := StableCopy(sf.Path, tmpDir)
		if err != nil {
			return Bundle{}, err
		}
		rel := filepath.ToSlash(filepath.Join("sources", sf.Source, "sessions", sf.RelativePath))
		mf := model.ManifestFile{Source: sf.Source, Kind: "session", RelativePath: rel, RawPath: sf.Path, SHA256: sha, Bytes: size, SessionID: sf.SessionID, CWD: sf.CWD, StartedAt: sf.StartedAt, CopyState: state, IsSubagent: sf.IsSubagent}
		if ad := registry[sf.Source]; ad != nil {
			fh, err := os.Open(copyPath)
			if err == nil {
				ps, parseErr := ad.ParseSession(ctx, sf, fh)
				_ = fh.Close()
				if parseErr == nil {
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
		}
		files = append(files, model.CapturedFile{Manifest: mf, Path: copyPath})
		total += size
	}
	for _, af := range artifacts {
		if err := safety.EnsureUnderRoot(af.Root, af.Path); err != nil {
			return Bundle{}, err
		}
		if !cfg.IncludeImages && media.FileLooksImage(af.Path) {
			continue
		}
		copyPath, sha, size, state, err := StableCopy(af.Path, tmpDir)
		if err != nil {
			return Bundle{}, err
		}
		rel := filepath.ToSlash(filepath.Join("sources", af.Source, "artifacts", af.RelativePath))
		mf := model.ManifestFile{Source: af.Source, Kind: af.Kind, RelativePath: rel, RawPath: af.Path, SHA256: sha, Bytes: size, CopyState: state, ParentHint: af.ParentHint}
		files = append(files, model.CapturedFile{Manifest: mf, Path: copyPath})
		total += size
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Manifest.RelativePath < files[j].Manifest.RelativePath })
	manifestFiles := make([]model.ManifestFile, len(files))
	artifactFileCount := 0
	for i := range files {
		manifestFiles[i] = files[i].Manifest
		if files[i].Manifest.Kind != "session" {
			artifactFileCount++
		}
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
	m := model.Manifest{Schema: model.BundleSchema, BundleID: opts.BundleID, MachineID: cfg.MachineID, MachineLabel: cfg.MachineLabel, CapturedAt: opts.CapturedAt, CreatedBy: "aha " + model.Version, Implementation: model.Implementation{Language: "go", Archive: "tar.zst"}, Source: model.ManifestSource{HostOS: runtime.GOOS}, Policy: model.ManifestPolicy{PathMode: cfg.PathMode, IncludeSubagents: cfg.IncludeSubagents, IncludeImages: cfg.IncludeImages, IndexToolOutput: cfg.IndexToolOutput, Redaction: cfg.Redaction}, Counts: model.ManifestCounts{SessionFiles: len(sessions), ArtifactFiles: artifactFileCount, ImageFiles: imageCount, BytesUncompressed: total}, Adapters: mad, Files: manifestFiles}
	success = true
	return Bundle{Manifest: m, Files: files, TempDir: tmpDir}, nil
}

func filterSessions(sessions []model.SessionFile, opts Options) []model.SessionFile {
	if len(opts.SessionFilters) > 0 {
		var filtered []model.SessionFile
		for _, s := range sessions {
			for _, f := range opts.SessionFilters {
				if sessionMatches(s, f) {
					filtered = append(filtered, s)
					break
				}
			}
		}
		sessions = filtered
	}
	if opts.MaxSessions > 0 && len(sessions) > opts.MaxSessions {
		sort.SliceStable(sessions, func(i, j int) bool {
			mi, ei := os.Stat(sessions[i].Path)
			mj, ej := os.Stat(sessions[j].Path)
			if ei == nil && ej == nil && !mi.ModTime().Equal(mj.ModTime()) {
				return mi.ModTime().After(mj.ModTime())
			}
			return sessions[i].Path < sessions[j].Path
		})
		sessions = sessions[:opts.MaxSessions]
	}
	return sessions
}

func sessionMatches(s model.SessionFile, filter string) bool {
	filter = strings.TrimSpace(filter)
	if filter == "" {
		return false
	}
	candidates := []string{s.SessionID, s.RelativePath, s.Path, filepath.Base(s.Path), strings.TrimSuffix(filepath.Base(s.Path), filepath.Ext(s.Path))}
	for _, c := range candidates {
		if c == filter || strings.Contains(c, filter) {
			return true
		}
	}
	return false
}

func openRegularNoFollow(path string) (*os.File, os.FileInfo, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, nil, err
	}
	if !st.Mode().IsRegular() {
		_ = f.Close()
		return nil, nil, fmt.Errorf("refusing to copy non-regular file: %s", path)
	}
	return f, st, nil
}

func ManifestStateSHA256(m model.Manifest) string {
	state := struct {
		Schema       string
		MachineID    string
		MachineLabel string
		Source       model.ManifestSource
		Policy       model.ManifestPolicy
		Adapters     []model.ManifestAdapt
		Files        []model.ManifestFile
	}{
		Schema:       m.Schema,
		MachineID:    m.MachineID,
		MachineLabel: m.MachineLabel,
		Source:       m.Source,
		Policy:       m.Policy,
		Adapters:     m.Adapters,
		Files:        m.Files,
	}
	b, _ := json.Marshal(state)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func StableCopy(path, dir string) (string, string, int64, string, error) {
	check := func() (os.FileInfo, error) {
		st, err := os.Lstat(path)
		if err != nil {
			return nil, err
		}
		if st.Mode()&os.ModeSymlink != 0 || !st.Mode().IsRegular() {
			return nil, fmt.Errorf("refusing to copy non-regular file: %s", path)
		}
		return st, nil
	}
	copyOnce := func(expected os.FileInfo) (string, string, int64, os.FileInfo, error) {
		out, err := os.CreateTemp(dir, "file-*")
		if err != nil {
			return "", "", 0, nil, err
		}
		outPath := out.Name()
		in, openedInfo, err := openRegularNoFollow(path)
		if err != nil {
			_ = out.Close()
			_ = os.Remove(outPath)
			return "", "", 0, nil, err
		}
		if expected != nil && !os.SameFile(expected, openedInfo) {
			_ = in.Close()
			_ = out.Close()
			_ = os.Remove(outPath)
			return "", "", 0, nil, fmt.Errorf("source file changed before open: %s", path)
		}
		h := sha256.New()
		n, copyErr := io.Copy(io.MultiWriter(out, h), in)
		closeInErr := in.Close()
		closeOutErr := out.Close()
		if copyErr != nil {
			_ = os.Remove(outPath)
			return "", "", 0, nil, copyErr
		}
		if closeInErr != nil {
			_ = os.Remove(outPath)
			return "", "", 0, nil, closeInErr
		}
		if closeOutErr != nil {
			_ = os.Remove(outPath)
			return "", "", 0, nil, closeOutErr
		}
		return outPath, hex.EncodeToString(h.Sum(nil)), n, openedInfo, nil
	}
	for i := 0; i < 2; i++ {
		st1, err := check()
		if err != nil {
			return "", "", 0, "", err
		}
		outPath, sha, n, openedInfo, err := copyOnce(st1)
		if err != nil {
			return "", "", 0, "", err
		}
		st2, err := check()
		if err != nil {
			_ = os.Remove(outPath)
			return "", "", 0, "", err
		}
		if !os.SameFile(openedInfo, st2) {
			_ = os.Remove(outPath)
			return "", "", 0, "", fmt.Errorf("source file changed during copy: %s", path)
		}
		if st1.Size() == st2.Size() && st1.ModTime().Equal(st2.ModTime()) {
			return outPath, sha, n, "stable", nil
		}
		_ = os.Remove(outPath)
	}
	if _, err := check(); err != nil {
		return "", "", 0, "", err
	}
	outPath, sha, n, _, err := copyOnce(nil)
	if err != nil {
		return "", "", 0, "", err
	}
	return outPath, sha, n, "unstable", nil
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
	b = normalizeBundleForWrite(b)
	if err := ValidateManifestSemantics(b.Manifest); err != nil {
		return "", err
	}
	if err := ValidateManifestBudgets(b.Manifest); err != nil {
		return "", err
	}
	if b.TempDir != "" {
		defer os.RemoveAll(b.TempDir)
	}
	seenNames := map[string]bool{"manifest.json": true, "checksums/sha256sums.txt": true}
	captured := map[string]model.ManifestFile{}
	for _, f := range b.Files {
		if err := validateArchiveDataPath(f.Manifest.RelativePath); err != nil {
			return "", err
		}
		if seenNames[f.Manifest.RelativePath] {
			return "", fmt.Errorf("duplicate archive path %s", f.Manifest.RelativePath)
		}
		seenNames[f.Manifest.RelativePath] = true
		captured[f.Manifest.RelativePath] = f.Manifest
	}
	for _, mf := range b.Manifest.Files {
		if err := validateArchiveDataPath(mf.RelativePath); err != nil {
			return "", err
		}
		got, ok := captured[mf.RelativePath]
		if !ok {
			return "", fmt.Errorf("manifest file has no captured data: %s", mf.RelativePath)
		}
		if got.SHA256 != mf.SHA256 || got.Bytes != mf.Bytes || got.Kind != mf.Kind || got.Source != mf.Source {
			return "", fmt.Errorf("captured metadata mismatch for %s", mf.RelativePath)
		}
	}
	if len(captured) != len(b.Manifest.Files) {
		return "", fmt.Errorf("captured file missing from manifest")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	out, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+"-*.tmp")
	if err != nil {
		return "", err
	}
	tmp := out.Name()
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
		if err := addTarCaptured(tw, f); err != nil {
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
	sha, err := FileSHA256(tmp)
	if err != nil {
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		return "", err
	}
	return sha, nil
}

func normalizeBundleForWrite(b Bundle) Bundle {
	b.Manifest.Files = append([]model.ManifestFile(nil), b.Manifest.Files...)
	sort.Slice(b.Manifest.Files, func(i, j int) bool { return b.Manifest.Files[i].RelativePath < b.Manifest.Files[j].RelativePath })
	b.Files = append([]model.CapturedFile(nil), b.Files...)
	sort.Slice(b.Files, func(i, j int) bool { return b.Files[i].Manifest.RelativePath < b.Files[j].Manifest.RelativePath })
	return b
}

func supportedBundleSchema(schema string) bool {
	return schema == model.BundleSchemaV1 || schema == model.BundleSchemaV2
}

func ValidateManifestSemantics(m model.Manifest) error {
	if !supportedBundleSchema(m.Schema) {
		return fmt.Errorf("invalid manifest: unsupported schema %q", m.Schema)
	}
	if strings.TrimSpace(m.BundleID) == "" {
		return fmt.Errorf("invalid manifest: bundle_id required")
	}
	if strings.TrimSpace(m.MachineID) == "" {
		return fmt.Errorf("invalid manifest: machine_id required")
	}
	if strings.TrimSpace(m.CapturedAt) == "" {
		return fmt.Errorf("invalid manifest: captured_at required")
	}
	for _, mf := range m.Files {
		if err := validateArchiveDataPath(mf.RelativePath); err != nil {
			return err
		}
		if strings.TrimSpace(mf.Source) == "" {
			return fmt.Errorf("invalid manifest: file source required for %s", mf.RelativePath)
		}
		switch mf.Kind {
		case "session":
			want := "sources/" + mf.Source + "/sessions/"
			if !strings.HasPrefix(mf.RelativePath, want) {
				return fmt.Errorf("invalid manifest: session path %s must be under %s", mf.RelativePath, want)
			}
		case "artifact":
			want := "sources/" + mf.Source + "/artifacts/"
			if !strings.HasPrefix(mf.RelativePath, want) {
				return fmt.Errorf("invalid manifest: artifact path %s must be under %s", mf.RelativePath, want)
			}
		default:
			return fmt.Errorf("invalid manifest: unsupported file kind %q", mf.Kind)
		}
	}
	return nil
}

func validateArchiveDataPath(name string) error {
	if name == "" || name == "." || name == ".." || path.IsAbs(name) || path.Clean(name) != name || strings.Contains(name, "\\") || strings.HasPrefix(name, "../") || strings.Contains(name, "/../") || strings.HasPrefix(name, "./") || strings.Contains(name, "/./") {
		return fmt.Errorf("unsafe archive path: %s", name)
	}
	if name == "manifest.json" || name == "checksums/sha256sums.txt" || strings.HasPrefix(name, "checksums/") {
		return fmt.Errorf("unsafe archive path: %s", name)
	}
	return nil
}

func CanonicalManifest(m model.Manifest) ([]byte, error) {
	// Manifest is struct-only: encoding/json emits fields in declaration order and no map iteration is involved.
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

func addTarCaptured(tw *tar.Writer, f model.CapturedFile) error {
	if f.Data != nil {
		return addTar(tw, f.Manifest.RelativePath, f.Data, 0o644)
	}
	h := &tar.Header{Name: filepath.ToSlash(f.Manifest.RelativePath), Mode: 0o644, Size: f.Manifest.Bytes, ModTime: time.Unix(0, 0).UTC(), AccessTime: time.Unix(0, 0).UTC(), ChangeTime: time.Unix(0, 0).UTC(), Uid: 0, Gid: 0, Uname: "", Gname: "", Typeflag: tar.TypeReg, Format: tar.FormatPAX}
	if err := tw.WriteHeader(h); err != nil {
		return err
	}
	in, err := os.Open(f.Path)
	if err != nil {
		return err
	}
	defer in.Close()
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(tw, hash), in)
	if err != nil {
		return err
	}
	if n != f.Manifest.Bytes {
		return fmt.Errorf("size mismatch while writing %s", f.Manifest.RelativePath)
	}
	if got := hex.EncodeToString(hash.Sum(nil)); got != f.Manifest.SHA256 {
		return fmt.Errorf("sha mismatch while writing %s", f.Manifest.RelativePath)
	}
	return nil
}

func addTar(tw *tar.Writer, name string, data []byte, mode int64) error {
	h := &tar.Header{Name: filepath.ToSlash(name), Mode: mode, Size: int64(len(data)), ModTime: time.Unix(0, 0).UTC(), AccessTime: time.Unix(0, 0).UTC(), ChangeTime: time.Unix(0, 0).UTC(), Uid: 0, Gid: 0, Uname: "", Gname: "", Typeflag: tar.TypeReg, Format: tar.FormatPAX}
	if err := tw.WriteHeader(h); err != nil {
		return err
	}
	_, err := tw.Write(data)
	return err
}

func FileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func CopyFileHashed(src, dst string) (string, error) {
	return CopyFileHashedLimit(src, dst, MaxBundleBytes)
}

func CopyFileHashedLimit(src, dst string, maxBytes int64) (string, error) {
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer in.Close()
	res, err := fileutil.CopyHash(dst, in, maxBytes)
	if err != nil {
		return "", err
	}
	return res.SHA256, nil
}

func CopyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(dst)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(dst)
	}
	return closeErr
}

func ReadManifest(path string) (model.Manifest, error) {
	manifest, err := readManifestOnly(path)
	if err != nil {
		return model.Manifest{}, err
	}
	if err := ValidateManifestSemantics(manifest); err != nil {
		return model.Manifest{}, err
	}
	return manifest, ValidateManifestBudgets(manifest)
}

func StreamReaders(path string, fn func(name string, size int64, r io.Reader) error) error {
	return WalkBundle(path, func(name string, size int64, r io.Reader) error {
		return fn(name, size, r)
	})
}

func StreamManifestFiles(path string, fn func(mf model.ManifestFile, r io.Reader) error) error {
	manifest, err := readManifestOnly(path)
	if err != nil {
		return err
	}
	if err := ValidateManifestSemantics(manifest); err != nil {
		return err
	}
	if err := ValidateManifestBudgets(manifest); err != nil {
		return err
	}
	return walkTarZstd(path, manifest, func(mf model.ManifestFile, r io.Reader) error {
		return fn(mf, r)
	})
}

func StreamFiles(path string, fn func(name string, data []byte) error) error {
	return WalkBundle(path, func(name string, size int64, r io.Reader) error {
		b, err := io.ReadAll(io.LimitReader(r, size+1))
		if err != nil {
			return err
		}
		if int64(len(b)) != size {
			return fmt.Errorf("short archive entry %s", name)
		}
		return fn(name, b)
	})
}

func ReadBundle(path string) (model.Manifest, map[string][]byte, []byte, string, error) {
	if st, err := os.Stat(path); err != nil {
		return model.Manifest{}, nil, nil, "", err
	} else if st.Size() > MaxBundleBytes {
		return model.Manifest{}, nil, nil, "", fmt.Errorf("bundle too large: exceeds %d bytes", MaxBundleBytes)
	}
	bundleBytes, err := os.ReadFile(path)
	if err != nil {
		return model.Manifest{}, nil, nil, "", err
	}
	bundleSHA := hash.SHA256Bytes(bundleBytes)
	manifest, err := ReadManifest(path)
	if err != nil {
		return model.Manifest{}, nil, nil, "", err
	}
	entries := map[string][]byte{}
	if err := StreamFiles(path, func(name string, data []byte) error {
		entries[name] = data
		return nil
	}); err != nil {
		return model.Manifest{}, nil, nil, "", err
	}
	return manifest, entries, bundleBytes, bundleSHA, nil
}

func WalkBundle(path string, fn func(name string, size int64, r io.Reader) error) error {
	manifest, err := readManifestOnly(path)
	if err != nil {
		return err
	}
	if err := ValidateManifestSemantics(manifest); err != nil {
		return err
	}
	if err := ValidateManifestBudgets(manifest); err != nil {
		return err
	}
	return walkTarZstd(path, manifest, func(mf model.ManifestFile, r io.Reader) error {
		return fn(mf.RelativePath, mf.Bytes, r)
	})
}

func ValidateManifestBudgets(manifest model.Manifest) error {
	if len(manifest.Files) > MaxManifestFiles {
		return fmt.Errorf("manifest has too many files: %d", len(manifest.Files))
	}
	var total int64
	for _, mf := range manifest.Files {
		if mf.Bytes < 0 {
			return fmt.Errorf("manifest file has negative size: %s", mf.RelativePath)
		}
		if mf.Bytes > MaxArchiveEntryBytes {
			return fmt.Errorf("manifest file too large: %s", mf.RelativePath)
		}
		if total > MaxBundleUncompressedBytes-mf.Bytes {
			return fmt.Errorf("bundle uncompressed size exceeds %d bytes", MaxBundleUncompressedBytes)
		}
		total += mf.Bytes
	}
	return nil
}

func readManifestOnly(path string) (model.Manifest, error) {
	f, err := os.Open(path)
	if err != nil {
		return model.Manifest{}, err
	}
	defer f.Close()
	zr, err := zstd.NewReader(f)
	if err != nil {
		return model.Manifest{}, err
	}
	defer zr.Close()
	tr := tar.NewReader(zr)
	h, err := tr.Next()
	if err != nil {
		return model.Manifest{}, err
	}
	if h.Name != "manifest.json" {
		return model.Manifest{}, fmt.Errorf("first tar entry is %s, want manifest.json", h.Name)
	}
	if h.Typeflag != tar.TypeReg {
		return model.Manifest{}, fmt.Errorf("manifest is not a regular tar entry")
	}
	if h.Size > MaxManifestBytes {
		return model.Manifest{}, fmt.Errorf("manifest too large: %d bytes", h.Size)
	}
	b, err := io.ReadAll(io.LimitReader(tr, MaxManifestBytes+1))
	if err != nil {
		return model.Manifest{}, err
	}
	if int64(len(b)) > MaxManifestBytes {
		return model.Manifest{}, fmt.Errorf("manifest too large")
	}
	var manifest model.Manifest
	if err := json.Unmarshal(b, &manifest); err != nil {
		return model.Manifest{}, err
	}
	if !supportedBundleSchema(manifest.Schema) {
		return model.Manifest{}, fmt.Errorf("unsupported schema %q", manifest.Schema)
	}
	return manifest, nil
}

func walkTarZstd(path string, manifest model.Manifest, fn func(mf model.ManifestFile, r io.Reader) error) error {
	manifestFiles := map[string]model.ManifestFile{}
	for _, mf := range manifest.Files {
		if err := validateArchiveDataPath(mf.RelativePath); err != nil {
			return err
		}
		if _, exists := manifestFiles[mf.RelativePath]; exists {
			return fmt.Errorf("duplicate manifest file path: %s", mf.RelativePath)
		}
		manifestFiles[mf.RelativePath] = mf
	}
	seen := map[string]bool{}
	var total int64
	err := walkRawTarZstd(path, func(h *tar.Header, r io.Reader) error {
		if h.Typeflag != tar.TypeReg {
			return fmt.Errorf("unsupported tar entry type for %s", h.Name)
		}
		if h.Name != "manifest.json" && h.Name != "checksums/sha256sums.txt" {
			if err := validateArchiveDataPath(h.Name); err != nil {
				return err
			}
		}
		if h.Size < 0 || h.Size > MaxArchiveEntryBytes {
			return fmt.Errorf("tar entry too large: %s", h.Name)
		}
		if total > MaxBundleUncompressedBytes-h.Size {
			return fmt.Errorf("archive uncompressed size exceeds %d bytes", MaxBundleUncompressedBytes)
		}
		total += h.Size
		if seen[h.Name] {
			return fmt.Errorf("duplicate tar entry %s", h.Name)
		}
		seen[h.Name] = true
		if h.Name == "manifest.json" {
			if h.Size > MaxManifestBytes {
				return fmt.Errorf("manifest too large: %d bytes", h.Size)
			}
			_, err := io.Copy(io.Discard, r)
			return err
		}
		if h.Name == "checksums/sha256sums.txt" {
			if h.Size > MaxChecksumBytes {
				return fmt.Errorf("checksum entry too large: %d bytes", h.Size)
			}
			_, err := io.Copy(io.Discard, r)
			return err
		}
		mf, ok := manifestFiles[h.Name]
		if !ok {
			return fmt.Errorf("archive file missing from manifest: %s", h.Name)
		}
		if h.Size != mf.Bytes {
			return fmt.Errorf("size mismatch for %s: tar=%d manifest=%d", mf.RelativePath, h.Size, mf.Bytes)
		}
		delete(manifestFiles, h.Name)
		hash := sha256.New()
		tee := io.TeeReader(r, hash)
		if err := fn(mf, tee); err != nil {
			return err
		}
		if _, err := io.Copy(io.Discard, tee); err != nil {
			return err
		}
		if got := hex.EncodeToString(hash.Sum(nil)); got != mf.SHA256 {
			return fmt.Errorf("sha mismatch for %s", mf.RelativePath)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if len(manifestFiles) > 0 {
		missing := make([]string, 0, len(manifestFiles))
		for name := range manifestFiles {
			missing = append(missing, name)
		}
		sort.Strings(missing)
		return fmt.Errorf("manifest file missing from archive: %s", missing[0])
	}
	return nil
}

func walkRawTarZstd(path string, fn func(h *tar.Header, r io.Reader) error) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	zr, err := zstd.NewReader(f)
	if err != nil {
		return err
	}
	defer zr.Close()
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if err := fn(h, tr); err != nil {
			return err
		}
	}
	return nil
}

package archive

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"time"

	"github.com/adewale/aha/internal/adapters"
	ahaclock "github.com/adewale/aha/internal/clock"
	"github.com/adewale/aha/internal/media"
	"github.com/adewale/aha/internal/model"
	"github.com/adewale/aha/internal/paths"
	"github.com/adewale/aha/internal/safety"
)

// StateOptions configures a depot v2 state capture.
type StateOptions struct {
	CapturedAt     string
	SessionFilters []string
	MaxSessions    int
	Clock          ahaclock.Clock
	// Cache is the advisory scan cache; nil means always read (--force).
	Cache *CaptureCache
}

// StateCapture is one machine's captured state for a depot v2 push: a
// snapshot manifest plus access to the bytes of any blob the manifest
// names. Files that hit the scan cache are not read at capture time;
// their bytes are produced on demand by BlobPath only if the push
// actually needs them (not carried by the parent snapshot).
//
// CaptureState never parses sessions: entry counts are ingest-derived
// corpus facts, not capture-time work (docs/depot-v2-spec.md, Phase 4).
type StateCapture struct {
	Manifest model.SnapshotManifest
	tempDir  string
	copies   map[string]string // blob sha -> temp copy (already read)
	raws     map[string]string // blob sha -> raw path (cache hit, not read)
}

// CaptureState discovers the configured sources and builds the snapshot
// manifest, reading only files the scan cache cannot vouch for.
func CaptureState(ctx context.Context, cfg model.Config, registry map[string]adapters.SourceAdapter, opts StateOptions) (*StateCapture, error) {
	if opts.CapturedAt == "" {
		clk := opts.Clock
		if clk == nil {
			clk = ahaclock.RealClock{}
		}
		opts.CapturedAt = clk.Now().Format(time.RFC3339)
	}
	sessions, artifacts, err := discoverSourceFiles(ctx, cfg, registry, opts.SessionFilters, opts.MaxSessions)
	if err != nil {
		return nil, err
	}
	tmpDir, err := os.MkdirTemp("", "aha-capture-*")
	if err != nil {
		return nil, err
	}
	sc := &StateCapture{tempDir: tmpDir, copies: map[string]string{}, raws: map[string]string{}}
	success := false
	defer func() {
		if !success {
			_ = os.RemoveAll(tmpDir)
		}
	}()
	var files []model.ManifestFile
	addFile := func(root, rawPath, rel string, mf model.ManifestFile) error {
		if err := safety.EnsureUnderRoot(root, rawPath); err != nil {
			return err
		}
		if opts.Cache != nil {
			if st, err := os.Lstat(rawPath); err == nil && st.Mode().IsRegular() {
				if sha, ok := opts.Cache.Lookup(rawPath, st); ok {
					mf.SHA256 = sha
					mf.Bytes = st.Size()
					mf.CopyState = "stable"
					sc.raws[sha] = rawPath
					files = append(files, mf)
					return nil
				}
			}
		}
		copyPath, sha, size, state, err := StableCopy(rawPath, tmpDir)
		if err != nil {
			return err
		}
		mf.SHA256 = sha
		mf.Bytes = size
		mf.CopyState = state
		sc.copies[sha] = copyPath
		files = append(files, mf)
		if opts.Cache != nil && state == "stable" {
			if st, err := os.Lstat(rawPath); err == nil && st.Mode().IsRegular() && st.Size() == size {
				opts.Cache.Record(rawPath, st, sha)
			}
		}
		return nil
	}
	for _, sf := range sessions {
		rel := filepath.ToSlash(filepath.Join("sources", sf.Source, "sessions", sf.RelativePath))
		mf := model.ManifestFile{Source: sf.Source, Kind: "session", RelativePath: rel, RawPath: sf.Path, SessionID: sf.SessionID, CWD: sf.CWD, StartedAt: sf.StartedAt, IsSubagent: sf.IsSubagent}
		if err := addFile(sf.Root, sf.Path, rel, mf); err != nil {
			return nil, err
		}
	}
	for _, af := range artifacts {
		if !cfg.IncludeImages && media.FileLooksImage(af.Path) {
			continue
		}
		rel := filepath.ToSlash(filepath.Join("sources", af.Source, "artifacts", af.RelativePath))
		mf := model.ManifestFile{Source: af.Source, Kind: af.Kind, RelativePath: rel, RawPath: af.Path, ParentHint: af.ParentHint}
		if err := addFile(af.Root, af.Path, rel, mf); err != nil {
			return nil, err
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].RelativePath < files[j].RelativePath })
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
	sc.Manifest = model.SnapshotManifest{
		Schema:       model.SnapshotManifestSchema,
		MachineID:    cfg.MachineID,
		MachineLabel: cfg.MachineLabel,
		CapturedAt:   opts.CapturedAt,
		CreatedBy:    "aha " + model.Version,
		Source:       model.ManifestSource{HostOS: runtime.GOOS},
		Policy:       model.ManifestPolicy{PathMode: cfg.PathMode, IncludeSubagents: cfg.IncludeSubagents, IncludeImages: cfg.IncludeImages, IndexToolOutput: cfg.IndexToolOutput, Redaction: cfg.Redaction},
		Adapters:     mad,
		Files:        files,
	}
	if _, _, err := model.EncodeSnapshotManifest(sc.Manifest); err != nil {
		return nil, err
	}
	success = true
	return sc, nil
}

// BlobPath returns a local file containing the uncompressed content of a
// blob this capture names, reading the source file on demand if the scan
// cache vouched for it. The on-demand read re-verifies the manifest's
// claimed hash: a file that changed since it was hashed is an error, not
// silently different bytes (the cache is advisory, never truth).
func (sc *StateCapture) BlobPath(key model.BlobKey) (string, error) {
	if p, ok := sc.copies[key.String()]; ok {
		return p, nil
	}
	raw, ok := sc.raws[key.String()]
	if !ok {
		return "", fmt.Errorf("capture does not contain blob %s", key)
	}
	copyPath, sha, _, _, err := StableCopy(raw, sc.tempDir)
	if err != nil {
		return "", err
	}
	if sha != key.String() {
		_ = os.Remove(copyPath)
		return "", fmt.Errorf("%s changed since it was hashed (content now %s, manifest claims %s); rerun the snapshot, with --force if it recurs", raw, sha, key)
	}
	sc.copies[key.String()] = copyPath
	return copyPath, nil
}

// Close removes the capture's temp files.
func (sc *StateCapture) Close() error {
	return os.RemoveAll(sc.tempDir)
}

// discoverSourceFiles runs adapter discovery for every enabled source,
// applies subagent policy and session filters, and returns sessions plus
// their artifacts, both path-sorted. Shared by v1 Capture and v2
// CaptureState. No file content is read.
func discoverSourceFiles(ctx context.Context, cfg model.Config, registry map[string]adapters.SourceAdapter, sessionFilters []string, maxSessions int) ([]model.SessionFile, []model.ArtifactFile, error) {
	var sessions []model.SessionFile
	var artifacts []model.ArtifactFile
	artifactByPath := map[string]int{}
	for _, sc := range cfg.Sources {
		if !sc.Enabled {
			continue
		}
		ad, ok := registry[sc.Type]
		if !ok {
			return nil, nil, fmt.Errorf("unknown source adapter %q", sc.Type)
		}
		root, err := paths.Expand(sc.Root)
		if err != nil {
			return nil, nil, err
		}
		sc.Root = root
		found, err := ad.Discover(ctx, sc)
		if err != nil {
			return nil, nil, err
		}
		for _, sf := range found {
			if sf.IsSubagent && !cfg.IncludeSubagents {
				continue
			}
			sessions = append(sessions, sf)
		}
	}
	sessions = filterSessions(sessions, Options{SessionFilters: sessionFilters, MaxSessions: maxSessions})
	for _, sf := range sessions {
		ad := registry[sf.Source]
		if ad == nil {
			continue
		}
		as, err := ad.DiscoverArtifacts(ctx, sf)
		if err != nil {
			return nil, nil, err
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
	return sessions, artifacts, nil
}

package main

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/klauspost/compress/zstd"
	"github.com/tailscale/hujson"
	_ "modernc.org/sqlite"
)

const version = "0.1.0"
const bundleSchema = "agent-session-snapshot-bundle/v1"

type Config struct {
	MachineID        string         `json:"machine_id"`
	MachineLabel     string         `json:"machine_label,omitempty"`
	Sources          []SourceConfig `json:"sources"`
	CorpusDir        string         `json:"corpus_dir"`
	BundleOutDir     string         `json:"bundle_out_dir"`
	PathMode         string         `json:"path_mode"`
	IncludeSubagents bool           `json:"include_subagents"`
	IncludeImages    bool           `json:"include_images"`
	IndexToolOutput  bool           `json:"index_tool_output"`
	Redaction        string         `json:"redaction"`
}

type SourceConfig struct {
	Type    string `json:"type"`
	Root    string `json:"root"`
	Enabled bool   `json:"enabled"`
}

type DefaultRoot struct{ OS, Path string }

type AdapterCapabilities struct {
	HasThreads        bool `json:"has_threads"`
	HasSubagents      bool `json:"has_subagents"`
	HasImages         bool `json:"has_images"`
	HasToolCalls      bool `json:"has_tool_calls"`
	HasStableEntryIDs bool `json:"has_stable_entry_ids"`
	CanLinkSubagents  bool `json:"can_link_subagents"`
}

type SourceAdapter interface {
	Name() string
	Version() string
	DefaultRoots() []DefaultRoot
	Capabilities() AdapterCapabilities
	Discover(ctx context.Context, config SourceConfig) ([]SessionFile, error)
	DiscoverArtifacts(ctx context.Context, session SessionFile) ([]ArtifactFile, error)
	ParseSession(ctx context.Context, file SessionFile, r io.Reader) (*ParsedSession, error)
}

type SessionFile struct {
	Source       string
	Root         string
	Path         string
	RelativePath string
	SessionID    string
	CWD          string
	StartedAt    string
	IsSubagent   bool
}

type ArtifactFile struct {
	Source       string
	Root         string
	Path         string
	RelativePath string
	Kind         string
	ParentHint   string
}

type ParsedSession struct {
	Source          string
	SourceSessionID string
	CWD             string
	StartedAt       string
	IsSubagent      bool
	Entries         []ParsedEntry
	Diagnostics     []string
	Metadata        map[string]any
}

type ParsedEntry struct {
	EntryID   string
	ParentID  string
	LineNo    int
	EntryType string
	Timestamp string
	Role      string
	RawJSON   string
	Text      string
	ToolName  string
	Command   string
	FilesJSON string
	Model     string
	Provider  string
	Tokens    int64
	Cost      float64
	Assets    []ParsedAsset
	Metadata  map[string]any
}

type ParsedAsset struct {
	AssetKind    string
	ContentIndex int
	PromptOrder  int
	RawRef       string
	MimeType     string
	Data         []byte
	Metadata     map[string]any
}

type Manifest struct {
	Schema         string          `json:"schema"`
	BundleID       string          `json:"bundle_id"`
	MachineID      string          `json:"machine_id"`
	MachineLabel   string          `json:"machine_label,omitempty"`
	CapturedAt     string          `json:"captured_at"`
	CreatedBy      string          `json:"created_by"`
	Implementation Implementation  `json:"implementation"`
	Source         ManifestSource  `json:"source"`
	Policy         ManifestPolicy  `json:"policy"`
	Counts         ManifestCounts  `json:"counts"`
	Adapters       []ManifestAdapt `json:"adapters"`
	Files          []ManifestFile  `json:"files"`
}

type Implementation struct{ Language, Archive string }
type ManifestSource struct{ HostOS, HostnameHash, UserHash string }
type ManifestPolicy struct {
	PathMode         string `json:"path_mode"`
	IncludeSubagents bool   `json:"include_subagents"`
	IncludeImages    bool   `json:"include_images"`
	IndexToolOutput  bool   `json:"index_tool_output"`
	Redaction        string `json:"redaction"`
}
type ManifestCounts struct {
	SessionFiles      int   `json:"session_files"`
	ArtifactFiles     int   `json:"artifact_files"`
	ImageFiles        int   `json:"image_files"`
	BytesUncompressed int64 `json:"bytes_uncompressed"`
}
type ManifestAdapt struct {
	Name         string              `json:"name"`
	Version      string              `json:"version"`
	Capabilities AdapterCapabilities `json:"capabilities"`
}
type ManifestFile struct {
	Source       string `json:"source"`
	Kind         string `json:"kind"`
	RelativePath string `json:"relative_path"`
	RawPath      string `json:"raw_path"`
	SHA256       string `json:"sha256"`
	Bytes        int64  `json:"bytes"`
	SessionID    string `json:"session_id,omitempty"`
	CWD          string `json:"cwd,omitempty"`
	StartedAt    string `json:"started_at,omitempty"`
	Entries      int    `json:"entries,omitempty"`
	CopyState    string `json:"copy_state"`
	IsSubagent   bool   `json:"is_subagent,omitempty"`
}

type capturedFile struct {
	manifest ManifestFile
	data     []byte
}

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		usage(stdout)
		return nil
	}
	switch args[0] {
	case "snapshot":
		return cmdSnapshot(args[1:], stdout, stderr)
	case "ingest":
		return cmdIngest(args[1:], stdout, stderr)
	case "search":
		return cmdSearch(args[1:], stdout, stderr)
	case "read":
		return cmdRead(args[1:], stdout, stderr)
	case "status":
		return cmdStatus(args[1:], stdout, stderr)
	case "conflicts":
		return cmdConflicts(args[1:], stdout, stderr)
	case "doctor":
		return cmdDoctor(args[1:], stdout, stderr)
	case "init":
		return cmdInit(args[1:], stdout, stderr)
	case "version", "--version", "-v":
		fmt.Fprintln(stdout, "aha", version)
		return nil
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func usage(w io.Writer) {
	fmt.Fprintf(w, `aha %s

Usage:
  aha snapshot --machine <id> [--source pi=PATH] [--source claude-code=PATH] [--out DIR]
  aha ingest <bundle.tar.zst> [...]
  aha search <query> [--source NAME] [--machine ID] [--role ROLE] [--after DATE] [--before DATE] [--path TEXT] [--json]
  aha read --session ID [--entry ID] [--before N] [--after N] [--json]
  aha status [--json]
  aha conflicts [--json]
  aha doctor
  aha init [--config PATH]
`, version)
}

func adapters() map[string]SourceAdapter {
	return map[string]SourceAdapter{
		"pi":          piAdapter{},
		"claude-code": claudeAdapter{},
	}
}

func defaultConfig() Config {
	return Config{
		Sources: []SourceConfig{
			{Type: "pi", Root: "~/.pi/agent/sessions", Enabled: true},
			{Type: "claude-code", Root: "~/.claude/projects", Enabled: true},
		},
		CorpusDir:        "~/.aha",
		BundleOutDir:     "~/agent-session-bundles",
		PathMode:         "raw",
		IncludeSubagents: true,
		IncludeImages:    true,
		IndexToolOutput:  false,
		Redaction:        "none-v1",
	}
}

func cmdInit(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", defaultConfigPath(), "config path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg := defaultConfig()
	b, _ := json.MarshalIndent(cfg, "", "  ")
	content := "// aha config (JSONC)\n// Set machine_id before running snapshot.\n" + string(b) + "\n"
	path, err := expandPath(*configPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("config already exists: %s", path)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return err
	}
	fmt.Fprintln(stdout, path)
	return nil
}

func cmdSnapshot(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("snapshot", flag.ContinueOnError)
	fs.SetOutput(stderr)
	machine := fs.String("machine", "", "machine id")
	outDir := fs.String("out", "", "output directory")
	configPath := fs.String("config", "", "JSONC config path")
	acceptSecrets := fs.Bool("accept-secrets", false, "acknowledge v1 does not redact secrets")
	capturedAtFlag := fs.String("captured-at", "", "capture timestamp (test/determinism)")
	bundleIDFlag := fs.String("bundle-id", "", "bundle id (test/determinism)")
	var sourceFlags multiFlag
	fs.Var(&sourceFlags, "source", "source spec type=path (repeatable)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	if *machine != "" {
		cfg.MachineID = *machine
	}
	if *outDir != "" {
		cfg.BundleOutDir = *outDir
	}
	if len(sourceFlags) > 0 {
		cfg.Sources = nil
		for _, sf := range sourceFlags {
			parts := strings.SplitN(sf, "=", 2)
			if len(parts) != 2 {
				return fmt.Errorf("invalid --source %q, want type=path", sf)
			}
			cfg.Sources = append(cfg.Sources, SourceConfig{Type: parts[0], Root: parts[1], Enabled: true})
		}
	}
	if cfg.MachineID == "" {
		return errors.New("machine_id required: set config machine_id or pass --machine")
	}
	if !*acceptSecrets {
		fmt.Fprintln(stderr, "V1 does not redact secrets. Bundles may contain prompts, source code, tool output, images, tokens, and private paths. Treat the bundle as private. Pass --accept-secrets to continue.")
		return errors.New("secrets warning not acknowledged")
	}
	out, err := expandPath(cfg.BundleOutDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	capturedAt := time.Now().UTC().Format(time.RFC3339)
	if *capturedAtFlag != "" {
		capturedAt = *capturedAtFlag
	}
	bundleID := randomID()
	if *bundleIDFlag != "" {
		bundleID = *bundleIDFlag
	}

	manifest, files, err := capture(context.Background(), cfg, capturedAt, bundleID)
	if err != nil {
		return err
	}
	name := fmt.Sprintf("aha-sessions-%s-%s-%s.tar.zst", safeName(cfg.MachineID), safeTime(capturedAt), safeName(bundleID))
	bundlePath := filepath.Join(out, name)
	sha, err := writeBundle(bundlePath, manifest, files)
	if err != nil {
		return err
	}
	receipt := map[string]any{"bundle": bundlePath, "sha256": sha, "bundle_id": bundleID, "captured_at": capturedAt}
	rb, _ := json.MarshalIndent(receipt, "", "  ")
	_ = os.WriteFile(bundlePath+".receipt.json", append(rb, '\n'), 0o644)
	fmt.Fprintf(stdout, "%s\nsha256:%s\n", bundlePath, sha)
	return nil
}

func capture(ctx context.Context, cfg Config, capturedAt, bundleID string) (Manifest, []capturedFile, error) {
	ads := adapters()
	var sessions []SessionFile
	var artifacts []ArtifactFile
	for _, sc := range cfg.Sources {
		if !sc.Enabled {
			continue
		}
		ad, ok := ads[sc.Type]
		if !ok {
			return Manifest{}, nil, fmt.Errorf("unknown source adapter %q", sc.Type)
		}
		sc.Root, _ = expandPath(sc.Root)
		found, err := ad.Discover(ctx, sc)
		if err != nil {
			return Manifest{}, nil, err
		}
		sessions = append(sessions, found...)
		for _, sf := range found {
			as, err := ad.DiscoverArtifacts(ctx, sf)
			if err != nil {
				return Manifest{}, nil, err
			}
			artifacts = append(artifacts, as...)
		}
	}
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].Path < sessions[j].Path })
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].Path < artifacts[j].Path })

	var files []capturedFile
	var total int64
	for _, sf := range sessions {
		data, state, err := stableRead(sf.Path)
		if err != nil {
			return Manifest{}, nil, err
		}
		rel := filepath.ToSlash(filepath.Join("sources", sf.Source, "sessions", sf.RelativePath))
		mf := ManifestFile{Source: sf.Source, Kind: "session", RelativePath: rel, RawPath: sf.Path, SHA256: shaBytes(data), Bytes: int64(len(data)), SessionID: sf.SessionID, CWD: sf.CWD, StartedAt: sf.StartedAt, CopyState: state, IsSubagent: sf.IsSubagent}
		if ad := ads[sf.Source]; ad != nil {
			if ps, err := ad.ParseSession(ctx, sf, bytes.NewReader(data)); err == nil {
				mf.Entries = len(ps.Entries)
				if mf.StartedAt == "" {
					mf.StartedAt = ps.StartedAt
				}
				if mf.CWD == "" {
					mf.CWD = ps.CWD
				}
			}
		}
		files = append(files, capturedFile{mf, data})
		total += int64(len(data))
	}
	for _, af := range artifacts {
		data, state, err := stableRead(af.Path)
		if err != nil {
			return Manifest{}, nil, err
		}
		rel := filepath.ToSlash(filepath.Join("sources", af.Source, "artifacts", af.RelativePath))
		mf := ManifestFile{Source: af.Source, Kind: af.Kind, RelativePath: rel, RawPath: af.Path, SHA256: shaBytes(data), Bytes: int64(len(data)), CopyState: state}
		files = append(files, capturedFile{mf, data})
		total += int64(len(data))
	}
	sort.Slice(files, func(i, j int) bool { return files[i].manifest.RelativePath < files[j].manifest.RelativePath })
	mf := make([]ManifestFile, len(files))
	for i := range files {
		mf[i] = files[i].manifest
	}
	var mad []ManifestAdapt
	for _, name := range []string{"claude-code", "pi"} {
		if ad, ok := ads[name]; ok {
			mad = append(mad, ManifestAdapt{Name: ad.Name(), Version: ad.Version(), Capabilities: ad.Capabilities()})
		}
	}
	manifest := Manifest{Schema: bundleSchema, BundleID: bundleID, MachineID: cfg.MachineID, MachineLabel: cfg.MachineLabel, CapturedAt: capturedAt, CreatedBy: "aha " + version, Implementation: Implementation{"go", "tar.zst"}, Source: ManifestSource{HostOS: runtimeOS()}, Policy: ManifestPolicy{cfg.PathMode, cfg.IncludeSubagents, cfg.IncludeImages, cfg.IndexToolOutput, cfg.Redaction}, Counts: ManifestCounts{SessionFiles: len(sessions), ArtifactFiles: len(artifacts), BytesUncompressed: total}, Adapters: mad, Files: mf}
	return manifest, files, nil
}

func writeBundle(path string, manifest Manifest, files []capturedFile) (string, error) {
	var raw bytes.Buffer
	tw := tar.NewWriter(&raw)
	mb, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", err
	}
	mb = append(mb, '\n')
	if err := addTar(tw, "manifest.json", mb, 0o644); err != nil {
		return "", err
	}
	var sums []string
	for _, f := range files {
		if err := addTar(tw, f.manifest.RelativePath, f.data, 0o644); err != nil {
			return "", err
		}
		sums = append(sums, fmt.Sprintf("%s  %s", f.manifest.SHA256, f.manifest.RelativePath))
	}
	sort.Strings(sums)
	if err := addTar(tw, "checksums/sha256sums.txt", []byte(strings.Join(sums, "\n")+"\n"), 0o644); err != nil {
		return "", err
	}
	if err := tw.Close(); err != nil {
		return "", err
	}
	var compressed bytes.Buffer
	enc, err := zstd.NewWriter(&compressed, zstd.WithEncoderLevel(zstd.SpeedDefault))
	if err != nil {
		return "", err
	}
	if _, err := enc.Write(raw.Bytes()); err != nil {
		return "", err
	}
	if err := enc.Close(); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, compressed.Bytes(), 0o644); err != nil {
		return "", err
	}
	return shaBytes(compressed.Bytes()), nil
}

func addTar(tw *tar.Writer, name string, data []byte, mode int64) error {
	h := &tar.Header{Name: filepath.ToSlash(name), Mode: mode, Size: int64(len(data)), ModTime: time.Unix(0, 0).UTC(), AccessTime: time.Unix(0, 0).UTC(), ChangeTime: time.Unix(0, 0).UTC(), Uid: 0, Gid: 0, Uname: "", Gname: "", Typeflag: tar.TypeReg, Format: tar.FormatPAX}
	if err := tw.WriteHeader(h); err != nil {
		return err
	}
	_, err := tw.Write(data)
	return err
}

func cmdIngest(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("ingest", flag.ContinueOnError)
	fs.SetOutput(stderr)
	corpus := fs.String("corpus", "", "corpus dir")
	configPath := fs.String("config", "", "config path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	if *corpus != "" {
		cfg.CorpusDir = *corpus
	}
	if fs.NArg() == 0 {
		return errors.New("ingest requires at least one bundle")
	}
	db, root, err := openCorpus(cfg.CorpusDir)
	if err != nil {
		return err
	}
	defer db.Close()
	for _, pattern := range fs.Args() {
		matches, _ := filepath.Glob(pattern)
		if len(matches) == 0 {
			matches = []string{pattern}
		}
		for _, path := range matches {
			rep, err := ingestBundle(db, root, path)
			if err != nil {
				return err
			}
			fmt.Fprintf(stdout, "%s: sessions=%d entries=%d messages=%d images=%d duplicate=%v\n", path, rep.Sessions, rep.Entries, rep.Messages, rep.Images, rep.Duplicate)
		}
	}
	return nil
}

type ingestReport struct {
	Sessions, Entries, Messages, Images int
	Duplicate                           bool
}

func ingestBundle(db *sql.DB, root, path string) (ingestReport, error) {
	bundleBytes, err := os.ReadFile(path)
	if err != nil {
		return ingestReport{}, err
	}
	bundleSHA := shaBytes(bundleBytes)
	zr, err := zstd.NewReader(bytes.NewReader(bundleBytes))
	if err != nil {
		return ingestReport{}, err
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
			return ingestReport{}, err
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			return ingestReport{}, err
		}
		entries[h.Name] = b
	}
	var manifest Manifest
	if err := json.Unmarshal(entries["manifest.json"], &manifest); err != nil {
		return ingestReport{}, fmt.Errorf("manifest: %w", err)
	}
	if manifest.Schema != bundleSchema {
		return ingestReport{}, fmt.Errorf("unsupported schema %q", manifest.Schema)
	}
	if err := initSchema(db); err != nil {
		return ingestReport{}, err
	}
	tx, err := db.Begin()
	if err != nil {
		return ingestReport{}, err
	}
	defer tx.Rollback()
	var exists int
	_ = tx.QueryRow(`select count(*) from bundles where bundle_id=? or bundle_sha256=?`, manifest.BundleID, bundleSHA).Scan(&exists)
	dup := exists > 0
	if _, err := tx.Exec(`insert or ignore into bundles(bundle_id,bundle_sha256,machine_id,captured_at,ingested_at,manifest_json) values(?,?,?,?,?,?)`, manifest.BundleID, bundleSHA, manifest.MachineID, manifest.CapturedAt, time.Now().UTC().Format(time.RFC3339), string(entries["manifest.json"])); err != nil {
		return ingestReport{}, err
	}
	if _, err := tx.Exec(`insert into ingest_attempts(bundle_id,bundle_sha256,ingested_at,duplicate) values(?,?,?,?)`, manifest.BundleID, bundleSHA, time.Now().UTC().Format(time.RFC3339), boolInt(dup)); err != nil {
		return ingestReport{}, err
	}
	if err := os.MkdirAll(filepath.Join(root, "blobs", "bundles"), 0o755); err != nil {
		return ingestReport{}, err
	}
	if err := os.WriteFile(filepath.Join(root, "blobs", "bundles", bundleSHA+".tar.zst"), bundleBytes, 0o644); err != nil {
		return ingestReport{}, err
	}
	_, _ = tx.Exec(`insert into machines(machine_id,first_seen_at,last_seen_at,labels_json) values(?,?,?,?) on conflict(machine_id) do update set last_seen_at=excluded.last_seen_at`, manifest.MachineID, manifest.CapturedAt, manifest.CapturedAt, mustJSON(map[string]any{"label": manifest.MachineLabel}))
	for _, ad := range manifest.Adapters {
		_, _ = tx.Exec(`insert or replace into sources(source_name,adapter_version,capabilities_json) values(?,?,?)`, ad.Name, ad.Version, mustJSON(ad.Capabilities))
	}
	ads := adapters()
	rep := ingestReport{Duplicate: dup}
	for _, mf := range manifest.Files {
		data, ok := entries[mf.RelativePath]
		if !ok {
			return ingestReport{}, fmt.Errorf("manifest file missing from archive: %s", mf.RelativePath)
		}
		if shaBytes(data) != mf.SHA256 {
			return ingestReport{}, fmt.Errorf("sha mismatch for %s", mf.RelativePath)
		}
		if err := storeFileBlob(root, mf.SHA256, data); err != nil {
			return ingestReport{}, err
		}
		_, err := tx.Exec(`insert or ignore into files(file_sha256,kind,bytes,compressed_blob_path,first_seen_bundle_id) values(?,?,?,?,?)`, mf.SHA256, mf.Kind, mf.Bytes, filepath.ToSlash(filepath.Join("blobs", "files", mf.SHA256+".zst")), manifest.BundleID)
		if err != nil {
			return ingestReport{}, err
		}
		if mf.Kind != "session" {
			if mf.Kind == "artifact" {
				preview := ""
				if utf8.Valid(data) {
					preview = string(data)
					if len(preview) > 4000 {
						preview = preview[:4000]
					}
				}
				res, err := tx.Exec(`insert or ignore into artifacts(artifact_sha256,source_name,machine_id,bundle_id,kind,parent_session_key,parent_entry_id,raw_path,relative_path,text_preview) values(?,?,?,?,?,?,?,?,?,?)`, mf.SHA256, mf.Source, manifest.MachineID, manifest.BundleID, mf.Kind, nil, nil, mf.RawPath, mf.RelativePath, preview)
				if err != nil {
					return ingestReport{}, err
				}
				if preview != "" {
					if n, _ := res.RowsAffected(); n > 0 {
						_, err = tx.Exec(`insert into fts_artifacts(artifact_sha256,text) values(?,?)`, mf.SHA256, preview)
						if err != nil {
							return ingestReport{}, err
						}
					}
				}
			}
			continue
		}
		ad := ads[mf.Source]
		if ad == nil {
			continue
		}
		ps, err := ad.ParseSession(context.Background(), SessionFile{Source: mf.Source, Path: mf.RawPath, RelativePath: strings.TrimPrefix(mf.RelativePath, "sources/"+mf.Source+"/sessions/"), SessionID: mf.SessionID, CWD: mf.CWD, StartedAt: mf.StartedAt, IsSubagent: mf.IsSubagent}, bytes.NewReader(data))
		if err != nil {
			return ingestReport{}, err
		}
		sessionID := firstNonEmpty(ps.SourceSessionID, mf.SessionID, strings.TrimSuffix(filepath.Base(mf.RelativePath), filepath.Ext(mf.RelativePath)))
		sessionKey := mf.Source + ":" + manifest.MachineID + ":" + sessionID
		_, err = tx.Exec(`insert or ignore into sessions(session_key,source_name,source_session_id,machine_id,raw_cwd,project_key,started_at,source_metadata_json,is_subagent,parent_session_key) values(?,?,?,?,?,?,?,?,?,?)`, sessionKey, mf.Source, sessionID, manifest.MachineID, firstNonEmpty(ps.CWD, mf.CWD), projectKey(firstNonEmpty(ps.CWD, mf.CWD)), firstNonEmpty(ps.StartedAt, mf.StartedAt), mustJSON(ps.Metadata), boolInt(ps.IsSubagent || mf.IsSubagent), nil)
		if err != nil {
			return ingestReport{}, err
		}
		_, err = tx.Exec(`insert or ignore into session_versions(session_key,file_sha256,bundle_id,relative_path,raw_path,observed_at,copy_state) values(?,?,?,?,?,?,?)`, sessionKey, mf.SHA256, manifest.BundleID, mf.RelativePath, mf.RawPath, manifest.CapturedAt, mf.CopyState)
		if err != nil {
			return ingestReport{}, err
		}
		rep.Sessions++
		for _, pe := range ps.Entries {
			eh := shaBytes([]byte(pe.RawJSON))
			var oldHash string
			err := tx.QueryRow(`select entry_sha256 from entries where session_key=? and entry_id=?`, sessionKey, pe.EntryID).Scan(&oldHash)
			if err == nil && oldHash != eh {
				_, _ = tx.Exec(`insert into conflicts(session_key,entry_id,first_entry_sha256,second_entry_sha256,details_json) values(?,?,?,?,?)`, sessionKey, pe.EntryID, oldHash, eh, mustJSON(map[string]any{"bundle_id": manifest.BundleID}))
				continue
			}
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return ingestReport{}, err
			}
			res, err := tx.Exec(`insert or ignore into entries(session_key,entry_id,parent_id,line_no,entry_type,timestamp,role,entry_sha256,raw_json,source_metadata_json) values(?,?,?,?,?,?,?,?,?,?)`, sessionKey, pe.EntryID, pe.ParentID, pe.LineNo, pe.EntryType, pe.Timestamp, pe.Role, eh, pe.RawJSON, mustJSON(pe.Metadata))
			if err != nil {
				return ingestReport{}, err
			}
			if n, _ := res.RowsAffected(); n > 0 {
				rep.Entries++
			}
			if strings.TrimSpace(pe.Text) != "" {
				res, err := tx.Exec(`insert or ignore into messages(session_key,entry_id,role,text,tool_name,command,files_json,model,provider,tokens,cost) values(?,?,?,?,?,?,?,?,?,?,?)`, sessionKey, pe.EntryID, pe.Role, pe.Text, pe.ToolName, pe.Command, pe.FilesJSON, pe.Model, pe.Provider, pe.Tokens, pe.Cost)
				if err != nil {
					return ingestReport{}, err
				}
				if n, _ := res.RowsAffected(); n > 0 {
					_, err = tx.Exec(`insert into fts_messages(session_key,entry_id,text) values(?,?,?)`, sessionKey, pe.EntryID, pe.Text)
					if err != nil {
						return ingestReport{}, err
					}
					rep.Messages++
				}
			}
			for _, asset := range pe.Assets {
				assetSHA := ""
				if len(asset.Data) > 0 {
					assetSHA = shaBytes(asset.Data)
					ext := extFromMime(asset.MimeType)
					blobPath := filepath.ToSlash(filepath.Join("blobs", "images", assetSHA+ext))
					if err := os.MkdirAll(filepath.Join(root, "blobs", "images"), 0o755); err != nil {
						return ingestReport{}, err
					}
					if err := os.WriteFile(filepath.Join(root, blobPath), asset.Data, 0o644); err != nil {
						return ingestReport{}, err
					}
					_, err = tx.Exec(`insert or ignore into images(image_sha256,source_name,mime_type,bytes,width,height,ext,blob_path) values(?,?,?,?,?,?,?,?)`, assetSHA, mf.Source, asset.MimeType, len(asset.Data), 0, 0, ext, blobPath)
					if err != nil {
						return ingestReport{}, err
					}
					rep.Images++
				} else {
					assetSHA = shaBytes([]byte(asset.RawRef))
				}
				_, err = tx.Exec(`insert or ignore into entry_assets(session_key,entry_id,asset_sha256,asset_kind,content_index,prompt_order,raw_ref,mime_type,metadata_json) values(?,?,?,?,?,?,?,?,?)`, sessionKey, pe.EntryID, assetSHA, asset.AssetKind, asset.ContentIndex, asset.PromptOrder, asset.RawRef, asset.MimeType, mustJSON(asset.Metadata))
				if err != nil {
					return ingestReport{}, err
				}
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return ingestReport{}, err
	}
	return rep, nil
}

func cmdSearch(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	fs.SetOutput(stderr)
	corpus := fs.String("corpus", "", "corpus dir")
	configPath := fs.String("config", "", "config path")
	source := fs.String("source", "", "source filter")
	machine := fs.String("machine", "", "machine filter")
	role := fs.String("role", "", "role filter")
	after := fs.String("after", "", "after date")
	before := fs.String("before", "", "before date")
	pathFilter := fs.String("path", "", "path/cwd filter")
	jsonOut := fs.Bool("json", false, "JSON output")
	limit := fs.Int("limit", 20, "limit")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return errors.New("search requires query")
	}
	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	if *corpus != "" {
		cfg.CorpusDir = *corpus
	}
	db, _, err := openCorpus(cfg.CorpusDir)
	if err != nil {
		return err
	}
	defer db.Close()
	q := ftsQuery(strings.Join(fs.Args(), " "))
	where := []string{"fts_messages match ?"}
	vals := []any{q}
	if *source != "" {
		where = append(where, "s.source_name=?")
		vals = append(vals, *source)
	}
	if *machine != "" {
		where = append(where, "s.machine_id=?")
		vals = append(vals, *machine)
	}
	if *role != "" {
		where = append(where, "m.role=?")
		vals = append(vals, *role)
	}
	if *after != "" {
		where = append(where, "e.timestamp>=?")
		vals = append(vals, *after)
	}
	if *before != "" {
		where = append(where, "e.timestamp<=?")
		vals = append(vals, *before)
	}
	if *pathFilter != "" {
		where = append(where, "s.raw_cwd like ?")
		vals = append(vals, "%"+*pathFilter+"%")
	}
	vals = append(vals, *limit)
	query := `select bm25(fts_messages) score,e.timestamp,s.source_name,s.machine_id,coalesce(s.raw_cwd,''),m.role,snippet(fts_messages,2,'[',']','…',12),m.session_key,m.entry_id from fts_messages join messages m on m.session_key=fts_messages.session_key and m.entry_id=fts_messages.entry_id join sessions s on s.session_key=m.session_key join entries e on e.session_key=m.session_key and e.entry_id=m.entry_id where ` + strings.Join(where, " and ") + ` order by score limit ?`
	rows, err := db.Query(query, vals...)
	if err != nil {
		return err
	}
	defer rows.Close()
	type result struct {
		Score                                                                   float64 `json:"score"`
		Timestamp, Source, Machine, Project, Role, Snippet, SessionKey, EntryID string
	}
	var results []result
	for rows.Next() {
		var r result
		if err := rows.Scan(&r.Score, &r.Timestamp, &r.Source, &r.Machine, &r.Project, &r.Role, &r.Snippet, &r.SessionKey, &r.EntryID); err != nil {
			return err
		}
		results = append(results, r)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if *role == "" || *role == "artifact" {
		awhere := []string{"fts_artifacts match ?"}
		avals := []any{q}
		if *source != "" {
			awhere = append(awhere, "a.source_name=?")
			avals = append(avals, *source)
		}
		if *machine != "" {
			awhere = append(awhere, "a.machine_id=?")
			avals = append(avals, *machine)
		}
		if *pathFilter != "" {
			awhere = append(awhere, "a.raw_path like ?")
			avals = append(avals, "%"+*pathFilter+"%")
		}
		avals = append(avals, *limit)
		arows, err := db.Query(`select bm25(fts_artifacts) score,a.source_name,a.machine_id,a.raw_path,snippet(fts_artifacts,1,'[',']','…',12),a.artifact_sha256 from fts_artifacts join artifacts a on a.artifact_sha256=fts_artifacts.artifact_sha256 where `+strings.Join(awhere, " and ")+` order by score limit ?`, avals...)
		if err != nil {
			return err
		}
		defer arows.Close()
		for arows.Next() {
			var r result
			if err := arows.Scan(&r.Score, &r.Source, &r.Machine, &r.Project, &r.Snippet, &r.EntryID); err != nil {
				return err
			}
			r.Role = "artifact"
			results = append(results, r)
		}
		if err := arows.Err(); err != nil {
			return err
		}
	}
	if len(results) > *limit {
		results = results[:*limit]
	}
	if *jsonOut {
		return writeJSON(stdout, results)
	}
	for _, r := range results {
		fmt.Fprintf(stdout, "%.4f %s %s %s %s %s %s %s %s\n", r.Score, r.Timestamp, r.Source, r.Machine, shortProject(r.Project), r.Role, r.Snippet, r.SessionKey, r.EntryID)
	}
	return nil
}

func cmdRead(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("read", flag.ContinueOnError)
	fs.SetOutput(stderr)
	corpus := fs.String("corpus", "", "corpus dir")
	configPath := fs.String("config", "", "config path")
	session := fs.String("session", "", "session key/id")
	entry := fs.String("entry", "", "entry id")
	before := fs.Int("before", 3, "entries before")
	after := fs.Int("after", 5, "entries after")
	jsonOut := fs.Bool("json", false, "JSON output")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *session == "" {
		return errors.New("--session required")
	}
	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	if *corpus != "" {
		cfg.CorpusDir = *corpus
	}
	db, _, err := openCorpus(cfg.CorpusDir)
	if err != nil {
		return err
	}
	defer db.Close()
	var sk string
	if err := db.QueryRow(`select session_key from sessions where session_key like ? or source_session_id like ? order by started_at limit 1`, "%"+*session+"%", "%"+*session+"%").Scan(&sk); err != nil {
		return err
	}
	center := 1
	if *entry != "" {
		_ = db.QueryRow(`select line_no from entries where session_key=? and entry_id like ? order by line_no limit 1`, sk, "%"+*entry+"%").Scan(&center)
	}
	rows, err := db.Query(`select e.line_no,e.entry_id,e.timestamp,e.role,coalesce(m.text,''),e.raw_json from entries e left join messages m on m.session_key=e.session_key and m.entry_id=e.entry_id where e.session_key=? and e.line_no between ? and ? order by e.line_no`, sk, center-*before, center+*after)
	if err != nil {
		return err
	}
	defer rows.Close()
	type outEntry struct {
		LineNo                                  int `json:"line_no"`
		EntryID, Timestamp, Role, Text, RawJSON string
	}
	var out []outEntry
	for rows.Next() {
		var e outEntry
		if err := rows.Scan(&e.LineNo, &e.EntryID, &e.Timestamp, &e.Role, &e.Text, &e.RawJSON); err != nil {
			return err
		}
		out = append(out, e)
	}
	if *jsonOut {
		return writeJSON(stdout, out)
	}
	for _, e := range out {
		fmt.Fprintf(stdout, "--- %d %s %s %s ---\n%s\n", e.LineNo, e.EntryID, e.Timestamp, e.Role, firstNonEmpty(e.Text, e.RawJSON))
	}
	return rows.Err()
}

func cmdStatus(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	corpus := fs.String("corpus", "", "corpus dir")
	configPath := fs.String("config", "", "config path")
	jsonOut := fs.Bool("json", false, "JSON output")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	if *corpus != "" {
		cfg.CorpusDir = *corpus
	}
	db, root, err := openCorpus(cfg.CorpusDir)
	if err != nil {
		return err
	}
	defer db.Close()
	stats := map[string]any{"corpus_dir": root}
	for _, table := range []string{"machines", "bundles", "sources", "files", "sessions", "session_versions", "entries", "messages", "artifacts", "images", "entry_assets", "conflicts", "fts_messages", "fts_artifacts"} {
		var n int
		_ = db.QueryRow("select count(*) from " + table).Scan(&n)
		stats[table] = n
	}
	var pageCount, pageSize int64
	_ = db.QueryRow(`pragma page_count`).Scan(&pageCount)
	_ = db.QueryRow(`pragma page_size`).Scan(&pageSize)
	stats["index_size_bytes"] = pageCount * pageSize
	if *jsonOut {
		return writeJSON(stdout, stats)
	}
	keys := make([]string, 0, len(stats))
	for k := range stats {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(stdout, "%s: %v\n", k, stats[k])
	}
	return nil
}

func cmdConflicts(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("conflicts", flag.ContinueOnError)
	fs.SetOutput(stderr)
	corpus := fs.String("corpus", "", "corpus dir")
	configPath := fs.String("config", "", "config path")
	jsonOut := fs.Bool("json", false, "JSON output")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	if *corpus != "" {
		cfg.CorpusDir = *corpus
	}
	db, _, err := openCorpus(cfg.CorpusDir)
	if err != nil {
		return err
	}
	defer db.Close()
	rows, err := db.Query(`select conflict_id,session_key,entry_id,first_entry_sha256,second_entry_sha256,details_json,created_at from conflicts order by conflict_id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	type conflict struct {
		ID                                                     int `json:"id"`
		SessionKey, EntryID, First, Second, Details, CreatedAt string
	}
	var cs []conflict
	for rows.Next() {
		var c conflict
		if err := rows.Scan(&c.ID, &c.SessionKey, &c.EntryID, &c.First, &c.Second, &c.Details, &c.CreatedAt); err != nil {
			return err
		}
		cs = append(cs, c)
	}
	if *jsonOut {
		return writeJSON(stdout, cs)
	}
	for _, c := range cs {
		fmt.Fprintf(stdout, "%d %s %s %s %s %s\n", c.ID, c.SessionKey, c.EntryID, c.First, c.Second, c.CreatedAt)
	}
	return rows.Err()
}

func cmdDoctor(args []string, stdout, stderr io.Writer) error {
	cfg, _ := loadConfig("")
	fmt.Fprintf(stdout, "aha: %s\nconfig: %s\ncorpus: %s\n", version, defaultConfigPath(), mustExpand(cfg.CorpusDir))
	for name, ad := range adapters() {
		fmt.Fprintf(stdout, "adapter: %s version=%s capabilities=%s\n", name, ad.Version(), mustJSON(ad.Capabilities()))
	}
	return nil
}

func openCorpus(dir string) (*sql.DB, string, error) {
	root, err := expandPath(firstNonEmpty(dir, "~/.aha"))
	if err != nil {
		return nil, "", err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, "", err
	}
	db, err := sql.Open("sqlite", filepath.Join(root, "corpus.db"))
	if err != nil {
		return nil, "", err
	}
	if err := initSchema(db); err != nil {
		db.Close()
		return nil, "", err
	}
	return db, root, nil
}

func initSchema(e interface {
	Exec(string, ...any) (sql.Result, error)
}) error {
	stmts := []string{
		`pragma foreign_keys=on`,
		`create table if not exists bundles(bundle_id text primary key,bundle_sha256 text unique,machine_id text,captured_at text,ingested_at text,manifest_json text)`,
		`create table if not exists ingest_attempts(attempt_id integer primary key,bundle_id text,bundle_sha256 text,ingested_at text,duplicate integer)`,
		`create table if not exists machines(machine_id text primary key,first_seen_at text,last_seen_at text,labels_json text)`,
		`create table if not exists sources(source_id integer primary key,source_name text unique,adapter_version text,capabilities_json text)`,
		`create table if not exists files(file_sha256 text primary key,kind text,bytes integer,compressed_blob_path text,first_seen_bundle_id text)`,
		`create table if not exists sessions(session_key text primary key,source_name text,source_session_id text,machine_id text,raw_cwd text,project_key text,started_at text,source_metadata_json text,is_subagent integer default 0,parent_session_key text)`,
		`create table if not exists session_versions(session_key text,file_sha256 text,bundle_id text,relative_path text,raw_path text,observed_at text,copy_state text,unique(session_key,file_sha256,bundle_id))`,
		`create table if not exists entries(session_key text,entry_id text,parent_id text,line_no integer,entry_type text,timestamp text,role text,entry_sha256 text,raw_json text,source_metadata_json text,primary key(session_key,entry_id))`,
		`create table if not exists messages(session_key text,entry_id text,role text,text text,tool_name text,command text,files_json text,model text,provider text,tokens integer,cost real,primary key(session_key,entry_id))`,
		`create table if not exists artifacts(artifact_sha256 text primary key,source_name text,machine_id text,bundle_id text,kind text,parent_session_key text,parent_entry_id text,raw_path text,relative_path text,text_preview text)`,
		`create table if not exists images(image_sha256 text primary key,source_name text,mime_type text,bytes integer,width integer,height integer,ext text,blob_path text)`,
		`create table if not exists entry_assets(session_key text,entry_id text,asset_sha256 text,asset_kind text,content_index integer,prompt_order integer,raw_ref text,mime_type text,metadata_json text,primary key(session_key,entry_id,asset_sha256,content_index,prompt_order))`,
		`create table if not exists conflicts(conflict_id integer primary key,session_key text,entry_id text,first_entry_sha256 text,second_entry_sha256 text,details_json text,created_at text default current_timestamp)`,
		`create virtual table if not exists fts_messages using fts5(session_key unindexed,entry_id unindexed,text)`,
		`create virtual table if not exists fts_artifacts using fts5(artifact_sha256 unindexed,text)`,
		`create index if not exists idx_sessions_source_machine on sessions(source_name,machine_id)`,
		`create index if not exists idx_entries_session_line on entries(session_key,line_no)`,
		`create index if not exists idx_entries_time_role on entries(timestamp,role)`,
	}
	for _, st := range stmts {
		if _, err := e.Exec(st); err != nil {
			return fmt.Errorf("schema %q: %w", st, err)
		}
	}
	return nil
}

// Adapters

type piAdapter struct{}

func (piAdapter) Name() string                { return "pi" }
func (piAdapter) Version() string             { return "v1" }
func (piAdapter) DefaultRoots() []DefaultRoot { return []DefaultRoot{{"all", "~/.pi/agent/sessions"}} }
func (piAdapter) Capabilities() AdapterCapabilities {
	return AdapterCapabilities{HasThreads: true, HasSubagents: true, HasImages: true, HasToolCalls: true, HasStableEntryIDs: true, CanLinkSubagents: true}
}
func (a piAdapter) Discover(ctx context.Context, config SourceConfig) ([]SessionFile, error) {
	return discoverJSONL(config.Root, "pi", true)
}
func (a piAdapter) DiscoverArtifacts(ctx context.Context, session SessionFile) ([]ArtifactFile, error) {
	root := filepath.Dir(filepath.Dir(session.Path))
	var out []ArtifactFile
	for _, dir := range []string{filepath.Join(root, "subagent-artifacts"), filepath.Join(filepath.Dir(session.Path), "subagent-artifacts")} {
		_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			rel, _ := filepath.Rel(dir, p)
			out = append(out, ArtifactFile{Source: "pi", Root: dir, Path: p, RelativePath: filepath.ToSlash(rel), Kind: "artifact", ParentHint: session.SessionID})
			return nil
		})
	}
	return out, nil
}
func (a piAdapter) ParseSession(ctx context.Context, file SessionFile, r io.Reader) (*ParsedSession, error) {
	return parseGenericJSONL("pi", file, r)
}

type claudeAdapter struct{}

func (claudeAdapter) Name() string    { return "claude-code" }
func (claudeAdapter) Version() string { return "v1" }
func (claudeAdapter) DefaultRoots() []DefaultRoot {
	return []DefaultRoot{{"all", "~/.claude/projects"}}
}
func (claudeAdapter) Capabilities() AdapterCapabilities {
	return AdapterCapabilities{HasThreads: false, HasSubagents: true, HasImages: true, HasToolCalls: true, HasStableEntryIDs: false, CanLinkSubagents: false}
}
func (a claudeAdapter) Discover(ctx context.Context, config SourceConfig) ([]SessionFile, error) {
	root := config.Root
	var out []SessionFile
	items, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	for _, it := range items {
		if !it.IsDir() || strings.HasPrefix(it.Name(), ".") {
			continue
		}
		dir := filepath.Join(root, it.Name())
		matches, _ := filepath.Glob(filepath.Join(dir, "*.jsonl"))
		sort.Strings(matches)
		for _, p := range matches {
			sid := strings.TrimSuffix(filepath.Base(p), filepath.Ext(p))
			out = append(out, SessionFile{Source: "claude-code", Root: root, Path: p, RelativePath: filepath.ToSlash(filepath.Join(it.Name(), filepath.Base(p))), SessionID: sid, CWD: decodeClaudeProjectPath(it.Name()), IsSubagent: strings.HasPrefix(filepath.Base(p), "agent-")})
		}
	}
	return out, nil
}
func (a claudeAdapter) DiscoverArtifacts(ctx context.Context, session SessionFile) ([]ArtifactFile, error) {
	return nil, nil
}
func (a claudeAdapter) ParseSession(ctx context.Context, file SessionFile, r io.Reader) (*ParsedSession, error) {
	return parseGenericJSONL("claude-code", file, r)
}

func discoverJSONL(root, source string, recursive bool) ([]SessionFile, error) {
	var out []SessionFile
	walk := func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(p) != ".jsonl" {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		sid := strings.TrimSuffix(filepath.Base(p), filepath.Ext(p))
		out = append(out, SessionFile{Source: source, Root: root, Path: p, RelativePath: filepath.ToSlash(rel), SessionID: sid, IsSubagent: strings.HasPrefix(filepath.Base(p), "agent-")})
		return nil
	}
	if recursive {
		err := filepath.WalkDir(root, walk)
		if os.IsNotExist(err) {
			return nil, nil
		}
		return out, err
	}
	items, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	for _, it := range items {
		_ = walk(filepath.Join(root, it.Name()), it, nil)
	}
	return out, nil
}

func parseGenericJSONL(source string, file SessionFile, r io.Reader) (*ParsedSession, error) {
	ps := &ParsedSession{Source: source, SourceSessionID: file.SessionID, CWD: file.CWD, StartedAt: file.StartedAt, IsSubagent: file.IsSubagent, Metadata: map[string]any{"relative_path": file.RelativePath}}
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 0, 1024*1024), 100*1024*1024)
	lineNo := 0
	for s.Scan() {
		lineNo++
		raw := strings.TrimSpace(s.Text())
		if raw == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			ps.Diagnostics = append(ps.Diagnostics, fmt.Sprintf("line %d: %v", lineNo, err))
			continue
		}
		if ps.CWD == "" {
			ps.CWD = stringField(m, "cwd")
		}
		if ps.StartedAt == "" {
			ps.StartedAt = stringField(m, "timestamp")
		}
		entryID := firstNonEmpty(stringField(m, "id"), stringField(m, "uuid"), nestedString(m, "message", "id"), nestedString(m, "message", "uuid"))
		if entryID == "" {
			entryID = fmt.Sprintf("line-%06d-%s", lineNo, shaBytes([]byte(raw))[:12])
		}
		role := stringField(m, "role")
		typ := stringField(m, "type")
		if role == "" {
			if typ == "user" || typ == "assistant" {
				role = typ
			} else {
				role = typ
			}
		}
		pe := ParsedEntry{EntryID: entryID, ParentID: firstNonEmpty(stringField(m, "parentId"), stringField(m, "parent_id"), stringField(m, "parentUuid")), LineNo: lineNo, EntryType: typ, Timestamp: stringField(m, "timestamp"), Role: role, RawJSON: raw, Model: nestedString(m, "message", "model"), Metadata: map[string]any{}}
		pe.Text, pe.ToolName, pe.Command, pe.FilesJSON, pe.Assets = extractContent(source, m)
		if pe.Model == "" {
			pe.Model = stringField(m, "model")
		}
		if usage, ok := nestedMap(m, "message", "usage"); ok {
			pe.Tokens = int64(numField(usage, "input_tokens") + numField(usage, "output_tokens") + numField(usage, "cache_creation_input_tokens") + numField(usage, "cache_read_input_tokens"))
		}
		if pe.Text == "" && (role == "branchSummary" || role == "compactionSummary") {
			pe.Text = firstNonEmpty(stringField(m, "summary"), stringField(m, "text"))
		}
		ps.Entries = append(ps.Entries, pe)
	}
	return ps, s.Err()
}

func extractContent(source string, m map[string]any) (text, tool, command, files string, assets []ParsedAsset) {
	var parts []string
	msg, _ := m["message"].(map[string]any)
	content, ok := msg["content"]
	if !ok {
		content = m["content"]
	}
	order := 0
	var walkContent func(any, int)
	walkContent = func(v any, idx int) {
		switch x := v.(type) {
		case string:
			if x != "" {
				parts = append(parts, x)
			}
		case []any:
			for i, it := range x {
				walkContent(it, i)
			}
		case map[string]any:
			t := stringField(x, "type")
			switch t {
			case "text":
				if s := stringField(x, "text"); s != "" {
					parts = append(parts, s)
				}
			case "tool_use":
				if tool == "" {
					tool = stringField(x, "name")
				}
				if b, err := json.Marshal(x["input"]); err == nil {
					files = string(b)
					if mm, ok := x["input"].(map[string]any); ok {
						command = stringField(mm, "command")
					}
				}
			case "image":
				assets = append(assets, parseImageAsset(x, idx, order))
				order++
			case "image_url":
				assets = append(assets, ParsedAsset{AssetKind: "image", ContentIndex: idx, PromptOrder: order, RawRef: mustJSON(x), MimeType: "", Metadata: map[string]any{"block_type": t}})
				order++
			case "tool_result": // preserved raw, not indexed
			default:
				if s := stringField(x, "text"); s != "" {
					parts = append(parts, s)
				}
				if src, ok := x["source"].(map[string]any); ok && (stringField(src, "media_type") != "" || stringField(src, "data") != "") {
					assets = append(assets, parseImageAsset(x, idx, order))
					order++
				}
			}
		}
	}
	walkContent(content, 0)
	return strings.TrimSpace(strings.Join(parts, "\n")), tool, command, files, assets
}

func parseImageAsset(block map[string]any, idx, order int) ParsedAsset {
	a := ParsedAsset{AssetKind: "image", ContentIndex: idx, PromptOrder: order, RawRef: mustJSON(block), Metadata: map[string]any{"block_type": stringField(block, "type")}}
	if src, ok := block["source"].(map[string]any); ok {
		a.MimeType = firstNonEmpty(stringField(src, "media_type"), stringField(src, "mime_type"))
		if data := stringField(src, "data"); data != "" {
			if b, err := base64.StdEncoding.DecodeString(data); err == nil {
				a.Data = b
			}
		}
		if a.RawRef == "" {
			a.RawRef = mustJSON(src)
		}
	}
	if u := stringField(block, "url"); u != "" {
		a.RawRef = u
	}
	if a.MimeType == "" {
		a.MimeType = "application/octet-stream"
	}
	return a
}

// utilities

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

func loadConfig(path string) (Config, error) {
	cfg := defaultConfig()
	if path == "" {
		path = defaultConfigPath()
	}
	exp, err := expandPath(path)
	if err != nil {
		return cfg, err
	}
	b, err := os.ReadFile(exp)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}
	ast, err := hujson.Parse(b)
	if err != nil {
		return cfg, err
	}
	ast.Standardize()
	if err := json.Unmarshal(ast.Pack(), &cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}
func defaultConfigPath() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "aha", "config.jsonc")
	}
	return "~/.config/aha/config.jsonc"
}
func expandPath(p string) (string, error) {
	if p == "" {
		return "", nil
	}
	if strings.HasPrefix(p, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if p == "~" {
			return home, nil
		}
		if strings.HasPrefix(p, "~/") {
			return filepath.Join(home, p[2:]), nil
		}
	}
	return filepath.Abs(os.ExpandEnv(p))
}
func mustExpand(p string) string {
	x, err := expandPath(p)
	if err != nil {
		return p
	}
	return x
}
func stableRead(path string) ([]byte, string, error) {
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
func shaBytes(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func randomID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
func safeName(s string) string {
	re := regexp.MustCompile(`[^A-Za-z0-9_.-]+`)
	return re.ReplaceAllString(s, "-")
}
func safeTime(s string) string { r := strings.NewReplacer(":", "-", ".", "-"); return r.Replace(s) }
func runtimeOS() string        { return runtime.GOOS }
func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }
func writeJSON(w io.Writer, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
}
func storeFileBlob(root, sha string, data []byte) error {
	dir := filepath.Join(root, "blobs", "files")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	var buf bytes.Buffer
	enc, err := zstd.NewWriter(&buf)
	if err != nil {
		return err
	}
	if _, err := enc.Write(data); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, sha+".zst"), buf.Bytes(), 0o644)
}
func extFromMime(mt string) string {
	if mt == "" {
		return ".bin"
	}
	exts, _ := mime.ExtensionsByType(mt)
	if len(exts) > 0 {
		return exts[0]
	}
	switch mt {
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	}
	return ".bin"
}
func projectKey(cwd string) string {
	if cwd == "" {
		return ""
	}
	return filepath.Base(cwd)
}
func shortProject(p string) string {
	if p == "" {
		return "-"
	}
	return filepath.Base(p)
}
func ftsQuery(q string) string {
	fields := strings.Fields(q)
	if len(fields) == 0 {
		return q
	}
	for i, f := range fields {
		fields[i] = `"` + strings.ReplaceAll(f, `"`, `""`) + `"`
	}
	return strings.Join(fields, " ")
}
func numField(m map[string]any, k string) float64 {
	switch v := m[k].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case json.Number:
		f, _ := v.Float64()
		return f
	}
	return 0
}
func stringField(m map[string]any, k string) string {
	if m == nil {
		return ""
	}
	switch v := m[k].(type) {
	case string:
		return v
	case json.Number:
		return v.String()
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return ""
}
func nestedMap(m map[string]any, path ...string) (map[string]any, bool) {
	cur := m
	for i, p := range path {
		v, ok := cur[p]
		if !ok {
			return nil, false
		}
		mm, ok := v.(map[string]any)
		if !ok {
			return nil, false
		}
		if i == len(path)-1 {
			return mm, true
		}
		cur = mm
	}
	return cur, true
}
func nestedString(m map[string]any, path ...string) string {
	if len(path) == 0 {
		return ""
	}
	cur := m
	for _, p := range path[:len(path)-1] {
		mm, ok := cur[p].(map[string]any)
		if !ok {
			return ""
		}
		cur = mm
	}
	return stringField(cur, path[len(path)-1])
}
func decodeClaudeProjectPath(name string) string {
	if strings.HasPrefix(name, "-") {
		return "/" + strings.ReplaceAll(strings.TrimPrefix(name, "-"), "-", "/")
	}
	return name
}

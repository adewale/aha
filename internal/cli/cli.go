package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/adewale/aha/internal/adapters"
	"github.com/adewale/aha/internal/archive"
	"github.com/adewale/aha/internal/config"
	"github.com/adewale/aha/internal/corpus"
	"github.com/adewale/aha/internal/hash"
	"github.com/adewale/aha/internal/model"
	"github.com/adewale/aha/internal/paths"
	"github.com/adewale/aha/internal/search"
)

type Command struct {
	Name, Usage string
	Run         func([]string, io.Writer, io.Writer) error
}

func Registry() map[string]Command {
	return map[string]Command{
		"refresh":   {"refresh", "aha refresh [--machine ID] [--source pi=PATH] [--source claude-code=PATH] [--out DIR] [--corpus DIR]", cmdRefresh},
		"snapshot":  {"snapshot", "aha snapshot [--machine ID] [--source pi=PATH] [--source claude-code=PATH] [--out DIR]", cmdSnapshot},
		"ingest":    {"ingest", "aha ingest [bundle.tar.zst ...]", cmdIngest},
		"search":    {"search", "aha search <query> [--source NAME] [--machine ID] [--role ROLE] [--after DATE] [--before DATE] [--path TEXT] [--json]", cmdSearch},
		"read":      {"read", "aha read --session ID [--entry ID] [--before N] [--after N] [--json]", cmdRead},
		"status":    {"status", "aha status [--json]", cmdStatus},
		"conflicts": {"conflicts", "aha conflicts [--json]", cmdConflicts},
		"doctor":    {"doctor", "aha doctor", cmdDoctor},
		"init":      {"init", "aha init [--config PATH]", cmdInit},
	}
}

func Run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		Usage(stdout)
		return nil
	}
	if args[0] == "version" || args[0] == "--version" || args[0] == "-v" {
		fmt.Fprintln(stdout, "aha", model.Version)
		return nil
	}
	cmd, ok := Registry()[args[0]]
	if !ok {
		return fmt.Errorf("unknown command %q", args[0])
	}
	return cmd.Run(args[1:], stdout, stderr)
}

func Usage(w io.Writer) {
	fmt.Fprintf(w, "aha %s\n\nUsage:\n", model.Version)
	names := CommandNames()
	for _, name := range names {
		fmt.Fprintf(w, "  %s\n", Registry()[name].Usage)
	}
}

func CommandNames() []string {
	names := make([]string, 0, len(Registry()))
	for n := range Registry() {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func cmdInit(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", config.DefaultPath(), "config path")
	acceptSecrets := fs.Bool("accept-secrets", false, "write config with privacy warning acknowledged")
	if err := fs.Parse(args); err != nil {
		return err
	}
	path, err := paths.Expand(*configPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("config already exists: %s", path)
	}
	content := config.StarterJSONC()
	if *acceptSecrets {
		cfg := config.Default()
		cfg.AcceptSecretsWarning = true
		b, _ := json.MarshalIndent(cfg, "", "  ")
		content = append([]byte("// aha config (JSONC)\n// Privacy warning acknowledged by `aha init --accept-secrets`. Bundles/corpora remain private.\n"), append(b, '\n')...)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		return err
	}
	fmt.Fprintln(stdout, path)
	return nil
}

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

type snapshotRequest struct {
	Config     model.Config
	CapturedAt string
	BundleID   string
}

func cmdSnapshot(args []string, stdout, stderr io.Writer) error {
	req, err := parseSnapshotRequest("snapshot", args, stderr)
	if err != nil {
		return err
	}
	path, sha, err := writeSnapshot(req)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "%s\nsha256:%s\n", path, sha)
	return nil
}

func cmdRefresh(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("refresh", flag.ContinueOnError)
	fs.SetOutput(stderr)
	machine := fs.String("machine", "", "machine id")
	outDir := fs.String("out", "", "output directory")
	corpusDir := fs.String("corpus", "", "corpus dir")
	configPath := fs.String("config", "", "JSONC config path")
	acceptSecrets := fs.Bool("accept-secrets", false, "acknowledge v1 does not redact secrets")
	capturedAt := fs.String("captured-at", "", "capture timestamp (advanced deterministic testing)")
	bundleID := fs.String("bundle-id", "", "bundle id (advanced deterministic testing)")
	var sourceFlags multiFlag
	fs.Var(&sourceFlags, "source", "source spec type=path (repeatable)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	req, err := buildSnapshotRequest(*configPath, *machine, *outDir, *acceptSecrets, *capturedAt, *bundleID, sourceFlags, stderr)
	if err != nil {
		return err
	}
	if *corpusDir != "" {
		req.Config.CorpusDir = *corpusDir
	}
	path, sha, err := writeSnapshot(req)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "%s\nsha256:%s\n", path, sha)
	store, err := corpus.Open(req.Config.CorpusDir)
	if err != nil {
		return err
	}
	defer store.Close()
	rep, err := corpus.IngestBundle(store, adapters.Builtins(), path)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "%s: sessions=%d entries=%d messages=%d images=%d artifacts=%d duplicate=%v\n", path, rep.Sessions, rep.Entries, rep.Messages, rep.Images, rep.Artifacts, rep.Duplicate)
	return nil
}

func parseSnapshotRequest(name string, args []string, stderr io.Writer) (snapshotRequest, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	machine := fs.String("machine", "", "machine id")
	outDir := fs.String("out", "", "output directory")
	configPath := fs.String("config", "", "JSONC config path")
	acceptSecrets := fs.Bool("accept-secrets", false, "acknowledge v1 does not redact secrets")
	capturedAt := fs.String("captured-at", "", "capture timestamp (advanced deterministic testing)")
	bundleID := fs.String("bundle-id", "", "bundle id (advanced deterministic testing)")
	var sourceFlags multiFlag
	fs.Var(&sourceFlags, "source", "source spec type=path (repeatable)")
	if err := fs.Parse(args); err != nil {
		return snapshotRequest{}, err
	}
	return buildSnapshotRequest(*configPath, *machine, *outDir, *acceptSecrets, *capturedAt, *bundleID, sourceFlags, stderr)
}

func buildSnapshotRequest(configPath, machine, outDir string, acceptSecrets bool, capturedAt, bundleID string, sourceFlags multiFlag, stderr io.Writer) (snapshotRequest, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return snapshotRequest{}, err
	}
	if machine != "" {
		cfg.MachineID = machine
	}
	if outDir != "" {
		cfg.BundleOutDir = outDir
	}
	if len(sourceFlags) > 0 {
		cfg.Sources = nil
		for _, sf := range sourceFlags {
			parts := strings.SplitN(sf, "=", 2)
			if len(parts) != 2 {
				return snapshotRequest{}, fmt.Errorf("invalid --source %q, want type=path", sf)
			}
			cfg.Sources = append(cfg.Sources, model.SourceConfig{Type: parts[0], Root: parts[1], Enabled: true})
		}
	}
	if cfg.MachineID == "" {
		return snapshotRequest{}, errors.New("machine_id required: set config machine_id or pass --machine")
	}
	if os.Getenv("AHA_ACCEPT_SECRETS") == "1" {
		cfg.AcceptSecretsWarning = true
	}
	if !acceptSecrets && !cfg.AcceptSecretsWarning {
		fmt.Fprintln(stderr, "V1 does not redact secrets. Bundles may contain prompts, source code, tool output, images, tokens, and private paths. Treat the bundle as private. Pass --accept-secrets to continue.")
		return snapshotRequest{}, errors.New("secrets warning not acknowledged")
	}
	return snapshotRequest{Config: cfg, CapturedAt: capturedAt, BundleID: bundleID}, nil
}

func writeSnapshot(req snapshotRequest) (string, string, error) {
	out, err := paths.Expand(req.Config.BundleOutDir)
	if err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return "", "", err
	}
	opts := archive.Options{CapturedAt: req.CapturedAt, BundleID: req.BundleID}
	if opts.CapturedAt == "" {
		opts.CapturedAt = time.Now().UTC().Format(time.RFC3339)
	}
	if opts.BundleID == "" {
		opts.BundleID = hash.RandomID()
	}
	bundle, err := archive.Capture(context.Background(), req.Config, adapters.Builtins(), opts)
	if err != nil {
		return "", "", err
	}
	path := filepath.Join(out, fmt.Sprintf("aha-sessions-%s-%s-%s.tar.zst", safeName(req.Config.MachineID), safeTime(opts.CapturedAt), safeName(opts.BundleID)))
	sha, err := archive.Write(path, bundle)
	if err != nil {
		return "", "", err
	}
	receipt := map[string]any{"bundle": path, "sha256": sha, "bundle_id": opts.BundleID, "captured_at": opts.CapturedAt}
	rb, _ := json.MarshalIndent(receipt, "", "  ")
	_ = os.WriteFile(path+".receipt.json", append(rb, '\n'), 0o644)
	return path, sha, nil
}

func cmdIngest(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("ingest", flag.ContinueOnError)
	fs.SetOutput(stderr)
	corpusDir := fs.String("corpus", "", "corpus dir")
	configPath := fs.String("config", "", "config path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if *corpusDir != "" {
		cfg.CorpusDir = *corpusDir
	}
	bundles := fs.Args()
	if len(bundles) == 0 {
		out, err := paths.Expand(cfg.BundleOutDir)
		if err != nil {
			return err
		}
		bundles = []string{filepath.Join(out, "*.tar.zst")}
	}
	store, err := corpus.Open(cfg.CorpusDir)
	if err != nil {
		return err
	}
	defer store.Close()
	for _, pattern := range bundles {
		matches, _ := filepath.Glob(pattern)
		if len(matches) == 0 {
			if len(fs.Args()) == 0 {
				return fmt.Errorf("no bundles found for %s", pattern)
			}
			matches = []string{pattern}
		}
		for _, path := range matches {
			rep, err := corpus.IngestBundle(store, adapters.Builtins(), path)
			if err != nil {
				return err
			}
			fmt.Fprintf(stdout, "%s: sessions=%d entries=%d messages=%d images=%d artifacts=%d duplicate=%v\n", path, rep.Sessions, rep.Entries, rep.Messages, rep.Images, rep.Artifacts, rep.Duplicate)
		}
	}
	return nil
}

func cmdSearch(args []string, stdout, stderr io.Writer) error {
	args = reorderSearchArgs(args)
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	fs.SetOutput(stderr)
	corpusDir := fs.String("corpus", "", "corpus dir")
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
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if *corpusDir != "" {
		cfg.CorpusDir = *corpusDir
	}
	store, err := corpus.Open(cfg.CorpusDir)
	if err != nil {
		return err
	}
	defer store.Close()
	results, err := search.Query(store.DB, strings.Join(fs.Args(), " "), search.Filters{Source: *source, Machine: *machine, Role: *role, After: *after, Before: *before, Path: *pathFilter, Limit: *limit})
	if err != nil {
		return err
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
	corpusDir := fs.String("corpus", "", "corpus dir")
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
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if *corpusDir != "" {
		cfg.CorpusDir = *corpusDir
	}
	store, err := corpus.Open(cfg.CorpusDir)
	if err != nil {
		return err
	}
	defer store.Close()
	entries, err := corpus.ReadContext(store.DB, *session, *entry, *before, *after)
	if err != nil {
		return err
	}
	if *jsonOut {
		return writeJSON(stdout, entries)
	}
	for _, e := range entries {
		body := e.Text
		if body == "" {
			body = e.RawJSON
		}
		fmt.Fprintf(stdout, "--- %d %s %s %s ---\n%s\n", e.LineNo, e.EntryID, e.Timestamp, e.Role, body)
	}
	return nil
}

func cmdStatus(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	corpusDir := fs.String("corpus", "", "corpus dir")
	configPath := fs.String("config", "", "config path")
	jsonOut := fs.Bool("json", false, "JSON output")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if *corpusDir != "" {
		cfg.CorpusDir = *corpusDir
	}
	store, err := corpus.Open(cfg.CorpusDir)
	if err != nil {
		return err
	}
	defer store.Close()
	stats := corpus.Status(store.DB, store.Root)
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
	corpusDir := fs.String("corpus", "", "corpus dir")
	configPath := fs.String("config", "", "config path")
	jsonOut := fs.Bool("json", false, "JSON output")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if *corpusDir != "" {
		cfg.CorpusDir = *corpusDir
	}
	store, err := corpus.Open(cfg.CorpusDir)
	if err != nil {
		return err
	}
	defer store.Close()
	cs, err := corpus.Conflicts(store.DB)
	if err != nil {
		return err
	}
	if *jsonOut {
		return writeJSON(stdout, cs)
	}
	for _, c := range cs {
		fmt.Fprintf(stdout, "%d %s %s %s %s %s\n", c.ID, c.SessionKey, c.EntryID, c.First, c.Second, c.CreatedAt)
	}
	return nil
}

func cmdDoctor(args []string, stdout, stderr io.Writer) error {
	fmt.Fprintf(stdout, "aha: %s\nconfig: %s\n", model.Version, config.DefaultPath())
	names := make([]string, 0, len(adapters.Builtins()))
	for n := range adapters.Builtins() {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		ad := adapters.Builtins()[name]
		fmt.Fprintf(stdout, "adapter: %s version=%s capabilities=%s\n", name, ad.Version(), mustJSON(ad.Capabilities()))
	}
	return nil
}

func reorderSearchArgs(args []string) []string {
	valueFlags := map[string]bool{"--corpus": true, "--config": true, "--source": true, "--machine": true, "--role": true, "--after": true, "--before": true, "--path": true, "--limit": true}
	boolFlags := map[string]bool{"--json": true}
	literal := []string(nil)
	for i, a := range args {
		if a == "--" {
			literal = args[i+1:]
			args = args[:i]
			break
		}
	}
	var flags []string
	var pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		name := a
		if eq := strings.IndexByte(a, '='); eq >= 0 {
			name = a[:eq]
		}
		if boolFlags[name] || strings.Contains(a, "=") && valueFlags[name] {
			flags = append(flags, a)
			continue
		}
		if valueFlags[name] && i+1 < len(args) {
			flags = append(flags, a, args[i+1])
			i++
			continue
		}
		pos = append(pos, a)
	}
	out := append(flags, pos...)
	if literal != nil {
		out = append(out, "--")
		out = append(out, literal...)
	}
	return out
}

func writeJSON(w io.Writer, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
}
func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }
func safeName(s string) string {
	return regexp.MustCompile(`[^A-Za-z0-9_.-]+`).ReplaceAllString(s, "-")
}
func safeTime(s string) string { return strings.NewReplacer(":", "-", ".", "-").Replace(s) }
func shortProject(p string) string {
	if p == "" {
		return "-"
	}
	return filepath.Base(p)
}

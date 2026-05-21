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
	"github.com/adewale/aha/internal/safety"
)

type Command struct {
	Name       string
	Usage      string
	Flags      []string
	Examples   []string
	JSONSchema string
	Docs       string
	Run        func([]string, io.Writer, io.Writer) error
}

func Registry() map[string]Command {
	return map[string]Command{
		"refresh":   {Name: "refresh", Usage: "aha refresh [--session MATCH ...] [--max-sessions N] [--repo DIR] [--json]", Flags: []string{"--accept-secrets", "--bundle-id", "--captured-at", "--config", "--corpus", "--machine", "--max-sessions", "--out", "--repo", "--session", "--source", "--json"}, Examples: []string{"aha refresh", "aha refresh --session abc --max-sessions 1"}, JSONSchema: "object{bundle,sha256,report}", Docs: "snapshot configured sources and ingest the new bundle", Run: cmdRefresh},
		"snapshot":  {Name: "snapshot", Usage: "aha snapshot [--session MATCH ...] [--max-sessions N] [--out DIR] [--json]", Flags: []string{"--accept-secrets", "--bundle-id", "--captured-at", "--config", "--machine", "--max-sessions", "--out", "--session", "--source", "--json"}, Examples: []string{"aha snapshot --accept-secrets --out ./bundles"}, JSONSchema: "object{bundle,sha256,bundle_id,captured_at}", Docs: "create an immutable local history bundle", Run: cmdSnapshot},
		"ingest":    {Name: "ingest", Usage: "aha ingest [--repo DIR] [bundle.tar.zst ...]", Flags: []string{"--config", "--corpus", "--repo", "--json"}, Examples: []string{"aha ingest ./bundle.tar.zst", "aha ingest --repo ./aha-repo"}, JSONSchema: "array<object{bundle,sessions,entries,messages,images,artifacts,duplicate}>", Docs: "merge one or more bundles into a corpus", Run: cmdIngest},
		"search":    {Name: "search", Usage: "aha search <query> [--repo DIR] [--source NAME] [--machine ID] [--role ROLE] [--json|--refs|--files|--md]", Flags: []string{"--after", "--before", "--config", "--corpus", "--files", "--json", "--limit", "--machine", "--md", "--path", "--refs", "--repo", "--role", "--source"}, Examples: []string{"aha search needle --json", "aha search needle --refs"}, JSONSchema: "array<object{score,timestamp,source,machine,project,role,snippet,session_key,entry_id,ref}>", Docs: "find relevant messages/artifacts; use read on returned refs before answering", Run: cmdSearch},
		"read":      {Name: "read", Usage: "aha read [REF] [--session ID] [--entry ID] [--repo DIR] [--before N] [--after N] [--json|--md]", Flags: []string{"--after", "--before", "--config", "--corpus", "--entry", "--json", "--md", "--repo", "--session"}, Examples: []string{"aha read <session>#<entry> --json", "aha read --session <session> --entry <entry> --json"}, JSONSchema: "array<object{line_no,entry_id,timestamp,role,text,raw_json}>", Docs: "retrieve source context for a search result", Run: cmdRead},
		"status":    {Name: "status", Usage: "aha status [--repo DIR] [--json]", Flags: []string{"--config", "--corpus", "--json", "--repo"}, Examples: []string{"aha status --json"}, JSONSchema: "object{sessions,entries,messages,artifacts,images,bundles,conflicts,bytes}", Docs: "summarize corpus health", Run: cmdStatus},
		"conflicts": {Name: "conflicts", Usage: "aha conflicts [--repo DIR] [--json]", Flags: []string{"--config", "--corpus", "--json", "--repo"}, Examples: []string{"aha conflicts --json"}, JSONSchema: "array<object{id,session_key,entry_id,first,second,created_at}>", Docs: "list quarantined merge conflicts", Run: cmdConflicts},
		"doctor":    {Name: "doctor", Usage: "aha doctor [--json]", Flags: []string{"--json"}, Examples: []string{"aha doctor"}, JSONSchema: "object{version,config,adapters,next}", Docs: "show diagnostics and next actions", Run: cmdDoctor},
		"init":      {Name: "init", Usage: "aha init [--config PATH] [--accept-secrets] [--json]", Flags: []string{"--accept-secrets", "--config", "--json"}, Examples: []string{"aha init --accept-secrets"}, JSONSchema: "object{config,accepted_secrets}", Docs: "write starter JSONC config", Run: cmdInit},
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
	if err := cmd.Run(args[1:], stdout, stderr); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	return nil
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

type corpusFlags struct {
	corpusDir *string
	repoDir   *string
	config    *string
}

func registerCorpusFlags(fs *flag.FlagSet) corpusFlags {
	return corpusFlags{corpusDir: fs.String("corpus", "", "corpus dir"), repoDir: fs.String("repo", "", "repo/corpus dir"), config: fs.String("config", "", "config path")}
}

func (f corpusFlags) loadConfig() (model.Config, error) {
	cfg, err := config.Load(*f.config)
	if err != nil {
		return cfg, err
	}
	applyCorpusOverride(&cfg, *f.repoDir, *f.corpusDir)
	return cfg, nil
}

func applyCorpusOverride(cfg *model.Config, repoDir, corpusDir string) {
	if repoDir != "" {
		cfg.CorpusDir = repoDir
	} else if corpusDir != "" {
		cfg.CorpusDir = corpusDir
	}
}

func openCorpusForCommand(cfg model.Config, create bool) (*corpus.Store, error) {
	if err := safety.ValidateWriteOutsideSources(cfg, cfg.CorpusDir, "corpus"); err != nil {
		return nil, err
	}
	if create {
		return corpus.Open(cfg.CorpusDir)
	}
	return corpus.OpenExisting(cfg.CorpusDir)
}

func writeSnapshot(req snapshotRequest) (string, string, error) {
	out, err := paths.Expand(req.Config.BundleOutDir)
	if err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return "", "", err
	}
	opts := archive.Options{CapturedAt: req.CapturedAt, BundleID: req.BundleID, SessionFilters: req.SessionFilters, MaxSessions: req.MaxSessions}
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

func reorderSearchArgs(args []string) []string {
	valueFlags := map[string]bool{"--corpus": true, "--repo": true, "--config": true, "--source": true, "--machine": true, "--role": true, "--after": true, "--before": true, "--path": true, "--limit": true}
	boolFlags := map[string]bool{"--json": true, "--refs": true, "--files": true, "--md": true}
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

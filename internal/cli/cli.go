package cli

import (
	"bytes"
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
	ahaclock "github.com/adewale/aha/internal/clock"
	"github.com/adewale/aha/internal/config"
	"github.com/adewale/aha/internal/corpus"
	"github.com/adewale/aha/internal/depot"
	"github.com/adewale/aha/internal/hash"
	"github.com/adewale/aha/internal/model"
	"github.com/adewale/aha/internal/safety"
)

type Command struct {
	Name       string
	Usage      string
	Flags      []string
	FlagSpecs  []FlagSpec
	Examples   []string
	JSONSchema string
	Docs       string
	Run        func([]string, io.Writer, io.Writer) error
}

type CommandError struct {
	Code    string
	Command string
	Err     error
	Next    []string
}

func (e *CommandError) Error() string { return e.Err.Error() }
func (e *CommandError) Unwrap() error { return e.Err }

type errorEnvelope struct {
	Error errorPayload `json:"error"`
}

type errorPayload struct {
	Code    string   `json:"code"`
	Message string   `json:"message"`
	Command string   `json:"command,omitempty"`
	Next    []string `json:"next,omitempty"`
}

func Registry() map[string]Command {
	return map[string]Command{
		"refresh":   {Name: "refresh", Usage: "aha refresh [--session MATCH ...] [--max-sessions N] [--repo DIR] [--depot DEPOT] [--json]", Flags: []string{"--accept-secrets", "--bundle-id", "--captured-at", "--config", "--corpus", "--depot", "--machine", "--max-sessions", "--repo", "--session", "--source", "--json"}, Examples: []string{"aha refresh", "aha refresh --session abc --max-sessions 1"}, JSONSchema: "object{bundle,sha256,report}", Docs: "snapshot configured source state or reuse unchanged depot state, then ingest pending/new depot bundles", Run: cmdRefresh},
		"snapshot":  {Name: "snapshot", Usage: "aha snapshot [--session MATCH ...] [--max-sessions N] [--depot DEPOT] [--json]", Flags: []string{"--accept-secrets", "--bundle-id", "--captured-at", "--config", "--depot", "--machine", "--max-sessions", "--session", "--source", "--json"}, Examples: []string{"aha snapshot --accept-secrets --depot local:./bundles"}, JSONSchema: "object{bundle,sha256,bundle_id,captured_at}", Docs: "create an immutable local history bundle and store it in a depot", Run: cmdSnapshot},
		"ingest":    {Name: "ingest", Usage: "aha ingest [--repo DIR] [--depot DEPOT] [--json] [bundle.tar.zst ...]", Flags: []string{"--config", "--corpus", "--depot", "--repo", "--json"}, Examples: []string{"aha ingest ./bundle.tar.zst", "aha ingest --repo ./aha-repo", "aha ingest --depot local:~/.aha/depot"}, JSONSchema: "array<object{bundle,sha256?,bytes?,fetched?,sessions,entries,messages,images,artifacts,duplicate}>", Docs: "merge one or more bundles into a corpus", Run: cmdIngest},
		"search":    {Name: "search", Usage: "aha search <query> [--repo DIR] [--source NAME] [--machine ID] [--role ROLE] [--project KEY] [--path-token TOKEN] [--json|--refs|--files|--md]", Flags: flagNames(searchFlagSpecs), FlagSpecs: searchFlagSpecs, Examples: []string{"aha search needle --json", "aha search needle --refs"}, JSONSchema: "array<object{score,timestamp,source,machine,project,role,snippet,session_key,entry_id,ref,ref_text}>", Docs: "find relevant messages/artifacts; use read on returned refs before answering", Run: cmdSearch},
		"read":      {Name: "read", Usage: "aha read [REF] [--session ID] [--entry ID] [--repo DIR] [--before N] [--after N] [--json|--md]", Flags: flagNames(readFlagSpecs), FlagSpecs: readFlagSpecs, Examples: []string{"aha read <ref_text> --json", "aha read --session <session> --entry <entry> --json"}, JSONSchema: "array<object{line_no,entry_id,timestamp,role,text,raw_json}>", Docs: "retrieve source context for a search result", Run: cmdRead},
		"status":    {Name: "status", Usage: "aha status [--repo DIR] [--depot DEPOT] [--json]", Flags: []string{"--config", "--corpus", "--depot", "--json", "--repo"}, Examples: []string{"aha status --json", "aha status --depot local:~/.aha/depot --json"}, JSONSchema: "object{corpus_dir,machines,sources,sessions,session_versions,entries,messages,artifacts,images,entry_assets,files,bundles,conflicts,fts_messages,fts_artifacts,session_path_tokens,artifact_path_tokens,index_size_bytes,depot_behind_bundles?,depot_catalog_refs_listed?,depot_unique_refs_listed?,depot_fetches?,next}", Docs: "summarize corpus health", Run: cmdStatus},
		"verify":    {Name: "verify", Usage: "aha verify [--repo DIR] [--repair-fts] [--json]", Flags: []string{"--config", "--corpus", "--json", "--repair-fts", "--repo"}, Examples: []string{"aha verify --json", "aha verify --repair-fts"}, JSONSchema: "object{root,stats,problems,repaired_fts,fts_repair?}", Docs: "verify corpus invariants and optionally repair derived FTS rows", Run: cmdVerify},
		"conflicts": {Name: "conflicts", Usage: "aha conflicts [--repo DIR] [--json]", Flags: []string{"--config", "--corpus", "--json", "--repo"}, Examples: []string{"aha conflicts --json"}, JSONSchema: "array<object{id,session_key,entry_id,first,second,created_at}>", Docs: "list quarantined merge conflicts", Run: cmdConflicts},
		"corpus":    {Name: "corpus", Usage: "aha corpus <size|vacuum|prune-orphans> [--repo DIR] [--json] [--force]", Flags: []string{"--config", "--corpus", "--force", "--json", "--repo"}, Examples: []string{"aha corpus size --json", "aha corpus vacuum", "aha corpus prune-orphans --json"}, JSONSchema: "object{root,total_bytes,database_bytes,bundle_blob_bytes,file_blob_bytes,image_blob_bytes,other_bytes,files}|object{before_bytes,after_bytes,reclaimed_bytes}|object{root,dry_run,orphan_bytes,deleted_files,deleted_bytes,orphans}", Docs: "inspect corpus disk usage, vacuum SQLite, or explicitly prune unreferenced blobs", Run: cmdCorpus},
		"doctor":    {Name: "doctor", Usage: "aha doctor [--depot DEPOT] [--json]", Flags: []string{"--config", "--depot", "--json"}, Examples: []string{"aha doctor", "aha doctor --depot local:~/.aha/depot --json"}, JSONSchema: "object{version,config,adapters,sources,corpus,depot,next}", Docs: "show diagnostics and next actions", Run: cmdDoctor},
		"depot":     {Name: "depot", Usage: "aha depot <init|ls|verify|compact> [DEPOT] [--json] [--repair] [--deep]", Flags: []string{"--config", "--deep", "--json", "--repair"}, Examples: []string{"aha depot init local:~/.aha/depot", "aha depot ls --json", "aha depot verify --deep", "aha depot verify --repair", "aha depot compact --json"}, JSONSchema: "object|array", Docs: "initialize, list, verify, or compact a bundle depot", Run: cmdDepot},
		"init":      {Name: "init", Usage: "aha init [--config PATH] [--accept-secrets] [--json]", Flags: []string{"--accept-secrets", "--config", "--json"}, Examples: []string{"aha init --accept-secrets"}, JSONSchema: "object{config,accepted_secrets}", Docs: "write starter JSONC config", Run: cmdInit},
		"mcp":       {Name: "mcp", Usage: "aha mcp [--config PATH] [--repo DIR] [--dry-run]", Flags: []string{"--config", "--corpus", "--dry-run", "--repo"}, Examples: []string{"aha mcp", "aha mcp --dry-run"}, JSONSchema: "jsonrpc:tools/list|tools/call (stdio MCP)", Docs: "run a read-only stdio MCP server over the corpus", Run: cmdMcp},
		"serve":     {Name: "serve", Usage: "aha serve [--addr HOST:PORT] [--allow-remote] [--allowed-hosts H1,H2] [--timeout DUR] [--token TOKEN] [--config PATH] [--repo DIR]", Flags: []string{"--addr", "--allow-remote", "--allowed-hosts", "--config", "--corpus", "--repo", "--timeout", "--token"}, Examples: []string{"aha serve", "aha serve --addr 127.0.0.1:18428", "aha serve --allow-remote --token $(openssl rand -hex 32)"}, JSONSchema: "http://HOST:PORT/api/{search,read,status,verify,conflicts,corpus_size,doctor}", Docs: "run a read-only local dashboard over the corpus on loopback", Run: cmdServe},
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
		return &CommandError{Code: "unknown_command", Command: args[0], Err: fmt.Errorf("unknown command %q", args[0]), Next: []string{"aha help", "aha doctor"}}
	}
	if err := cmd.Run(args[1:], stdout, stderr); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	return nil
}

func RunMain(args []string, stdout, stderr io.Writer) int {
	cleanArgs, profileOpts, profileErr := profileOptionsFromArgs(args)
	if profileErr != nil {
		if wantsJSON(args) {
			_ = writeJSON(stderr, errorEnvelope{Error: machineError(profileErr, cleanArgs)})
		} else {
			fmt.Fprintln(stderr, "error:", profileErr)
		}
		return 1
	}
	if wantsJSON(cleanArgs) {
		var commandStderr bytes.Buffer
		if err := runWithProfiling(profileOpts, func() error { return Run(cleanArgs, stdout, &commandStderr) }); err != nil {
			_ = writeJSON(stderr, errorEnvelope{Error: machineError(err, cleanArgs)})
			return 1
		}
		_, _ = stderr.Write(commandStderr.Bytes())
		return 0
	}
	if err := runWithProfiling(profileOpts, func() error { return Run(cleanArgs, stdout, stderr) }); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	return 0
}

func wantsJSON(args []string) bool {
	for _, arg := range args {
		if arg == "--" {
			return false
		}
		if arg == "--json" || strings.HasPrefix(arg, "--json=") {
			return true
		}
	}
	return false
}

func machineError(err error, args []string) errorPayload {
	payload := errorPayload{Code: classifyError(err), Message: err.Error(), Next: []string{"aha doctor", "aha help"}}
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		payload.Command = args[0]
	}
	var ce *CommandError
	if errors.As(err, &ce) {
		payload.Code = ce.Code
		payload.Command = ce.Command
		if len(ce.Next) > 0 {
			payload.Next = ce.Next
		}
	}
	return payload
}

func classifyError(err error) string {
	var notFound corpus.NotFoundError
	if errors.As(err, &notFound) {
		return "not_found"
	}
	var ambiguous corpus.AmbiguousError
	if errors.As(err, &ambiguous) {
		return "ambiguous"
	}
	var unsupportedSchema archive.UnsupportedSchemaError
	if errors.As(err, &unsupportedSchema) {
		return "unsupported_schema"
	}
	var unsupportedRef model.UnsupportedRefError
	if errors.As(err, &unsupportedRef) {
		return "unsupported_ref"
	}
	msg := err.Error()
	if strings.Contains(msg, "flag provided but not defined") || strings.Contains(msg, "invalid value") || strings.Contains(msg, "flag needs an argument") {
		return "flag_parse_error"
	}
	if strings.Contains(msg, "required") || strings.Contains(msg, "not found") || strings.Contains(msg, "ambiguous") {
		return "validation_error"
	}
	return "command_failed"
}

func Usage(w io.Writer) {
	fmt.Fprintf(w, "aha %s\n\nUsage:\n", model.Version)
	fmt.Fprintln(w, "  aha [--cpuprofile FILE] [--memprofile FILE] <command> [args]")
	names := CommandNames()
	for _, name := range names {
		fmt.Fprintf(w, "  %s\n", Registry()[name].Usage)
	}
	fmt.Fprintln(w, "\nGlobal profiling flags may also be supplied after the subcommand, or via AHA_CPU_PROFILE/AHA_MEM_PROFILE.")
}

func GenerateCommandsMarkdown() string {
	var b strings.Builder
	b.WriteString("# aha commands\n\n")
	b.WriteString("This file is generated from CLI command metadata. Update command metadata, then regenerate this file.\n\n")
	b.WriteString("## Global profiling\n\n")
	b.WriteString("Any command may write Go pprof profiles with `--cpuprofile FILE` and/or `--memprofile FILE`. These flags can appear before or after the subcommand, or be supplied via `AHA_CPU_PROFILE` and `AHA_MEM_PROFILE`. Profiles are local debugging artifacts and are not written unless explicitly requested.\n\n")
	b.WriteString("Examples: `aha --cpuprofile cpu.pprof search needle`, `aha verify --memprofile heap.pprof`.\n\n")
	b.WriteString("## JSON errors\n\n")
	b.WriteString("When a command is invoked with `--json`, failures are written to stderr as:\n\n")
	b.WriteString("```json\n{\n  \"error\": {\n    \"code\": \"machine_readable_code\",\n    \"message\": \"human-readable message\",\n    \"command\": \"command-name\",\n    \"next\": [\"aha doctor\"]\n  }\n}\n```\n\n")
	for _, name := range CommandNames() {
		cmd := Registry()[name]
		fmt.Fprintf(&b, "## aha %s\n\n", name)
		fmt.Fprintf(&b, "%s\n\n", cmd.Docs)
		fmt.Fprintf(&b, "```txt\n%s\n```\n\n", cmd.Usage)
		b.WriteString("**Flags:**\n\n")
		for _, flag := range cmd.Flags {
			fmt.Fprintf(&b, "- `%s`\n", flag)
		}
		b.WriteString("\n**Examples:**\n\n")
		for _, example := range cmd.Examples {
			fmt.Fprintf(&b, "- `%s`\n", example)
		}
		fmt.Fprintf(&b, "\n**JSON contract:** `%s`\n\n", cmd.JSONSchema)
	}
	return b.String()
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
	drv, err := depotDriverForConfig(req.Config, req.DepotOverride)
	if err != nil {
		return "", "", err
	}
	if err := ensureDepot(context.Background(), drv); err != nil {
		return "", "", err
	}
	tmpDir, err := os.MkdirTemp("", "aha-snapshot-*")
	if err != nil {
		return "", "", err
	}
	defer os.RemoveAll(tmpDir)
	opts := archive.Options{CapturedAt: req.CapturedAt, BundleID: req.BundleID, SessionFilters: req.SessionFilters, MaxSessions: req.MaxSessions}
	if opts.CapturedAt == "" {
		opts.CapturedAt = ahaclock.RealClock{}.Now().Format(time.RFC3339)
	}
	if opts.BundleID == "" {
		opts.BundleID = hash.RandomID()
	}
	opts.Clock = ahaclock.RealClock{}
	bundle, err := archive.Capture(context.Background(), req.Config, adapters.Builtins(), opts)
	if err != nil {
		return "", "", err
	}
	if req.SkipIfUnchanged {
		ref, ok, err := findDepotBundleWithSameState(context.Background(), drv, bundle.Manifest, tmpDir)
		if err != nil {
			return "", "", err
		}
		if ok {
			path := localDepotBundlePath(drv, ref)
			if path == "" {
				path = ref.Key
			}
			return path, ref.BundleSHA256, nil
		}
	}
	tmpPath := filepath.Join(tmpDir, fmt.Sprintf("aha-sessions-%s-%s-%s.tar.zst", safeName(req.Config.MachineID), safeTime(opts.CapturedAt), safeName(opts.BundleID)))
	info, err := archive.WriteWithInfo(tmpPath, bundle)
	if err != nil {
		return "", "", err
	}
	ref := depot.BundleRefFromWriteInfo(bundle.Manifest, info, filepath.Base(tmpPath))
	ref, _, err = depot.PutBundleKnown(context.Background(), drv, tmpPath, ref)
	if err != nil {
		return "", "", err
	}
	path := localDepotBundlePath(drv, ref)
	if path == "" {
		path = ref.Key
	}
	return path, info.BundleSHA256, nil
}

func reorderSearchArgs(args []string) []string {
	return reorderArgsBySpec(args, searchFlagSpecs)
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

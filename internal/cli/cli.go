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
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/adewale/aha/internal/config"
	"github.com/adewale/aha/internal/corpus"
	"github.com/adewale/aha/internal/model"
	"github.com/adewale/aha/internal/safety"
	"github.com/adewale/aha/internal/usererror"
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
	RunContext func(context.Context, []string, io.Writer, io.Writer) error
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
	Schema string       `json:"schema"`
	Error  errorPayload `json:"error"`
}

type errorPayload struct {
	Code        string                 `json:"code"`
	Message     string                 `json:"message"`
	Command     string                 `json:"command,omitempty"`
	Next        []string               `json:"next"`
	NextAction  usererror.Action       `json:"next_action"`
	Diagnostics []usererror.Diagnostic `json:"diagnostics"`
}

func Registry() map[string]Command {
	return map[string]Command{
		"refresh":   {Name: "refresh", Usage: "aha refresh [--session MATCH ...] [--max-sessions N] [--repo DIR] [--depot DEPOT] [--force] [--progress MODE] [--json]", Flags: []string{"--accept-secrets", "--captured-at", "--config", "--corpus", "--depot", "--force", "--machine", "--max-sessions", "--progress", "--repo", "--session", "--source", "--json"}, Examples: []string{"aha refresh", "aha refresh --session abc --max-sessions 1"}, JSONSchema: "object{push:object{manifest_sha256,reused,files,blobs_uploaded,blobs_existing,blobs_carried},report,reports}", Docs: "push this machine's state to the depot (unchanged state is recognized without re-uploading), then pull every machine's latest snapshot into the corpus", Run: cmdRefresh, RunContext: runRefreshContext},
		"snapshot":  {Name: "snapshot", Usage: "aha snapshot [--session MATCH ...] [--max-sessions N] [--depot DEPOT] [--force] [--progress MODE] [--json]", Flags: []string{"--accept-secrets", "--captured-at", "--config", "--depot", "--force", "--machine", "--max-sessions", "--progress", "--session", "--source", "--json"}, Examples: []string{"aha snapshot --accept-secrets --depot local:~/.aha/depot"}, JSONSchema: "object{manifest_sha256,reused,files,blobs_uploaded,blobs_existing,blobs_carried}", Docs: "push this machine's state to the depot: upload only new file versions, publish a snapshot manifest, move the pointer (no corpus needed; never downloads other machines' data)", Run: cmdSnapshot, RunContext: runSnapshotContext},
		"ingest":    {Name: "ingest", Usage: "aha ingest [--repo DIR] [--depot DEPOT] [--progress MODE] [--json] [bundle.tar.zst ...]", Flags: []string{"--config", "--corpus", "--depot", "--progress", "--repo", "--json"}, Examples: []string{"aha ingest ./bundle.tar.zst", "aha ingest --repo ./aha-repo", "aha ingest --depot local:~/.aha/depot"}, JSONSchema: "array<object{machine?,manifest_sha256?,bundle?,sessions,entries,messages,images,artifacts,duplicate}>", Docs: "pull every machine's latest depot snapshot into the corpus (fetching only unknown content), or import explicit v1 bundle files", Run: cmdIngest, RunContext: runIngestContext},
		"search":    {Name: "search", Usage: "aha search <query> [--repo DIR] [--source NAME] [--machine ID] [--role ROLE] [--project KEY] [--path-token TOKEN] [--json|--refs|--files|--md]", Flags: flagNames(searchFlagSpecs), FlagSpecs: searchFlagSpecs, Examples: []string{"aha search needle --json", "aha search needle --refs"}, JSONSchema: "array<object{score,timestamp,source,machine,project,role,snippet,session_key,entry_id,ref,ref_text}>", Docs: "find relevant messages/artifacts; use read on returned refs before answering", Run: cmdSearch},
		"read":      {Name: "read", Usage: "aha read [REF] [--session ID] [--entry ID] [--repo DIR] [--before N] [--after N] [--json|--md]", Flags: flagNames(readFlagSpecs), FlagSpecs: readFlagSpecs, Examples: []string{"aha read <ref_text> --json", "aha read --session <session> --entry <entry> --json"}, JSONSchema: "array<object{line_no,entry_id,timestamp,role,text,raw_json}>", Docs: "retrieve source context for a search result", Run: cmdRead},
		"status":    {Name: "status", Usage: "aha status [--repo DIR] [--depot DEPOT] [--json]", Flags: []string{"--config", "--corpus", "--depot", "--json", "--repo"}, Examples: []string{"aha status --json", "aha status --depot local:~/.aha/depot --json"}, JSONSchema: "object{corpus_dir,machines,sources,sessions,session_versions,entries,messages,artifacts,images,entry_assets,files,snapshots,conflicts,tool_invocations,fts_messages,fts_artifacts,session_path_tokens,artifact_path_tokens,index_size_bytes,depot_behind_snapshots?,depot_machines_listed?,depot_fetches?,next}", Docs: "summarize corpus health", Run: cmdStatus},
		"verify":    {Name: "verify", Usage: "aha verify [--repo DIR] [--repair-fts] [--progress MODE] [--json]", Flags: []string{"--config", "--corpus", "--json", "--progress", "--repair-fts", "--repo"}, Examples: []string{"aha verify --json", "aha verify --repair-fts"}, JSONSchema: "object{root,stats,problems,repaired_fts,fts_repair?}", Docs: "verify corpus invariants and optionally repair derived FTS rows", Run: cmdVerify, RunContext: runVerifyContext},
		"conflicts": {Name: "conflicts", Usage: "aha conflicts [--repo DIR] [--json]", Flags: []string{"--config", "--corpus", "--json", "--repo"}, Examples: []string{"aha conflicts --json"}, JSONSchema: "array<object{id,session_key,entry_id,first,second,created_at}>", Docs: "list quarantined merge conflicts", Run: cmdConflicts},
		"incidents": {Name: "incidents", Usage: "aha incidents [--repo DIR] [--limit N] [--state S] [--project P] [--source S] [--machine M] [--tool T] [--json]", Flags: []string{"--config", "--corpus", "--json", "--limit", "--machine", "--project", "--repo", "--source", "--state", "--tool"}, Examples: []string{"aha incidents --json", "aha incidents --state unresolved", "aha incidents --state resolved --project myrepo"}, JSONSchema: "array<object{tool_name,command_family,error_signature,episodes,distinct_sessions,distinct_projects,resolved,resolution_rate,state,tier,first_seen,last_seen,spark,paths:array<object{families,support,distinct_sessions,distinct_projects,confidence,sample_ref,sample_ordinal}>,sample_ref,score}>", Docs: "rank recurring tool-call failures with their resolution status (unresolved/partial/resolved) and the fix paths that worked", Run: cmdIncidents},
		"corpus":    {Name: "corpus", Usage: "aha corpus <size|vacuum|prune-orphans|rebuild> [--repo DIR] [--progress MODE] [--json] [--force|--backup]", Flags: []string{"--backup", "--config", "--corpus", "--force", "--json", "--progress", "--repo"}, Examples: []string{"aha corpus size --json", "aha corpus vacuum", "aha corpus prune-orphans --json", "aha corpus rebuild --backup --json"}, JSONSchema: "object{root,total_bytes,database_bytes,file_blob_bytes,image_blob_bytes,other_bytes,files}|object{before_bytes,after_bytes,reclaimed_bytes}|object{root,dry_run,orphan_bytes,deleted_files,deleted_bytes,orphans}|object{root,backup,next,next_action}", Docs: "inspect corpus disk usage, vacuum SQLite, explicitly prune unreferenced blobs, or atomically rebuild a pre-v2 corpus while preserving a backup", Run: cmdCorpus, RunContext: runCorpusContext},
		"export":    {Name: "export", Usage: "aha export [--machine ID] [--depot DEPOT] [--out FILE] [--json]", Flags: []string{"--config", "--depot", "--json", "--machine", "--out"}, Examples: []string{"aha export", "aha export --machine work-mac --out work.tar.zst"}, JSONSchema: "object{bundle,sha256,manifest_sha256,machine,files,bytes}", Docs: "materialize a machine's latest depot snapshot as a portable v1 bundle.tar.zst (the single-file hand-off format; re-import with aha ingest)", Run: cmdExport},
		"doctor":    {Name: "doctor", Usage: "aha doctor [--depot DEPOT] [--json]", Flags: []string{"--config", "--depot", "--json"}, Examples: []string{"aha doctor", "aha doctor --depot local:~/.aha/depot --json"}, JSONSchema: "object{version,config,adapters,sources,corpus,depot,next,next_action}", Docs: "show diagnostics and exactly one state-aware next action", Run: cmdDoctor},
		"depot":     {Name: "depot", Usage: "aha depot <setup|init|use|ls|verify> [DEPOT] [--progress MODE] [--json] [--deep]", Flags: []string{"--config", "--deep", "--json", "--progress"}, Examples: []string{"aha depot setup r2:aha-depot --json", "aha depot init local:~/.aha/depot", "aha depot init r2:aha-depot", "aha depot use r2:aha-depot", "aha depot ls --json", "aha depot verify --deep"}, JSONSchema: "object|array", Docs: "preflight R2 with one safe next action, initialize a depot, switch the default, list snapshots, or verify content", Run: cmdDepot, RunContext: runDepotContext},
		"init":      {Name: "init", Usage: "aha init [--config PATH] [--accept-secrets] [--json]", Flags: []string{"--accept-secrets", "--config", "--json"}, Examples: []string{"aha init --accept-secrets"}, JSONSchema: "object{config,accepted_secrets}", Docs: "write starter JSONC config", Run: cmdInit},
		"mcp":       {Name: "mcp", Usage: "aha mcp [--config PATH] [--repo DIR] [--dry-run]", Flags: []string{"--config", "--corpus", "--dry-run", "--repo"}, Examples: []string{"aha mcp", "aha mcp --dry-run"}, JSONSchema: "jsonrpc:tools/list|tools/call (stdio MCP)", Docs: "run a read-only stdio MCP server over the corpus", Run: cmdMcp},
		"serve":     {Name: "serve", Usage: "aha serve [--addr HOST:PORT] [--allow-remote] [--allowed-hosts H1,H2] [--timeout DUR] [--token TOKEN] [--config PATH] [--repo DIR]", Flags: []string{"--addr", "--allow-remote", "--allowed-hosts", "--config", "--corpus", "--repo", "--timeout", "--token"}, Examples: []string{"aha serve", "aha serve --addr 127.0.0.1:18428", "aha serve --allow-remote --token $(openssl rand -hex 32)"}, JSONSchema: "http://HOST:PORT/api/{search,read,incidents,incident_trajectory,overview,status,verify,conflicts,corpus_size,doctor}", Docs: "run a read-only local dashboard over the corpus on loopback", Run: cmdServe},
	}
}

func Run(args []string, stdout, stderr io.Writer) error {
	return RunContext(context.Background(), args, stdout, stderr)
}

func RunContext(ctx context.Context, args []string, stdout, stderr io.Writer) error {
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
		return &CommandError{Code: "unknown_command", Command: args[0], Err: fmt.Errorf("unknown command %q", args[0]), Next: []string{"aha help"}}
	}
	run := cmd.Run
	if cmd.RunContext != nil {
		if err := cmd.RunContext(ctx, args[1:], stdout, stderr); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil
			}
			return err
		}
		return nil
	}
	if err := run(args[1:], stdout, stderr); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	return nil
}

func RunMain(args []string, stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	errorCleanArgs, errorOpts := errorOptionsFromArgs(args)
	cleanArgs, profileOpts, profileErr := profileOptionsFromArgs(errorCleanArgs)
	if profileErr != nil {
		view := publicErrorView(profileErr, cleanArgs)
		if wantsJSON(errorCleanArgs) {
			_ = writePresentedError(stderr, machineError(view, errorOpts.Verbose), structuredProgressRequested(cleanArgs))
		} else {
			renderHumanError(stderr, view, errorOpts.Verbose)
		}
		return 1
	}
	if wantsJSON(cleanArgs) {
		if wantsLiveProgress(cleanArgs, stderr) {
			if err := runWithProfiling(profileOpts, func() error { return RunContext(ctx, cleanArgs, stdout, stderr) }); err != nil {
				_ = writePresentedError(stderr, machineError(publicErrorView(err, cleanArgs), errorOpts.Verbose), structuredProgressRequested(cleanArgs))
				return 1
			}
			return 0
		}
		var commandStderr bytes.Buffer
		if err := runWithProfiling(profileOpts, func() error { return RunContext(ctx, cleanArgs, stdout, &commandStderr) }); err != nil {
			_ = writePresentedError(stderr, machineError(publicErrorView(err, cleanArgs), errorOpts.Verbose), false)
			return 1
		}
		_, _ = stderr.Write(commandStderr.Bytes())
		return 0
	}
	if err := runWithProfiling(profileOpts, func() error { return RunContext(ctx, cleanArgs, stdout, stderr) }); err != nil {
		renderHumanError(stderr, publicErrorView(err, cleanArgs), errorOpts.Verbose)
		return 1
	}
	return 0
}

func wantsLiveProgress(args []string, stderr io.Writer) bool {
	requested := "auto"
	for i, arg := range args {
		if strings.HasPrefix(arg, "--progress=") {
			requested = strings.TrimPrefix(arg, "--progress=")
		}
		if arg == "--progress" && i+1 < len(args) {
			requested = args[i+1]
		}
	}
	switch requested {
	case "json", "plain", "tty":
		return true
	case "off":
		return false
	default:
		return writerIsTerminal(stderr)
	}
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

func machineError(view usererror.View, verbose bool) errorEnvelope {
	diagnostics := []usererror.Diagnostic{}
	if verbose {
		diagnostics = usererror.Diagnostics(view)
	}
	return errorEnvelope{
		Schema: "aha.error.v1",
		Error: errorPayload{
			Code: string(view.Code()), Message: view.Message(), Command: view.Command(),
			Next: []string{view.Next().Text()}, NextAction: view.Next(), Diagnostics: diagnostics,
		},
	}
}

func Usage(w io.Writer) {
	fmt.Fprintf(w, "aha %s\n\nUsage:\n", model.Version)
	fmt.Fprintln(w, "  aha [--cpuprofile FILE] [--memprofile FILE] [--verbose-errors] <command> [args]")
	names := CommandNames()
	for _, name := range names {
		fmt.Fprintf(w, "  %s\n", Registry()[name].Usage)
	}
	fmt.Fprintln(w, "\nGlobal profiling flags and --verbose-errors may also be supplied after the subcommand.")
}

func GenerateCommandsMarkdown() string {
	var b strings.Builder
	b.WriteString("# aha commands\n\n")
	b.WriteString("This file is generated from CLI command metadata. Update command metadata, then regenerate this file.\n\n")
	b.WriteString("## Global profiling\n\n")
	b.WriteString("Any command may write Go pprof profiles with `--cpuprofile FILE` and/or `--memprofile FILE`. These flags can appear before or after the subcommand, or be supplied via `AHA_CPU_PROFILE` and `AHA_MEM_PROFILE`. Profiles are local debugging artifacts and are not written unless explicitly requested.\n\n")
	b.WriteString("Examples: `aha --cpuprofile cpu.pprof search needle`, `aha verify --memprofile heap.pprof`.\n\n")
	b.WriteString("## Error contract\n\n")
	b.WriteString("Every failed command prints one concise, credential-safe error and exactly one `next:` action. Raw dependency, SQL, SDK, and filesystem errors are not public output. Add global `--verbose-errors` for allowlisted diagnostics (failure kind, operation, retryability), never raw causes.\n\n")
	b.WriteString("When a command is invoked with `--json`, failures are written to stderr using the stable `aha.error.v1` envelope:\n\n")
	b.WriteString("```json\n{\n  \"schema\": \"aha.error.v1\",\n  \"error\": {\n    \"code\": \"machine_readable_code\",\n    \"message\": \"safe human-readable message\",\n    \"command\": \"command-name\",\n    \"next\": [\"aha doctor --json\"],\n    \"next_action\": {\"command\": \"aha\", \"args\": [\"doctor\", \"--json\"]},\n    \"diagnostics\": []\n  }\n}\n```\n\n")
	b.WriteString("With `--progress=json`, stderr remains valid NDJSON: progress events are followed by one terminal `aha.error.v1` object on failure.\n\n")
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

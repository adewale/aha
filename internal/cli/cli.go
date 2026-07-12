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
		"analyse":   {Name: "analyse", Usage: "aha analyse failures [--workspace PATH] [--limit N] [--state S] [--project P] [--source S] [--machine M] [--tool T] [--json]", Flags: []string{"--config", "--json", "--limit", "--machine", "--project", "--source", "--state", "--tool", "--workspace"}, Examples: []string{"aha analyse failures --json", "aha analyse failures --state unresolved"}, JSONSchema: "array<object{tool_name,command_family,error_signature,episodes,distinct_sessions,distinct_projects,resolved,resolution_rate,state,tier,first_seen,last_seen,spark,paths,sample_ref,score}>", Docs: "rank recurring tool-call failures and the resolution paths that worked", Run: cmdAnalyse},
		"archive":   {Name: "archive", Usage: "aha archive <init|set-default|status|upload|download|verify> [ARCHIVE] [--workspace PATH] [--deep] [--dry-run] [--progress MODE] [--json]", Flags: []string{"--config", "--deep", "--dry-run", "--force", "--json", "--progress", "--workspace"}, Examples: []string{"aha archive init", "aha archive upload", "aha archive download", "aha archive status r2:team-history --json", "aha archive verify --deep"}, JSONSchema: "aha.archive.status.v2|object{state,...}", Docs: "manage durable aggregated history and explicit upload/download transitions", Run: cmdArchive, RunContext: runArchiveContext},
		"dashboard": {Name: "dashboard", Usage: "aha dashboard [--workspace PATH] [--addr HOST:PORT] [--allow-remote] [--allowed-hosts H1,H2] [--timeout DUR] [--token TOKEN]", Flags: []string{"--addr", "--allow-remote", "--allowed-hosts", "--config", "--timeout", "--token", "--workspace"}, Examples: []string{"aha dashboard", "aha dashboard --addr 127.0.0.1:18428"}, JSONSchema: "http://HOST:PORT/api/{search,show,analyse,overview,status,workspace_verify,workspace_conflicts}", Docs: "run the read-only local dashboard over a Workspace", Run: cmdDashboard},
		"init":      {Name: "init", Usage: "aha init --acknowledge-raw-history [--config PATH] [--dry-run] [--json]", Flags: []string{"--acknowledge-raw-history", "--config", "--dry-run", "--json"}, Examples: []string{"aha init --acknowledge-raw-history"}, JSONSchema: "object{config,acknowledged_raw_history,archive,workspace}", Docs: "write config, assign the machine, initialise the default local Archive, and prepare the default Workspace destination", Run: cmdInit},
		"mcp":       {Name: "mcp", Usage: "aha mcp <check|serve> [--workspace PATH] [--config PATH]", Flags: []string{"--config", "--workspace"}, Examples: []string{"aha mcp check", "aha mcp serve"}, JSONSchema: "check diagnostic|jsonrpc:tools/list|tools/call", Docs: "check or serve the read-only stdio MCP interface over a Workspace", Run: cmdMcp},
		"search":    {Name: "search", Usage: "aha search [--workspace PATH] QUERY [--source NAME] [--machine ID] [--role ROLE] [--project KEY] [--path-token TOKEN] [--json|--refs|--files|--md]", Flags: flagNames(searchFlagSpecs), FlagSpecs: searchFlagSpecs, Examples: []string{"aha search needle --json", "aha search needle --refs"}, JSONSchema: "array<object{score,timestamp,source,machine,project,role,snippet,session_key,entry_id,ref,ref_text}>", Docs: "find relevant messages and artefacts; use show on returned refs before answering", Run: cmdSearch},
		"show":      {Name: "show", Usage: "aha show [--workspace PATH] REF [--session ID] [--entry ID] [--before N] [--after N] [--json|--md]", Flags: flagNames(showFlagSpecs), FlagSpecs: showFlagSpecs, Examples: []string{"aha show <ref_text> --json", "aha show --session <session> --entry <entry> --json"}, JSONSchema: "array<object{line_no,entry_id,timestamp,role,text,raw_json}>", Docs: "display contextual evidence for a search result", Run: cmdShow},
		"status":    {Name: "status", Usage: "aha status [--archive ARCHIVE] [--workspace PATH] [--json]", Flags: []string{"--archive", "--config", "--json", "--workspace"}, Examples: []string{"aha status --json", "aha status --archive r2:team-history --workspace ~/.aha/workspace --json"}, JSONSchema: "aha.status.v2", Docs: "inspect agent-history, Archive, and Workspace state with one next transition", Run: cmdStatus, RunContext: runStatusContext},
		"workspace": {Name: "workspace", Usage: "aha workspace <set-default|status|verify|repair|conflicts> [PATH] [--repair-fts] [--backup] [--dry-run] [--progress MODE] [--json]", Flags: []string{"--backup", "--config", "--dry-run", "--json", "--progress", "--repair-fts"}, Examples: []string{"aha workspace status", "aha workspace verify --repair-fts", "aha workspace repair --backup", "aha workspace conflicts --json"}, JSONSchema: "aha.workspace.status.v2|object|array", Docs: "select, inspect, verify, repair, or inspect conflicts in a local Workspace", Run: cmdWorkspace, RunContext: runWorkspaceContext},
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
		if len(args) == 2 && args[1] == "--json" {
			return writeJSON(stdout, model.RunningBuild())
		}
		if len(args) != 1 {
			return fmt.Errorf("version accepts only --json")
		}
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
	b.WriteString("Any command may write Go pprof profiles with `--cpuprofile FILE` and/or `--memprofile FILE`. These flags can appear before or after the subcommand, or be supplied via `AHA_CPU_PROFILE` and `AHA_MEM_PROFILE`. Profiles are local debugging artefacts and are not written unless explicitly requested.\n\n")
	b.WriteString("Examples: `aha --cpuprofile cpu.pprof search needle`, `aha workspace verify --memprofile heap.pprof`.\n\n")
	b.WriteString("## Error contract\n\n")
	b.WriteString("Every failed command prints one concise, credential-safe error and exactly one `next:` action. Raw dependency, SQL, SDK, and filesystem errors are not public output. Add global `--verbose-errors` for allowlisted diagnostics (failure kind, operation, retryability), never raw causes.\n\n")
	b.WriteString("When a command is invoked with `--json`, failures are written to stderr using the stable `aha.error.v1` envelope:\n\n")
	b.WriteString("```json\n{\n  \"schema\": \"aha.error.v1\",\n  \"error\": {\n    \"code\": \"machine_readable_code\",\n    \"message\": \"safe human-readable message\",\n    \"command\": \"command-name\",\n    \"next\": [\"aha status --json\"],\n    \"next_action\": {\"command\": \"aha\", \"args\": [\"status\", \"--json\"]},\n    \"diagnostics\": []\n  }\n}\n```\n\n")
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

type workspaceFlags struct {
	workspaceDir *string
	config       *string
}

func registerWorkspaceFlags(fs *flag.FlagSet) workspaceFlags {
	return workspaceFlags{workspaceDir: fs.String("workspace", "", "Workspace directory"), config: fs.String("config", "", "config path")}
}

func (f workspaceFlags) loadConfig() (model.Config, error) {
	cfg, err := config.Load(*f.config)
	if err != nil {
		return cfg, err
	}
	applyWorkspaceOverride(&cfg, *f.workspaceDir)
	return cfg, nil
}

func applyWorkspaceOverride(cfg *model.Config, workspaceDir string) {
	if workspaceDir != "" {
		cfg.WorkspaceDir = workspaceDir
	}
}

func prepareWritableCorpus(cfg model.Config) (safety.WorkspaceDestination, error) {
	return safety.PrepareWorkspaceDestination(cfg, cfg.WorkspaceDir)
}

func openPreparedCorpus(destination safety.WorkspaceDestination) (*corpus.Store, error) {
	path, err := destination.Path()
	if err != nil {
		return nil, err
	}
	return corpus.Open(path)
}

func openCorpusForCommand(cfg model.Config, create bool) (*corpus.Store, error) {
	if create {
		destination, err := prepareWritableCorpus(cfg)
		if err != nil {
			return nil, err
		}
		return openPreparedCorpus(destination)
	}
	if err := safety.ValidateWriteOutsideSources(cfg, cfg.WorkspaceDir, "Workspace"); err != nil {
		return nil, err
	}
	return corpus.OpenExistingReadOnly(cfg.WorkspaceDir)
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

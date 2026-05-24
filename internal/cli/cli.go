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
	"github.com/adewale/aha/internal/config"
	"github.com/adewale/aha/internal/corpus"
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
		"refresh":   {Name: "refresh", Usage: "aha refresh [--session MATCH ...] [--max-sessions N] [--repo DIR] [--depot DEPOT] [--json]", Flags: []string{"--accept-secrets", "--bundle-id", "--captured-at", "--config", "--corpus", "--depot", "--machine", "--max-sessions", "--repo", "--session", "--source", "--json"}, Examples: []string{"aha refresh", "aha refresh --session abc --max-sessions 1"}, JSONSchema: "object{bundle,sha256,report}", Docs: "snapshot configured sources to the depot and ingest new bundles", Run: cmdRefresh},
		"snapshot":  {Name: "snapshot", Usage: "aha snapshot [--session MATCH ...] [--max-sessions N] [--depot DEPOT] [--json]", Flags: []string{"--accept-secrets", "--bundle-id", "--captured-at", "--config", "--depot", "--machine", "--max-sessions", "--session", "--source", "--json"}, Examples: []string{"aha snapshot --accept-secrets --depot local:./bundles"}, JSONSchema: "object{bundle,sha256,bundle_id,captured_at}", Docs: "create an immutable local history bundle and store it in a depot", Run: cmdSnapshot},
		"ingest":    {Name: "ingest", Usage: "aha ingest [--repo DIR] [--depot DEPOT] [--json] [bundle.tar.zst ...]", Flags: []string{"--config", "--corpus", "--depot", "--repo", "--json"}, Examples: []string{"aha ingest ./bundle.tar.zst", "aha ingest --repo ./aha-repo", "aha ingest --depot local:~/.aha/depot"}, JSONSchema: "array<object{bundle,sessions,entries,messages,images,artifacts,duplicate}>", Docs: "merge one or more bundles into a corpus", Run: cmdIngest},
		"search":    {Name: "search", Usage: "aha search <query> [--repo DIR] [--source NAME] [--machine ID] [--role ROLE] [--json|--refs|--files|--md]", Flags: flagNames(searchFlagSpecs), FlagSpecs: searchFlagSpecs, Examples: []string{"aha search needle --json", "aha search needle --refs"}, JSONSchema: "array<object{score,timestamp,source,machine,project,role,snippet,session_key,entry_id,ref,ref_text}>", Docs: "find relevant messages/artifacts; use read on returned refs before answering", Run: cmdSearch},
		"read":      {Name: "read", Usage: "aha read [REF] [--session ID] [--entry ID] [--repo DIR] [--before N] [--after N] [--json|--md]", Flags: flagNames(readFlagSpecs), FlagSpecs: readFlagSpecs, Examples: []string{"aha read <session>#<entry> --json", "aha read --session <session> --entry <entry> --json"}, JSONSchema: "array<object{line_no,entry_id,timestamp,role,text,raw_json}>", Docs: "retrieve source context for a search result", Run: cmdRead},
		"status":    {Name: "status", Usage: "aha status [--repo DIR] [--depot DEPOT] [--json]", Flags: []string{"--config", "--corpus", "--depot", "--json", "--repo"}, Examples: []string{"aha status --json", "aha status --depot local:~/.aha/depot --json"}, JSONSchema: "object{corpus_dir,sessions,entries,messages,artifacts,images,bundles,conflicts,index_size_bytes,depot_behind_bundles,next}", Docs: "summarize corpus health", Run: cmdStatus},
		"conflicts": {Name: "conflicts", Usage: "aha conflicts [--repo DIR] [--json]", Flags: []string{"--config", "--corpus", "--json", "--repo"}, Examples: []string{"aha conflicts --json"}, JSONSchema: "array<object{id,session_key,entry_id,first,second,created_at}>", Docs: "list quarantined merge conflicts", Run: cmdConflicts},
		"doctor":    {Name: "doctor", Usage: "aha doctor [--depot DEPOT] [--json]", Flags: []string{"--config", "--depot", "--json"}, Examples: []string{"aha doctor", "aha doctor --depot local:~/.aha/depot --json"}, JSONSchema: "object{version,config,adapters,depot,next}", Docs: "show diagnostics and next actions", Run: cmdDoctor},
		"depot":     {Name: "depot", Usage: "aha depot <init|ls|verify> [DEPOT] [--json]", Flags: []string{"--config", "--json", "--repair"}, Examples: []string{"aha depot init local:~/.aha/depot", "aha depot ls --json"}, JSONSchema: "object|array", Docs: "initialize, list, or verify a bundle depot", Run: cmdDepot},
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
	if wantsJSON(args) {
		var commandStderr bytes.Buffer
		if err := Run(args, stdout, &commandStderr); err != nil {
			_ = writeJSON(stderr, errorEnvelope{Error: machineError(err, args)})
			return 1
		}
		_, _ = stderr.Write(commandStderr.Bytes())
		return 0
	}
	if err := Run(args, stdout, stderr); err != nil {
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
	names := CommandNames()
	for _, name := range names {
		fmt.Fprintf(w, "  %s\n", Registry()[name].Usage)
	}
}

func GenerateCommandsMarkdown() string {
	var b strings.Builder
	b.WriteString("# aha commands\n\n")
	b.WriteString("This file is generated from CLI command metadata. Update command metadata, then regenerate this file.\n\n")
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
		opts.CapturedAt = time.Now().UTC().Format(time.RFC3339)
	}
	if opts.BundleID == "" {
		opts.BundleID = hash.RandomID()
	}
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
	sha, err := archive.Write(tmpPath, bundle)
	if err != nil {
		return "", "", err
	}
	ref, _, err := drv.PutBundle(context.Background(), tmpPath)
	if err != nil {
		return "", "", err
	}
	path := localDepotBundlePath(drv, ref)
	if path == "" {
		path = ref.Key
	}
	receipt := map[string]any{"bundle": path, "sha256": sha, "bundle_id": opts.BundleID, "captured_at": opts.CapturedAt, "depot": drv.Address()}
	if local := localDepotBundlePath(drv, ref); local != "" {
		rb, _ := json.MarshalIndent(receipt, "", "  ")
		_ = os.WriteFile(local+".receipt.json", append(rb, '\n'), 0o644)
	}
	return path, sha, nil
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

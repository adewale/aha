// Package mcp wires aha's read-only tool surface into the official
// github.com/modelcontextprotocol/go-sdk Server. The tools/* functions
// previously hand-rolled here now live behind the SDK's typed
// AddTool[In, Out] registration. The wire format, framing, schema
// validation, structuredContent emission, annotations, ping, and
// initialize negotiation are all the SDK's responsibility — we only
// supply Go-typed input structs, the business logic that reads the
// corpus, and a small dispatcher (CallTool) that the HTTP dashboard
// reuses without going through the SDK.
package mcp

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/adewale/aha/internal/adapters"
	"github.com/adewale/aha/internal/config"
	"github.com/adewale/aha/internal/corpus"
	"github.com/adewale/aha/internal/model"
	"github.com/adewale/aha/internal/search"
	"github.com/adewale/aha/internal/usererror"
)

// Backend is the read surface the MCP tools need from the rest of aha.
// Defined as an interface so tests can stub it.
type Backend interface {
	DB() *sql.DB
	Store() *corpus.Store
	Root() string
	Config() model.Config
}

// CorpusBackend wraps a corpus.Store and a model.Config.
type CorpusBackend struct {
	store *corpus.Store
	cfg   model.Config
}

func NewCorpusBackend(store *corpus.Store, cfg model.Config) *CorpusBackend {
	return &CorpusBackend{store: store, cfg: cfg}
}

func (b *CorpusBackend) DB() *sql.DB          { return b.store.DB }
func (b *CorpusBackend) Store() *corpus.Store { return b.store }
func (b *CorpusBackend) Root() string         { return b.store.Root }
func (b *CorpusBackend) Config() model.Config { return b.cfg }

// serverInfo is the implementation block we hand to NewServer.
var serverInfo = &mcp.Implementation{Name: "aha", Version: model.Version}

// ToolNames is the canonical, sorted list of tool names this server
// registers. Exported so cross-language conformance tests (Python, TS, Go)
// and the codegen drift test can assert against a single source of truth
// rather than each hard-coding the same list. Update it alongside the
// AddTool calls in registerTools and a test in tools_test.go will fail
// loudly if the registered set drifts.
var ToolNames = []string{
	"conflicts",
	"corpus_size",
	"doctor",
	"incident_trajectory",
	"incidents",
	"overview",
	"read",
	"search",
	"status",
	"verify",
}

// ToolDescriptions is the canonical per-tool description text. The
// registerTools function passes these strings into the SDK as the human-
// readable tool descriptions, and internal/mcp/codegen reads them when
// emitting JSDoc on each method of the generated TS aha() factory so a
// code-mode LLM reading the surface gets the same descriptions it would
// get over tools/list. Update here and both surfaces move together.
var ToolDescriptions = map[string]string{
	"search":              "Search the corpus over messages and artifacts. Returns ref-bearing results suitable for chaining into read.",
	"read":                "Retrieve full surrounding context for a search hit. Accepts either a canonical ref text or session+entry coordinates. mode='branch' walks the Pi parent_id tree from the entry leaf to the root; mode='live' adds compaction collapse and filters non-participating entries.",
	"status":              "Return corpus health summary: counts and disk usage.",
	"verify":              "Run read-only corpus invariant checks (no repair).",
	"conflicts":           "List quarantined merge conflicts.",
	"corpus_size":         "Return corpus on-disk size breakdown.",
	"doctor":              "Return local environment, config, source, and corpus diagnostics. Depot probing is omitted to keep this tool local-only.",
	"incidents":           "The failure-and-fix view: one row per recurring tool-call failure carrying both its recurrence (episodes, distinct sessions/projects, first/last seen, an occurrence sparkline) and its resolution status (state unresolved/partial/resolved, rate, tentative/established tier, and top resolution paths ranked by Wilson-lower-bound confidence x spread, each with a ref into a sample resolving success). Optional project/source/machine/tool facets. The single surface for 'what keeps breaking, and do we know how to fix it?'; filter state=unresolved for the unsolved-pain to-do list, or state=resolved for skills worth harvesting. Identities and paths are normalized command families / error signatures — never raw tool output.",
	"incident_trajectory": "Reconstruct the full fail->fix arc behind a resolving-success ref (the sample_ref carried by an incident resolution path) and, for multi-call entries, that path's sample_ordinal: every tool call from the failing opener through the resolving success, in order, each with a ref to read it.",
	"overview":            "Corpus orientation summary: session/entry/message/tool-call counts, source/machine/top-project breakdowns, the session time span, and on-disk index size. Answers 'what is in this corpus and is it healthy?'.",
}

// ---------- Input structs (jsonschema tags drive the SDK schema generator) ----------

// SearchInput names the documented filter set for the search tool. The
// JSON-Schema this maps to is `additionalProperties: false` by virtue of
// AddTool's strict-input behaviour, so MCP clients sending unknown fields
// are rejected before the handler runs.
type SearchInput struct {
	Query     string `json:"query" jsonschema:"Full-text query passed to the corpus FTS index"`
	Source    string `json:"source,omitempty" jsonschema:"Filter by source adapter name (pi, claude-code, codex)"`
	Machine   string `json:"machine,omitempty" jsonschema:"Filter by machine id"`
	Role      string `json:"role,omitempty" jsonschema:"Filter by message role (user, assistant, ...)"`
	After     string `json:"after,omitempty" jsonschema:"RFC3339 lower bound on timestamps"`
	Before    string `json:"before,omitempty" jsonschema:"RFC3339 upper bound on timestamps"`
	Path      string `json:"path,omitempty" jsonschema:"Substring filter against session/artefact paths"`
	PathToken string `json:"path_token,omitempty" jsonschema:"Indexed path-segment filter"`
	Project   string `json:"project,omitempty" jsonschema:"Indexed project filter"`
	Limit     int    `json:"limit,omitempty" jsonschema:"Cap on returned hits (default 20, max 200)"`
}

// ReadInput accepts either a canonical ref (preferred) or a session+entry
// pair. Both modes resolve via the same corpus.Read* functions the CLI uses.
type ReadInput struct {
	Ref     string `json:"ref,omitempty" jsonschema:"Canonical ref text (msg:v1:..., session:v1:..., artifact:v1:...)"`
	Session string `json:"session,omitempty" jsonschema:"Session key (used when ref is empty)"`
	Entry   string `json:"entry,omitempty" jsonschema:"Entry id within the session. In branch/live mode this is the leaf entry to walk back from."`
	Mode    string `json:"mode,omitempty" jsonschema:"Read mode: 'window' (default, file-order context around the entry), 'branch' (walk parent_id from the entry leaf to the root), or 'live' (branch with Pi compaction collapse and non-participating entries filtered)."`
	Before  *int   `json:"before,omitempty" jsonschema:"Lines of context before the target entry (window mode only, default 3; explicit 0 is honored)"`
	After   *int   `json:"after,omitempty" jsonschema:"Lines of context after the target entry (window mode only, default 5; explicit 0 is honored)"`
}

// IncidentsInput parameterizes the failure-and-fix view, with optional
// facet filters.
type IncidentsInput struct {
	Limit   int    `json:"limit,omitempty" jsonschema:"Cap on returned incidents (default 50, max 200)"`
	Project string `json:"project,omitempty" jsonschema:"Filter to one project key"`
	Source  string `json:"source,omitempty" jsonschema:"Filter to one source adapter (pi, claude-code, codex, opencode)"`
	Machine string `json:"machine,omitempty" jsonschema:"Filter to one machine id"`
	Tool    string `json:"tool,omitempty" jsonschema:"Filter to one tool name"`
	State   string `json:"state,omitempty" jsonschema:"Filter by incident state: unresolved, partial, or resolved"`
}

// IncidentTrajectoryInput names the resolving-success ref to reconstruct an arc
// from.
type IncidentTrajectoryInput struct {
	Ref     string `json:"ref" jsonschema:"Resolving-success ref (msg:v1:...), e.g. an incident path sample_ref"`
	Ordinal *int   `json:"ordinal,omitempty" jsonschema:"Resolving invocation ordinal from the incident path sample_ordinal; required when one transcript entry resolved multiple incidents"`
}

// EmptyInput is used as the In parameter for tools that take no arguments.
// Keeping it as a typed empty struct (rather than `any`) makes AddTool
// reject extraneous arguments at the SDK boundary.
type EmptyInput struct{}

// ---------- Pure business logic (used by both the SDK handlers and CallTool) ----------

func doSearch(b Backend, in SearchInput) ([]search.Result, error) {
	if in.Query == "" {
		return nil, fmt.Errorf("missing required argument for search: query")
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > search.MaxLimit {
		limit = search.MaxLimit
	}
	results, err := search.Query(b.DB(), in.Query, search.Filters{
		Source:    in.Source,
		Machine:   in.Machine,
		Role:      in.Role,
		After:     in.After,
		Before:    in.Before,
		Path:      in.Path,
		PathToken: in.PathToken,
		Project:   in.Project,
		Limit:     limit,
	})
	if err != nil {
		return nil, err
	}
	if results == nil {
		results = []search.Result{}
	}
	return results, nil
}

const MaxReadContextEntries = 200

func doRead(b Backend, in ReadInput) ([]corpus.ReadEntry, error) {
	var (
		entries []corpus.ReadEntry
		err     error
	)
	switch in.Mode {
	case "", "window":
		before := 3
		if in.Before != nil {
			before = clampNonNegative(*in.Before, MaxReadContextEntries)
		}
		after := 5
		if in.After != nil {
			after = clampNonNegative(*in.After, MaxReadContextEntries)
		}
		if in.Ref != "" {
			ref, perr := model.ParseRef(in.Ref)
			if perr != nil {
				return nil, fmt.Errorf("invalid ref: %w", perr)
			}
			entries, err = corpus.ReadCanonical(b.DB(), ref, before, after)
		} else {
			if in.Session == "" {
				return nil, fmt.Errorf("read requires either ref or session")
			}
			entries, err = corpus.ReadContext(b.DB(), in.Session, in.Entry, before, after)
		}
	case "branch", "live":
		if in.Session == "" {
			return nil, fmt.Errorf("read mode %q requires session", in.Mode)
		}
		if in.Entry == "" {
			return nil, fmt.Errorf("read mode %q requires entry (the leaf to walk back from)", in.Mode)
		}
		if in.Mode == "branch" {
			entries, err = corpus.ReadBranch(b.DB(), in.Session, in.Entry)
		} else {
			entries, err = corpus.LiveContext(b.DB(), in.Session, in.Entry)
		}
	default:
		return nil, fmt.Errorf("unknown read mode %q (want window, branch, or live)", in.Mode)
	}
	if err != nil {
		return nil, err
	}
	if entries == nil {
		entries = []corpus.ReadEntry{}
	}
	return entries, nil
}

func doStatus(b Backend) (map[string]any, error) {
	return corpus.Status(b.DB(), b.Root())
}

func doVerify(b Backend) (corpus.VerifyReport, error) {
	return corpus.Verify(b.Store())
}

func doConflicts(b Backend) ([]corpus.Conflict, error) {
	rows, err := corpus.Conflicts(b.DB())
	if err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []corpus.Conflict{}
	}
	return rows, nil
}

func doCorpusSize(b Backend) (corpus.SizeReport, error) {
	return corpus.Size(b.Store())
}

// doIncidents passes the requested limit straight through; the corpus layer
// owns the default (50) and the MaxClusterLimit clamp.
func doIncidents(b Backend, in IncidentsInput) ([]corpus.Incident, error) {
	rows, err := corpus.Incidents(b.DB(), corpus.IncidentFilter{
		Limit:   in.Limit,
		Project: in.Project,
		Source:  in.Source,
		Machine: in.Machine,
		Tool:    in.Tool,
		State:   in.State,
	})
	if err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []corpus.Incident{}
	}
	return rows, nil
}

func doIncidentTrajectory(b Backend, in IncidentTrajectoryInput) ([]corpus.TrajectoryStep, error) {
	if in.Ref == "" {
		return nil, fmt.Errorf("missing required argument for incident_trajectory: ref")
	}
	steps, err := corpus.IncidentTrajectory(b.DB(), in.Ref, in.Ordinal)
	if err != nil {
		return nil, err
	}
	if steps == nil {
		steps = []corpus.TrajectoryStep{}
	}
	return steps, nil
}

func doOverview(b Backend) (corpus.Overview, error) {
	return corpus.CorpusOverview(b.DB())
}

func doDoctor(b Backend) (map[string]any, error) {
	cfg := b.Config()
	names := make([]string, 0, len(adapters.Builtins()))
	for n := range adapters.Builtins() {
		names = append(names, n)
	}
	sort.Strings(names)
	var ads []map[string]any
	for _, name := range names {
		ad := adapters.Builtins()[name]
		ads = append(ads, map[string]any{
			"name":          name,
			"version":       ad.Version(),
			"capabilities":  ad.Capabilities(),
			"default_roots": ad.DefaultRoots(),
		})
	}
	var sources []map[string]any
	for _, sc := range cfg.Sources {
		sources = append(sources, map[string]any{
			"type":    sc.Type,
			"root":    sc.Root,
			"enabled": sc.Enabled,
		})
	}
	corpusBlock := map[string]any{
		"path": b.Root(),
		"ok":   true,
	}
	if st, err := corpus.Status(b.DB(), b.Root()); err == nil {
		corpusBlock["entries"] = st["entries"]
		corpusBlock["sessions"] = st["sessions"]
		corpusBlock["index_size_bytes"] = st["index_size_bytes"]
	}
	return map[string]any{
		"version":  model.Version,
		"config":   config.DefaultPath(),
		"adapters": ads,
		"sources":  sources,
		"corpus":   corpusBlock,
		"next":     []string{"aha search <query>", "aha read <ref>"},
	}, nil
}

// ---------- SDK registration ----------
//
// Two patterns by output shape:
//
//   - Object-typed tools (status, verify, corpus_size, doctor) pass a typed
//     Out value as the second return; the SDK derives the output schema
//     from Out, marshals it once, and fills BOTH CallToolResult.Content
//     (as a TextContent block) AND CallToolResult.StructuredContent from
//     the same bytes. We return nil for the *CallToolResult.
//
//   - List-typed tools (search, read, incidents, conflicts) declare Out=any so the
//     SDK skips output-schema derivation — array schemas would otherwise
//     panic the SDK with "output schema must have type object" (which
//     matches the official Python SDK's Pydantic Dict[str, Any] constraint
//     on structuredContent). We hand-build CallToolResult.Content with the
//     JSON-marshalled list payload; structuredContent stays nil for these.
//
// Input validation is the SDK's job too. AddTool derives the input schema
// from the typed In struct via jsonschema-go, which emits
// `additionalProperties: false` for structs by default, and the SDK then
// validates every tools/call's arguments against that schema before our
// handler runs. No additional argument-strictness wrapper is needed.

// readOnlyAnnotations is the ToolAnnotations applied to every tool. aha's
// MCP surface is read-only by construction: there is no write tool to be
// registered, and the SDK consults these hints when prompting the user.
var readOnlyAnnotations = &mcp.ToolAnnotations{
	ReadOnlyHint:    true,
	IdempotentHint:  true,
	DestructiveHint: boolPtr(false),
	OpenWorldHint:   boolPtr(false),
}

func boolPtr(v bool) *bool { return &v }

func clampNonNegative(n, max int) int {
	if n < 0 {
		return 0
	}
	if n > max {
		return max
	}
	return n
}

// NewServer builds an MCP Server with every read tool registered. The
// returned *mcp.Server is ready to be paired with any of the SDK's
// transports (Stdio, Streamable HTTP, InMemory). Tests use
// mcp.NewInMemoryTransports + this constructor to build a complete
// in-process MCP loop.
func NewServer(backend Backend) *mcp.Server {
	server := mcp.NewServer(serverInfo, nil)
	registerTools(server, backend)
	return server
}

func registerTools(server *mcp.Server, b Backend) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "search",
		Description: ToolDescriptions["search"],
		Annotations: readOnlyAnnotations,
	}, func(_ context.Context, _ *mcp.CallToolRequest, in SearchInput) (*mcp.CallToolResult, any, error) {
		out, err := doSearch(b, in)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(out), nil, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "read",
		Description: ToolDescriptions["read"],
		Annotations: readOnlyAnnotations,
	}, func(_ context.Context, _ *mcp.CallToolRequest, in ReadInput) (*mcp.CallToolResult, any, error) {
		out, err := doRead(b, in)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(out), nil, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "status",
		Description: ToolDescriptions["status"],
		Annotations: readOnlyAnnotations,
	}, func(_ context.Context, _ *mcp.CallToolRequest, _ EmptyInput) (*mcp.CallToolResult, map[string]any, error) {
		out, err := doStatus(b)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return nil, out, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "verify",
		Description: ToolDescriptions["verify"],
		Annotations: readOnlyAnnotations,
	}, func(_ context.Context, _ *mcp.CallToolRequest, _ EmptyInput) (*mcp.CallToolResult, corpus.VerifyReport, error) {
		out, err := doVerify(b)
		if err != nil {
			return errorResult(err), out, nil
		}
		return nil, out, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "conflicts",
		Description: ToolDescriptions["conflicts"],
		Annotations: readOnlyAnnotations,
	}, func(_ context.Context, _ *mcp.CallToolRequest, _ EmptyInput) (*mcp.CallToolResult, any, error) {
		out, err := doConflicts(b)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(out), nil, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "corpus_size",
		Description: ToolDescriptions["corpus_size"],
		Annotations: readOnlyAnnotations,
	}, func(_ context.Context, _ *mcp.CallToolRequest, _ EmptyInput) (*mcp.CallToolResult, corpus.SizeReport, error) {
		out, err := doCorpusSize(b)
		if err != nil {
			return errorResult(err), out, nil
		}
		return nil, out, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "doctor",
		Description: ToolDescriptions["doctor"],
		Annotations: readOnlyAnnotations,
	}, func(_ context.Context, _ *mcp.CallToolRequest, _ EmptyInput) (*mcp.CallToolResult, map[string]any, error) {
		out, err := doDoctor(b)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return nil, out, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "incidents",
		Description: ToolDescriptions["incidents"],
		Annotations: readOnlyAnnotations,
	}, func(_ context.Context, _ *mcp.CallToolRequest, in IncidentsInput) (*mcp.CallToolResult, any, error) {
		out, err := doIncidents(b, in)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(out), nil, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "incident_trajectory",
		Description: ToolDescriptions["incident_trajectory"],
		Annotations: readOnlyAnnotations,
	}, func(_ context.Context, _ *mcp.CallToolRequest, in IncidentTrajectoryInput) (*mcp.CallToolResult, any, error) {
		out, err := doIncidentTrajectory(b, in)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(out), nil, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "overview",
		Description: ToolDescriptions["overview"],
		Annotations: readOnlyAnnotations,
	}, func(_ context.Context, _ *mcp.CallToolRequest, _ EmptyInput) (*mcp.CallToolResult, corpus.Overview, error) {
		out, err := doOverview(b)
		if err != nil {
			return errorResult(err), corpus.Overview{}, nil
		}
		return nil, out, nil
	})
}

// textResult builds a CallToolResult carrying v as a JSON text content
// block. Used for list-typed tool outputs, where the SDK's auto-fill path
// is unavailable: a Go slice's auto-derived schema has type=array, which
// the SDK refuses for output schemas (Python SDK Pydantic requires
// Dict[str, Any] for structuredContent). The typed payload travels in
// content[].text and the TS client surface JSON-parses it transparently.
//
// Object-typed tools don't need this helper: the SDK fills both Content
// and StructuredContent from the typed Out return value when the handler
// returns (nil, out, nil).
func textResult(v any) *mcp.CallToolResult {
	text, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return errorResult(err)
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(text)}},
	}
}

func errorResult(err error) *mcp.CallToolResult {
	view := usererror.Normalize(err, "mcp")
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: view.Message() + "\nnext: " + view.Next().Text()}},
	}
}

// ---------- CallTool: HTTP-server-facing dispatch (no SDK roundtrip) ----------

// CallTool is the dispatch entrypoint used by the HTTP dashboard. It
// parses the JSON arguments into the right Input struct (rejecting unknown
// keys to mirror the SDK's strict input validation), runs the same
// business-logic function the MCP handler runs, and returns the typed
// result. The HTTP server doesn't speak MCP itself; this function is what
// keeps `aha serve` independent of the MCP transport stack.
func CallTool(b Backend, name string, raw json.RawMessage) (any, error) {
	switch name {
	case "search":
		in, err := decodeInput[SearchInput](raw, name)
		if err != nil {
			return nil, err
		}
		return doSearch(b, in)
	case "read":
		in, err := decodeInput[ReadInput](raw, name)
		if err != nil {
			return nil, err
		}
		return doRead(b, in)
	case "status":
		if err := rejectArgsIfPresent(raw, name); err != nil {
			return nil, err
		}
		return doStatus(b)
	case "verify":
		if err := rejectArgsIfPresent(raw, name); err != nil {
			return nil, err
		}
		return doVerify(b)
	case "conflicts":
		if err := rejectArgsIfPresent(raw, name); err != nil {
			return nil, err
		}
		return doConflicts(b)
	case "corpus_size":
		if err := rejectArgsIfPresent(raw, name); err != nil {
			return nil, err
		}
		return doCorpusSize(b)
	case "doctor":
		if err := rejectArgsIfPresent(raw, name); err != nil {
			return nil, err
		}
		return doDoctor(b)
	case "incidents":
		in, err := decodeInput[IncidentsInput](raw, name)
		if err != nil {
			return nil, err
		}
		return doIncidents(b, in)
	case "incident_trajectory":
		in, err := decodeInput[IncidentTrajectoryInput](raw, name)
		if err != nil {
			return nil, err
		}
		return doIncidentTrajectory(b, in)
	case "overview":
		if err := rejectArgsIfPresent(raw, name); err != nil {
			return nil, err
		}
		return doOverview(b)
	}
	return nil, fmt.Errorf("unknown tool: %s", name)
}

// decodeInput strict-parses raw into a typed Input struct. Unknown fields
// are rejected so HTTP callers get the same `unexpected argument` error
// the MCP handler would produce.
func decodeInput[T any](raw json.RawMessage, toolName string) (T, error) {
	var zero T
	if len(raw) == 0 || string(raw) == "null" {
		return zero, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var out T
	if err := dec.Decode(&out); err != nil {
		var typeErr *json.UnmarshalTypeError
		if errors.As(err, &typeErr) {
			return zero, fmt.Errorf("invalid arguments for %s: %w", toolName, err)
		}
		return zero, fmt.Errorf("unexpected argument for %s: %w", toolName, err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return zero, fmt.Errorf("unexpected trailing JSON for %s", toolName)
		}
		return zero, fmt.Errorf("invalid trailing JSON for %s: %w", toolName, err)
	}
	return out, nil
}

// rejectArgsIfPresent is the no-input equivalent of decodeInput: any
// non-empty object causes a strict-parse failure into EmptyInput, which
// has no fields and therefore rejects every key the caller sent.
func rejectArgsIfPresent(raw json.RawMessage, toolName string) error {
	_, err := decodeInput[EmptyInput](raw, toolName)
	return err
}

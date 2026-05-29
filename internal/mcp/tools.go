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
	"sort"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/adewale/aha/internal/adapters"
	"github.com/adewale/aha/internal/config"
	"github.com/adewale/aha/internal/corpus"
	"github.com/adewale/aha/internal/model"
	"github.com/adewale/aha/internal/search"
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

// ServerInfo is the implementation block we hand to NewServer. Exported so
// tests can use the same value when constructing in-process server peers
// via mcp.NewInMemoryTransports.
var ServerInfo = &mcp.Implementation{Name: "aha", Version: model.Version}

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
	Path      string `json:"path,omitempty" jsonschema:"Substring filter against session/artifact paths"`
	PathToken string `json:"path_token,omitempty" jsonschema:"Indexed path-segment filter"`
	Project   string `json:"project,omitempty" jsonschema:"Indexed project filter"`
	Limit     int    `json:"limit,omitempty" jsonschema:"Cap on returned hits (default 20, max 200)"`
}

// ReadInput accepts either a canonical ref (preferred) or a session+entry
// pair. Both modes resolve via the same corpus.Read* functions the CLI uses.
type ReadInput struct {
	Ref     string `json:"ref,omitempty" jsonschema:"Canonical ref text (msg:v1:..., session:v1:..., artifact:v1:...)"`
	Session string `json:"session,omitempty" jsonschema:"Session key (used when ref is empty)"`
	Entry   string `json:"entry,omitempty" jsonschema:"Entry id within the session (optional)"`
	Before  int    `json:"before,omitempty" jsonschema:"Lines of context before the target entry (default 3)"`
	After   int    `json:"after,omitempty" jsonschema:"Lines of context after the target entry (default 5)"`
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

func doRead(b Backend, in ReadInput) ([]corpus.ReadEntry, error) {
	before := in.Before
	if before == 0 {
		before = 3
	}
	after := in.After
	if after == 0 {
		after = 5
	}
	var (
		entries []corpus.ReadEntry
		err     error
	)
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

// rejectExtras strict-decodes the raw arguments into a fresh T using
// DisallowUnknownFields and returns an isError CallToolResult if the
// strict decode fails. Returns nil when the arguments are well-formed
// (or absent). Wraps each tool handler to give us "additionalProperties:
// false" semantics without having to hand-construct the input schema.
//
// AddTool already decoded the args into the typed In struct (silently
// dropping unknown fields). We re-decode strictly here to surface the
// rejection the JSON Schema doesn't itself enforce.
func rejectExtras[T any](req *mcp.CallToolRequest) *mcp.CallToolResult {
	raw := req.Params.Arguments
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var t T
	if err := dec.Decode(&t); err != nil {
		return errorResult(fmt.Errorf("unexpected argument: %w", err))
	}
	return nil
}

// ---------- SDK registration ----------

// readOnlyAnnotations is the ToolAnnotations applied to every tool. aha's
// MCP surface is read-only by construction: there is no write tool to be
// registered, and the SDK consults these hints when prompting the user.
var readOnlyAnnotations = &mcp.ToolAnnotations{
	ReadOnlyHint:   true,
	IdempotentHint: true,
	DestructiveHint: boolPtr(false),
	OpenWorldHint:   boolPtr(false),
}

func boolPtr(v bool) *bool { return &v }

// NewServer builds an MCP Server with every read tool registered. The
// returned *mcp.Server is ready to be paired with any of the SDK's
// transports (Stdio, Streamable HTTP, InMemory). Tests use
// mcp.NewInMemoryTransports + this constructor to build a complete
// in-process MCP loop.
func NewServer(backend Backend) *mcp.Server {
	server := mcp.NewServer(ServerInfo, nil)
	registerTools(server, backend)
	return server
}

func registerTools(server *mcp.Server, b Backend) {
	// Tools whose Out type is a Go slice (search/read/conflicts) would
	// produce an "array"-typed JSON schema, which the SDK refuses for
	// output schemas (spec wants object). Those are registered with
	// Out=any so the SDK skips output-schema derivation; the typed
	// payload still travels in content[].text and is JSON-parseable
	// client-side (which is exactly what the bidirectional conformance
	// tests verify).
	mcp.AddTool(server, &mcp.Tool{
		Name:        "search",
		Description: "Search the corpus over messages and artifacts. Returns ref-bearing results suitable for chaining into read.",
		Annotations: readOnlyAnnotations,
	}, func(_ context.Context, req *mcp.CallToolRequest, in SearchInput) (*mcp.CallToolResult, any, error) {
		if r := rejectExtras[SearchInput](req); r != nil {
			return r, nil, nil
		}
		out, err := doSearch(b, in)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return textOnlyResult(out), nil, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "read",
		Description: "Retrieve full surrounding context for a search hit. Accepts either a canonical ref text or session+entry coordinates.",
		Annotations: readOnlyAnnotations,
	}, func(_ context.Context, req *mcp.CallToolRequest, in ReadInput) (*mcp.CallToolResult, any, error) {
		if r := rejectExtras[ReadInput](req); r != nil {
			return r, nil, nil
		}
		out, err := doRead(b, in)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return textOnlyResult(out), nil, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "status",
		Description: "Return corpus health summary: counts and disk usage.",
		Annotations: readOnlyAnnotations,
	}, func(_ context.Context, req *mcp.CallToolRequest, _ EmptyInput) (*mcp.CallToolResult, any, error) {
		if r := rejectExtras[EmptyInput](req); r != nil {
			return r, nil, nil
		}
		out, err := doStatus(b)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return objectResult(out), nil, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "verify",
		Description: "Run read-only corpus invariant checks (no repair).",
		Annotations: readOnlyAnnotations,
	}, func(_ context.Context, req *mcp.CallToolRequest, _ EmptyInput) (*mcp.CallToolResult, any, error) {
		if r := rejectExtras[EmptyInput](req); r != nil {
			return r, nil, nil
		}
		out, err := doVerify(b)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return objectResult(out), nil, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "conflicts",
		Description: "List quarantined merge conflicts.",
		Annotations: readOnlyAnnotations,
	}, func(_ context.Context, req *mcp.CallToolRequest, _ EmptyInput) (*mcp.CallToolResult, any, error) {
		if r := rejectExtras[EmptyInput](req); r != nil {
			return r, nil, nil
		}
		out, err := doConflicts(b)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return textOnlyResult(out), nil, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "corpus_size",
		Description: "Return corpus on-disk size breakdown.",
		Annotations: readOnlyAnnotations,
	}, func(_ context.Context, req *mcp.CallToolRequest, _ EmptyInput) (*mcp.CallToolResult, any, error) {
		if r := rejectExtras[EmptyInput](req); r != nil {
			return r, nil, nil
		}
		out, err := doCorpusSize(b)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return objectResult(out), nil, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "doctor",
		Description: "Return local environment, config, source, and corpus diagnostics. Depot probing is omitted to keep this tool local-only.",
		Annotations: readOnlyAnnotations,
	}, func(_ context.Context, req *mcp.CallToolRequest, _ EmptyInput) (*mcp.CallToolResult, any, error) {
		if r := rejectExtras[EmptyInput](req); r != nil {
			return r, nil, nil
		}
		out, err := doDoctor(b)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return objectResult(out), nil, nil
	})
}

// textOnlyResult builds a CallToolResult that carries the payload only as
// JSON in content[].text. Used for list-shaped outputs, where structured
// content (which the Python SDK requires to be a JSON object) doesn't fit.
// The SDK fills structuredContent itself when the Out generic type is
// non-nil; we use the zero-output overload by returning nil for Out via
// the surrounding handler signature... except we can't, so we set
// structuredContent: nil in CallToolResult to override.
func textOnlyResult(v any) *mcp.CallToolResult {
	text, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return errorResult(err)
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(text)}},
		// Explicitly nil-overriding StructuredContent so the SDK doesn't
		// emit a list under a "structuredContent" key that the official
		// Python SDK Pydantic Dict type rejects.
		StructuredContent: nil,
	}
}

// objectResult builds a CallToolResult for object-typed outputs. Both
// content[].text and StructuredContent carry the value.
func objectResult(v any) *mcp.CallToolResult {
	text, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return errorResult(err)
	}
	return &mcp.CallToolResult{
		Content:           []mcp.Content{&mcp.TextContent{Text: string(text)}},
		StructuredContent: v,
	}
}

func errorResult(err error) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
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
		// json's "unknown field" error message matches what tests look for;
		// also tolerate the case where the body parses but errs midway.
		if errors.Is(err, &json.UnmarshalTypeError{}) {
			return zero, fmt.Errorf("invalid arguments for %s: %w", toolName, err)
		}
		return zero, fmt.Errorf("unexpected argument for %s: %w", toolName, err)
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

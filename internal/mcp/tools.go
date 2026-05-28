package mcp

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/adewale/aha/internal/adapters"
	"github.com/adewale/aha/internal/config"
	"github.com/adewale/aha/internal/corpus"
	"github.com/adewale/aha/internal/model"
	"github.com/adewale/aha/internal/search"
)

// toolSpec describes one MCP tool: name, description, JSON-Schema for
// arguments, and tool annotations. Kept minimal — the underlying functions
// own validation beyond the structural checks here.
//
// Annotations follow the 2025-06-18 spec
// (https://modelcontextprotocol.io/specification/2025-06-18/server/tools).
// They are *hints* to the client and must be treated as untrusted unless the
// server itself is trusted; aha's server is fully read-only so we set
// readOnlyHint: true on every tool.
type toolSpec struct {
	Name        string             `json:"name"`
	Description string             `json:"description"`
	InputSchema map[string]any     `json:"inputSchema"`
	Annotations map[string]any     `json:"annotations,omitempty"`
}

func tool(name, description string, properties map[string]any, required ...string) toolSpec {
	if properties == nil {
		properties = map[string]any{}
	}
	if required == nil {
		required = []string{}
	}
	return toolSpec{
		Name:        name,
		Description: description,
		InputSchema: map[string]any{
			"type":                 "object",
			"properties":           properties,
			"required":             required,
			"additionalProperties": false,
		},
		Annotations: map[string]any{
			"readOnlyHint":    true,
			"destructiveHint": false,
			"idempotentHint":  true,
			"openWorldHint":   false,
		},
	}
}

func str() map[string]any { return map[string]any{"type": "string"} }
func num() map[string]any { return map[string]any{"type": "number"} }

// readOnlyTools is the phase-1 tool list (see docs/mcp-spec.md). The order
// here is the order returned by tools/list.
var readOnlyTools = []toolSpec{
	tool(
		"search",
		"Search the corpus over messages and artifacts. Returns ref-bearing results suitable for chaining into read.",
		map[string]any{
			"query":      str(),
			"source":     str(),
			"machine":    str(),
			"role":       str(),
			"after":      str(),
			"before":     str(),
			"path":       str(),
			"path_token": str(),
			"project":    str(),
			"limit":      num(),
		},
		"query",
	),
	tool(
		"read",
		"Retrieve full surrounding context for a search hit. Accepts either a canonical ref text (msg:v1:..., session:v1:..., artifact:v1:...) or session+entry coordinates.",
		map[string]any{
			"ref":     str(),
			"session": str(),
			"entry":   str(),
			"before":  num(),
			"after":   num(),
		},
	),
	tool("status", "Return corpus health summary: counts and disk usage. Same shape as `aha status --json`.", nil),
	tool("verify", "Run read-only corpus invariant checks (no repair). Same shape as `aha verify --json`.", nil),
	tool("conflicts", "List quarantined merge conflicts. Same shape as `aha conflicts --json`.", nil),
	tool("corpus_size", "Return corpus on-disk size breakdown. Same shape as `aha corpus size --json`.", nil),
	tool("doctor", "Return local environment, config, source, and corpus diagnostics. Depot probing is omitted to keep this tool local-only.", nil),
}

// Backend is the read surface the MCP server needs from the rest of aha.
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

// CallTool dispatches a tools/call to the matching read function, with strict
// argument validation. The returned value is what the JSON-RPC response puts
// into the `content[0].text` field. Exported so the HTTP server in
// internal/server can reuse the same dispatch.
func CallTool(b Backend, name string, raw json.RawMessage) (any, error) {
	spec := findTool(name)
	if spec == nil {
		return nil, fmt.Errorf("unknown tool: %s", name)
	}
	args, err := decodeArgs(spec, raw)
	if err != nil {
		return nil, err
	}
	switch name {
	case "search":
		return callSearch(b, args)
	case "read":
		return callRead(b, args)
	case "status":
		return callStatus(b)
	case "verify":
		return callVerify(b)
	case "conflicts":
		return callConflicts(b)
	case "corpus_size":
		return callCorpusSize(b)
	case "doctor":
		return callDoctor(b)
	}
	return nil, fmt.Errorf("tool %q has no handler", name)
}

func findTool(name string) *toolSpec {
	for i := range readOnlyTools {
		if readOnlyTools[i].Name == name {
			return &readOnlyTools[i]
		}
	}
	return nil
}

// decodeArgs enforces the allow-list (rejecting unknown keys) and required
// fields declared by the toolSpec. The map it returns has any extra
// type-coercion (e.g. JSON numbers → ints) already applied.
func decodeArgs(spec *toolSpec, raw json.RawMessage) (map[string]any, error) {
	out := map[string]any{}
	if len(raw) == 0 || string(raw) == "null" {
		if len(requiredFromSchema(spec)) > 0 {
			return nil, fmt.Errorf("missing arguments for %s", spec.Name)
		}
		return out, nil
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("invalid arguments for %s: %w", spec.Name, err)
	}
	allowed := map[string]bool{}
	for k := range propertiesFromSchema(spec) {
		allowed[k] = true
	}
	for k := range out {
		if !allowed[k] {
			return nil, fmt.Errorf("unexpected argument for %s: %s", spec.Name, k)
		}
	}
	for _, req := range requiredFromSchema(spec) {
		v, ok := out[req]
		if !ok || isEmpty(v) {
			return nil, fmt.Errorf("missing required argument for %s: %s", spec.Name, req)
		}
	}
	return out, nil
}

func propertiesFromSchema(spec *toolSpec) map[string]any {
	if props, ok := spec.InputSchema["properties"].(map[string]any); ok {
		return props
	}
	return nil
}

func requiredFromSchema(spec *toolSpec) []string {
	req, ok := spec.InputSchema["required"].([]string)
	if !ok {
		return nil
	}
	return req
}

func isEmpty(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return x == ""
	}
	return false
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func asInt(v any) int {
	switch x := v.(type) {
	case float64:
		return int(x)
	case int:
		return x
	}
	return 0
}

// isJSONObject reports whether v marshals to a JSON object. Map types,
// structs, and *struct pointers qualify; slices, arrays, scalars, and nil
// do not. Used to decide whether structuredContent is safe to emit (the
// official Python SDK rejects non-object structuredContent).
func isJSONObject(v any) bool {
	if v == nil {
		return false
	}
	t := reflect.TypeOf(v)
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Map, reflect.Struct:
		return true
	}
	return false
}

// callSearch invokes search.Query with the documented filter set. Returns
// an empty slice (never nil) when no rows match, so JSON marshalling
// produces [] rather than null. The CLI happily renders nil as "no
// results"; MCP clients expect a list shape.
func callSearch(b Backend, args map[string]any) (any, error) {
	q := asString(args["query"])
	if q == "" {
		return nil, fmt.Errorf("missing required argument for search: query")
	}
	limit := asInt(args["limit"])
	if limit <= 0 {
		limit = 20
	}
	if limit > search.MaxLimit {
		limit = search.MaxLimit
	}
	results, err := search.Query(b.DB(), q, search.Filters{
		Source:    asString(args["source"]),
		Machine:   asString(args["machine"]),
		Role:      asString(args["role"]),
		After:     asString(args["after"]),
		Before:    asString(args["before"]),
		Path:      asString(args["path"]),
		PathToken: asString(args["path_token"]),
		Project:   asString(args["project"]),
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

// callRead resolves either a canonical ref text or a session+entry pair, then
// pulls surrounding context via the same corpus functions the CLI uses.
// Empty result → [] (never nil) so JSON marshalling produces a list.
func callRead(b Backend, args map[string]any) (any, error) {
	before := asInt(args["before"])
	if _, ok := args["before"]; !ok {
		before = 3
	}
	after := asInt(args["after"])
	if _, ok := args["after"]; !ok {
		after = 5
	}
	var (
		entries []corpus.ReadEntry
		err     error
	)
	if refText := asString(args["ref"]); refText != "" {
		ref, perr := model.ParseRef(refText)
		if perr != nil {
			return nil, fmt.Errorf("invalid ref: %w", perr)
		}
		entries, err = corpus.ReadCanonical(b.DB(), ref, before, after)
	} else {
		session := asString(args["session"])
		if session == "" {
			return nil, fmt.Errorf("read requires either ref or session")
		}
		entries, err = corpus.ReadContext(b.DB(), session, asString(args["entry"]), before, after)
	}
	if err != nil {
		return nil, err
	}
	if entries == nil {
		entries = []corpus.ReadEntry{}
	}
	return entries, nil
}

func callStatus(b Backend) (any, error) {
	return corpus.Status(b.DB(), b.Root())
}

func callVerify(b Backend) (any, error) {
	return corpus.Verify(b.Store())
}

func callConflicts(b Backend) (any, error) {
	rows, err := corpus.Conflicts(b.DB())
	if err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []corpus.Conflict{}
	}
	return rows, nil
}

func callCorpusSize(b Backend) (any, error) {
	return corpus.Size(b.Store())
}

// callDoctor mirrors the CLI doctor JSON payload but skips the depot probe
// to keep the MCP surface local-only and free of network calls in phase 1.
func callDoctor(b Backend) (any, error) {
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

// tools/list payload uses the spec's exported field names.
func toolsList() any {
	return map[string]any{"tools": readOnlyTools}
}

// HandleMessage routes one decoded JSON-RPC message and returns a response.
// Returns (nil, false) for notifications (no reply on the wire).
func HandleMessage(b Backend, msg Message) (*response, bool) {
	if msg.JSONRPC != "" && msg.JSONRPC != jsonRPCVersion {
		return newError(msg.ID, codeInvalidRequest, "unsupported jsonrpc version"), true
	}
	switch msg.Method {
	case "initialize":
		return newResult(msg.ID, map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": serverName, "version": model.Version},
		}), true
	case "notifications/initialized":
		return nil, false
	case "ping":
		return newResult(msg.ID, map[string]any{}), true
	case "tools/list":
		return newResult(msg.ID, toolsList()), true
	case "resources/list":
		return newResult(msg.ID, map[string]any{"resources": []any{}}), true
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if len(msg.Params) > 0 {
			if err := json.Unmarshal(msg.Params, &p); err != nil {
				return newError(msg.ID, codeInvalidRequest, "invalid params: "+err.Error()), true
			}
		}
		if strings.TrimSpace(p.Name) == "" {
			return newError(msg.ID, codeInvalidRequest, "tools/call requires name"), true
		}
		out, err := CallTool(b, p.Name, p.Arguments)
		if err != nil {
			return newError(msg.ID, codeToolError, err.Error()), true
		}
		text, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			return newError(msg.ID, codeToolError, err.Error()), true
		}
		// Per the 2025-06-18 spec: "For backwards compatibility, a tool that
		// returns structured content SHOULD also return the serialized JSON
		// in a TextContent block." We always emit the text content; we emit
		// structuredContent *only* when the payload is a JSON object, which
		// is what the official SDK accepts there (its Pydantic type is
		// Dict[str, Any]). Arrays and scalars travel in the text field
		// alone; the typed client surface JSON-parses that fallback.
		result := map[string]any{
			"content": []any{
				map[string]any{"type": "text", "text": string(text)},
			},
		}
		if isJSONObject(out) {
			result["structuredContent"] = out
		}
		return newResult(msg.ID, result), true
	}
	return newError(msg.ID, codeMethodNotFound, "unknown method: "+msg.Method), true
}

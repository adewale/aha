package adapters

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/adewale/aha/internal/hash"
	"github.com/adewale/aha/internal/model"
)

type CodexCLI struct{}

func (CodexCLI) Name() string    { return "codex" }
func (CodexCLI) Version() string { return "v1" }
func (CodexCLI) DefaultRoots() []model.DefaultRoot {
	return []model.DefaultRoot{{OS: "all", Path: "~/.codex/sessions"}}
}
func (CodexCLI) Capabilities() model.AdapterCapabilities {
	return model.AdapterCapabilities{HasThreads: true, HasSubagents: true, HasImages: true, HasToolCalls: true, HasStableEntryIDs: false, CanLinkSubagents: false}
}
func (CodexCLI) Discover(ctx context.Context, config model.SourceConfig) ([]model.SessionFile, error) {
	var out []model.SessionFile
	err := filepath.WalkDir(config.Root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
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
		base := filepath.Base(p)
		if !strings.HasPrefix(base, "rollout-") && base != "history.jsonl" {
			return nil
		}
		rel, err := filepath.Rel(config.Root, p)
		if err != nil {
			rel = filepath.Base(p)
		}
		out = append(out, model.SessionFile{Source: "codex", Root: config.Root, Path: p, RelativePath: filepath.ToSlash(rel), SessionID: SessionIDFromPath(p)})
		return nil
	})
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}
func (CodexCLI) DiscoverArtifacts(ctx context.Context, session model.SessionFile) ([]model.ArtifactFile, error) {
	return nil, nil
}
func (CodexCLI) ParseSession(ctx context.Context, file model.SessionFile, r io.Reader) (*model.ParsedSession, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return parseCodex(ctx, file, r)
}

// parseCodex handles both Codex rollout formats. The current Codex CLI wraps
// every line in a {timestamp, type, payload} envelope (type "session_meta",
// "response_item", "event_msg", ...), with the conversation living inside
// response_item payloads — so the generic JSONL parser, which looks for
// top-level role / message.content, extracts nothing. Older rollouts use the
// flat {type:"session"|"message", role, message:{content}} shape the generic
// parser already understands, so those are delegated unchanged.
func parseCodex(ctx context.Context, file model.SessionFile, r io.Reader) (*model.ParsedSession, error) {
	modern, replay, err := probeCodexFormat(r)
	if err != nil {
		return nil, err
	}
	if modern {
		return parseCodexModern(ctx, file, replay)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return parseGenericJSONL("codex", file, replay)
}

// looksModernCodex reports whether the rollout uses the enveloped format,
// detected by a line carrying a "payload" object under one of the known
// rollout line types.
func probeCodexFormat(r io.Reader) (bool, io.Reader, error) {
	br := bufio.NewReader(r)
	var probe bytes.Buffer
	buf := make([]byte, 4096)
	for countNonEmptyLines(probe.Bytes()) < 50 && probe.Len() < 1<<20 {
		want := len(buf)
		if remain := (1 << 20) - probe.Len(); remain < want {
			want = remain
		}
		if want <= 0 {
			break
		}
		n, err := br.Read(buf[:want])
		if n > 0 {
			_, _ = probe.Write(buf[:n])
			if looksModernCodex(probe.Bytes()) {
				return true, io.MultiReader(bytes.NewReader(probe.Bytes()), br), nil
			}
		}
		if err != nil {
			if err == io.EOF {
				break
			}
			return false, nil, err
		}
	}
	return looksModernCodex(probe.Bytes()), io.MultiReader(bytes.NewReader(probe.Bytes()), br), nil
}

func countNonEmptyLines(data []byte) int {
	reader := bufio.NewReader(bytes.NewReader(data))
	count := 0
	for {
		line, err := reader.ReadString('\n')
		if strings.TrimSpace(line) != "" {
			count++
		}
		if err != nil {
			break
		}
	}
	return count
}

func looksModernCodex(data []byte) bool {
	reader := bufio.NewReader(bytes.NewReader(data))
	for scanned := 0; scanned < 50; {
		line, err := reader.ReadString('\n')
		if raw := strings.TrimSpace(line); raw != "" {
			scanned++
			var m map[string]any
			if json.Unmarshal([]byte(raw), &m) == nil {
				if _, ok := m["payload"].(map[string]any); ok {
					switch stringField(m, "type") {
					case "session_meta", "response_item", "event_msg", "turn_context", "compacted":
						return true
					}
				}
			}
		}
		if err != nil {
			break
		}
	}
	return false
}

func parseCodexModern(ctx context.Context, file model.SessionFile, r io.Reader) (*model.ParsedSession, error) {
	ps := &model.ParsedSession{Source: "codex", SourceSessionID: file.SessionID, CWD: file.CWD, StartedAt: file.StartedAt, IsSubagent: file.IsSubagent, Metadata: map[string]any{"relative_path": file.RelativePath}}
	reader := bufio.NewReader(r)
	lineNo := 0
	curModel, curProvider := "", ""
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			ps.Diagnostics = append(ps.Diagnostics, fmt.Sprintf("line %d: %v", lineNo+1, err))
			break
		}
		if err == io.EOF && line == "" {
			break
		}
		lineNo++
		raw := strings.TrimRight(line, "\r\n")
		if strings.TrimSpace(raw) == "" {
			if err == io.EOF {
				break
			}
			continue
		}
		var m map[string]any
		if jErr := json.Unmarshal([]byte(raw), &m); jErr != nil {
			ps.Diagnostics = append(ps.Diagnostics, fmt.Sprintf("line %d: %v", lineNo, jErr))
			if err == io.EOF {
				break
			}
			continue
		}
		ltype := stringField(m, "type")
		ts := stringField(m, "timestamp")
		payload, _ := m["payload"].(map[string]any)
		entryID := firstNonEmpty(stringField(m, "id"), stringField(payload, "id"))
		if entryID == "" {
			entryID = fmt.Sprintf("line-%06d-%s", lineNo, hash.SHA256Bytes([]byte(raw))[:12])
		}
		pe := model.ParsedEntry{EntryID: entryID, LineNo: lineNo, EntryType: ltype, Timestamp: ts, RawJSON: raw, ParticipatesInContext: true, Metadata: map[string]any{}}
		switch ltype {
		case "session_meta":
			if id := stringField(payload, "id"); id != "" {
				ps.SourceSessionID = id
			}
			if ps.CWD == "" {
				ps.CWD = stringField(payload, "cwd")
			}
			if ps.StartedAt == "" {
				ps.StartedAt = firstNonEmpty(ts, stringField(payload, "timestamp"))
			}
			curModel = firstNonEmpty(stringField(payload, "model"), curModel)
			curProvider = firstNonEmpty(stringField(payload, "model_provider"), stringField(payload, "provider"), curProvider)
			ps.Metadata["session_meta"] = raw
		case "turn_context":
			curModel = firstNonEmpty(stringField(payload, "model"), curModel)
			curProvider = firstNonEmpty(stringField(payload, "model_provider"), stringField(payload, "provider"), curProvider)
		case "response_item":
			fillCodexResponseItem(&pe, payload, curModel, curProvider)
		case "event_msg", "compacted":
			if stringField(payload, "type") != "" {
				fillCodexResponseItem(&pe, payload, curModel, curProvider)
			} else {
				pe.EntryType = ltype
			}
		}
		ps.Entries = append(ps.Entries, pe)
		if err == io.EOF {
			break
		}
	}
	return ps, nil
}

// fillCodexResponseItem populates a ParsedEntry from a response_item payload:
// conversational messages (role + content text), tool calls, and tool output.
func fillCodexResponseItem(pe *model.ParsedEntry, payload map[string]any, curModel, curProvider string) {
	ptype := stringField(payload, "type")
	pe.EntryType = firstNonEmpty(ptype, "response_item")
	switch ptype {
	case "user_message":
		pe.Role = "user"
		pe.Text = stringField(payload, "message")
	case "agent_message":
		pe.Role = "assistant"
		pe.Text = stringField(payload, "message")
		pe.Model = curModel
		pe.Provider = curProvider
	case "message":
		pe.Role = stringField(payload, "role")
		ec := extractContent(map[string]any{"content": payload["content"]})
		pe.Text, pe.ToolName, pe.Command, pe.FilesJSON, pe.Assets = ec.text, ec.tool, ec.command, ec.files, ec.assets
		pe.ToolCalls, pe.ToolResults = ec.toolCalls, ec.toolResults
		if pe.Role == "assistant" {
			pe.Model = curModel
			pe.Provider = curProvider
		}
	case "function_call", "local_shell_call", "custom_tool_call":
		pe.Role = "toolCall"
		pe.ToolName = firstNonEmpty(stringField(payload, "name"), ptype)
		pe.FilesJSON = codexArgumentsJSON(payload["arguments"])
		pe.Command = codexCommandFromArguments(payload["arguments"])
		pe.ToolCalls = []model.ParsedToolCall{{ID: firstNonEmpty(stringField(payload, "call_id"), stringField(payload, "id")), ToolName: pe.ToolName, Command: pe.Command, FilesJSON: pe.FilesJSON}}
	case "web_search_call":
		pe.Role = "toolCall"
		pe.ToolName = "web_search"
		if action, ok := payload["action"].(map[string]any); ok {
			pe.Command = stringField(action, "query")
		}
		pe.ToolCalls = []model.ParsedToolCall{{ID: firstNonEmpty(stringField(payload, "call_id"), stringField(payload, "id")), ToolName: pe.ToolName, Command: pe.Command}}
	case "function_call_output", "custom_tool_call_output", "local_shell_call_output":
		pe.Role = "toolResult"
		pe.Text = codexToolOutputText(payload["output"])
		exitCode, hasExitCode := codexExitCode(pe.Text)
		isError := boolField(payload, "is_error") || (hasExitCode && exitCode != 0)
		pe.ToolResults = []model.ParsedToolResult{{ForID: firstNonEmpty(stringField(payload, "call_id"), stringField(payload, "id")), IsError: isError, OutcomeText: truncateUTF8(pe.Text, maxOutcomeTextBytes), ExitCode: exitCode, ExitCodeValid: hasExitCode}}
	case "reasoning":
		pe.Role = "reasoning"
		pe.Text = extractContent(map[string]any{"content": firstNonNil(payload["summary"], payload["content"])}).text
	case "token_count":
		applyCodexTokenCount(pe, payload)
	}
	if curProvider != "" && pe.Provider == "" {
		pe.Provider = curProvider
	}
}

func codexArgumentsJSON(args any) string {
	switch x := args.(type) {
	case string:
		return x
	case map[string]any, []any:
		if b, err := json.Marshal(x); err == nil {
			return string(b)
		}
	}
	return ""
}

func codexCommandFromArguments(args any) string {
	var m map[string]any
	switch x := args.(type) {
	case string:
		if x == "" || json.Unmarshal([]byte(x), &m) != nil {
			return ""
		}
	case map[string]any:
		m = x
	default:
		return ""
	}
	return codexCommandValue(firstNonNil(m["command"], m["cmd"]))
}

func codexCommandValue(v any) string {
	switch c := v.(type) {
	case string:
		return c
	case []any:
		parts := make([]string, 0, len(c))
		for _, p := range c {
			if s, ok := p.(string); ok {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, " ")
	}
	return ""
}

var codexExitCodeRE = regexp.MustCompile(`(?i)\b(?:process|command) exited with code\s+(-?\d+)\b`)

func codexExitCode(text string) (int64, bool) {
	m := codexExitCodeRE.FindStringSubmatch(text)
	if m == nil {
		return 0, false
	}
	n, err := strconv.ParseInt(m[1], 10, 64)
	return n, err == nil
}

func codexToolOutputText(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []any:
		return extractContent(map[string]any{"content": x}).text
	case map[string]any:
		if s := stringField(x, "content"); s != "" {
			return s
		}
		return stringField(x, "output")
	}
	return ""
}

func applyCodexTokenCount(e *model.ParsedEntry, payload map[string]any) {
	info, ok := payload["info"].(map[string]any)
	if !ok {
		return
	}
	usage, ok := info["last_token_usage"].(map[string]any)
	if !ok {
		usage = info
	}
	total := int64(numField(usage, "total_tokens"))
	if total == 0 {
		total = int64(numField(usage, "input_tokens") + numField(usage, "output_tokens"))
	}
	if total > 0 {
		e.Tokens = total
	}
	if cached := int64(numField(usage, "cached_input_tokens")); cached > 0 {
		e.CacheReadTokens = cached
	}
	if written := int64(numField(usage, "cache_creation_input_tokens")); written > 0 {
		e.CacheWriteTokens = written
	}
	if reasoning := int64(numField(usage, "reasoning_output_tokens")); reasoning > 0 {
		e.ReasoningTokens = reasoning
	}
}

func firstNonNil(vals ...any) any {
	for _, v := range vals {
		if v != nil {
			return v
		}
	}
	return nil
}

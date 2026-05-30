package adapters

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/adewale/aha/internal/hash"
	"github.com/adewale/aha/internal/model"
)

func parseGenericJSONL(source string, file model.SessionFile, r io.Reader) (*model.ParsedSession, error) {
	ps := &model.ParsedSession{Source: source, SourceSessionID: file.SessionID, CWD: file.CWD, StartedAt: file.StartedAt, IsSubagent: file.IsSubagent, Metadata: map[string]any{"relative_path": file.RelativePath}}
	reader := bufio.NewReader(r)
	lineNo := 0
	for {
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
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			ps.Diagnostics = append(ps.Diagnostics, fmt.Sprintf("line %d: %v", lineNo, err))
			continue
		}
		if ps.CWD == "" {
			ps.CWD = stringField(m, "cwd")
		}
		if ps.StartedAt == "" {
			ps.StartedAt = stringField(m, "timestamp")
		}
		typ := stringField(m, "type")
		entryID := firstNonEmpty(stringField(m, "id"), stringField(m, "uuid"), nestedString(m, "message", "id"), nestedString(m, "message", "uuid"))
		if typ == "session" && entryID != "" {
			ps.SourceSessionID = entryID
		}
		if entryID == "" {
			entryID = fmt.Sprintf("line-%06d-%s", lineNo, hash.SHA256Bytes([]byte(raw))[:12])
		}
		role := firstNonEmpty(stringField(m, "role"), nestedString(m, "message", "role"))
		if role == "" {
			role = typ
		}
		participates := typ != "custom"
		if b, ok := firstBool(m, []string{"participatesInContext"}, []string{"participates_in_context"}, []string{"message", "participatesInContext"}, []string{"message", "participates_in_context"}); ok {
			participates = b
		}
		if excluded, ok := firstBool(m, []string{"excludeFromContext"}, []string{"exclude_from_context"}, []string{"message", "excludeFromContext"}, []string{"message", "exclude_from_context"}); ok && excluded {
			participates = false
		}
		pe := model.ParsedEntry{EntryID: entryID, ParentID: firstNonEmpty(stringField(m, "parentId"), stringField(m, "parent_id"), stringField(m, "parentUuid")), LineNo: lineNo, EntryType: typ, Timestamp: stringField(m, "timestamp"), Role: role, RawJSON: raw, Model: nestedString(m, "message", "model"), ParticipatesInContext: participates, Metadata: map[string]any{}}
		switch typ {
		case "compaction":
			pe.CompactionFirstKeptEntryID = stringField(m, "firstKeptEntryId")
			pe.CompactionTokensBefore = int64(numField(m, "tokensBefore"))
		case "model_change":
			if pe.Model == "" {
				pe.Model = stringField(m, "modelId")
			}
			if pe.Provider == "" {
				pe.Provider = stringField(m, "provider")
			}
		case "thinking_level_change":
			pe.ThinkingLevel = stringField(m, "thinkingLevel")
		case "label":
			pe.Label = stringField(m, "label")
			pe.LabelTargetEntryID = stringField(m, "targetId")
		}
		ec := extractContent(m)
		pe.Text, pe.ToolName, pe.Command, pe.FilesJSON, pe.Assets = ec.text, ec.tool, ec.command, ec.files, ec.assets
		pe.ToolCalls, pe.ToolResults = ec.toolCalls, ec.toolResults
		if pe.Role == "user" && contentHasBlockType(m, "tool_result") {
			pe.Role = "toolResult"
		}
		if pe.ToolName == "" {
			pe.ToolName = firstNonEmpty(stringField(m, "toolName"), nestedString(m, "message", "toolName"))
		}
		if pe.Model == "" {
			pe.Model = stringField(m, "model")
		}
		if usage, ok := nestedMap(m, "message", "usage"); ok {
			pe.Tokens = int64(numField(usage, "input_tokens") + numField(usage, "output_tokens") + numField(usage, "cache_creation_input_tokens") + numField(usage, "cache_read_input_tokens"))
			pe.CacheReadTokens = int64(numField(usage, "cache_read_input_tokens"))
			pe.CacheWriteTokens = int64(numField(usage, "cache_creation_input_tokens"))
			pe.ReasoningTokens = int64(numField(usage, "reasoning_output_tokens") + numField(usage, "reasoning_tokens"))
		}
		if pe.Text == "" && (role == "branchSummary" || role == "compactionSummary" || typ == "summary" || typ == "compaction" || typ == "branch_summary") {
			pe.Text = firstNonEmpty(stringField(m, "summary"), stringField(m, "text"))
		}
		ps.Entries = append(ps.Entries, pe)
	}
	return ps, nil
}

const maxOutcomeTextBytes = 4096

type extractedContent struct {
	text        string
	tool        string
	command     string
	files       string
	assets      []model.ParsedAsset
	toolCalls   []model.ParsedToolCall
	toolResults []model.ParsedToolResult
}

// ExtractToolSignals projects only tool call/result signals from a raw JSON
// transcript entry. Corpus migrations use this to backfill derived cluster data
// for DBs that were ingested before tool_invocations existed.
func ExtractToolSignals(raw string) ([]model.ParsedToolCall, []model.ParsedToolResult, error) {
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nil, nil, err
	}
	if payload, ok := m["payload"].(map[string]any); ok {
		var pe model.ParsedEntry
		fillCodexResponseItem(&pe, payload, "", "")
		if len(pe.ToolCalls) > 0 || len(pe.ToolResults) > 0 {
			return pe.ToolCalls, pe.ToolResults, nil
		}
	}
	if parts, ok := m["parts"].([]any); ok {
		_, _, _, _, _, calls, results := openCodeParts(parts)
		if len(calls) > 0 || len(results) > 0 {
			return calls, results, nil
		}
	}
	ec := extractContent(m)
	return ec.toolCalls, ec.toolResults, nil
}

func extractContent(m map[string]any) extractedContent {
	var ec extractedContent
	var parts []string
	msg, _ := m["message"].(map[string]any)
	content, ok := msg["content"]
	if !ok {
		content = m["content"]
	}
	order := 0
	toolOrdinal := 0
	resultOrdinal := 0
	var walkContent func(any, int)
	walkContent = func(v any, idx int) {
		switch x := v.(type) {
		case string:
			if x != "" {
				parts = append(parts, x)
			}
		case []any:
			for i, it := range x {
				walkContent(it, i)
			}
		case map[string]any:
			t := stringField(x, "type")
			switch t {
			case "text":
				if s := stringField(x, "text"); s != "" {
					parts = append(parts, s)
				}
			case "tool_use", "toolCall":
				call := model.ParsedToolCall{ID: stringField(x, "id"), ToolName: firstNonEmpty(stringField(x, "name"), stringField(x, "toolName")), Ordinal: toolOrdinal}
				toolOrdinal++
				input := firstNonNil(x["input"], x["arguments"])
				if b, err := json.Marshal(input); err == nil && input != nil {
					call.FilesJSON = string(b)
					if mm, ok := input.(map[string]any); ok {
						call.Command = firstNonEmpty(stringField(mm, "command"), stringField(mm, "cmd"), stringField(x, "command"))
					}
				}
				if call.Command == "" {
					call.Command = stringField(x, "command")
				}
				ec.toolCalls = append(ec.toolCalls, call)
				if ec.tool == "" {
					ec.tool = call.ToolName
				}
				if ec.command == "" {
					ec.command = call.Command
				}
				if ec.files == "" {
					ec.files = call.FilesJSON
				}
			case "image", "image_url":
				ec.assets = append(ec.assets, parseImageAsset(x, idx, order))
				order++
			case "tool_result", "toolResult":
				// Extract content so the index_tool_output config flag has
				// something to index when enabled; ingest's shouldIndexText
				// gates whether the text actually lands in messages.text.
				if inner, ok := x["content"]; ok {
					walkContent(inner, idx)
				}
				res := model.ParsedToolResult{ForID: firstNonEmpty(stringField(x, "tool_use_id"), stringField(x, "toolCallId")), IsError: boolField(x, "is_error") || boolField(x, "isError"), Ordinal: resultOrdinal}
				resultOrdinal++
				if code, ok := int64Field(x, "exit_code", "exitCode"); ok {
					res.ExitCode, res.ExitCodeValid = code, true
					if code != 0 {
						res.IsError = true
					}
				}
				if s := toolResultText(x["content"]); s != "" {
					res.OutcomeText = truncateUTF8(s, maxOutcomeTextBytes)
				}
				ec.toolResults = append(ec.toolResults, res)
			default:
				if s := stringField(x, "text"); s != "" {
					parts = append(parts, s)
				}
				if src, ok := x["source"].(map[string]any); ok && (stringField(src, "media_type") != "" || stringField(src, "data") != "") {
					ec.assets = append(ec.assets, parseImageAsset(x, idx, order))
					order++
				}
			}
		}
	}
	walkContent(content, 0)
	ec.text = strings.TrimSpace(strings.Join(parts, "\n"))
	role := firstNonEmpty(nestedString(m, "message", "role"), stringField(m, "role"))
	if role == "toolResult" && len(ec.toolResults) == 0 {
		res := model.ParsedToolResult{
			ForID:   firstNonEmpty(nestedString(m, "message", "toolCallId"), nestedString(m, "message", "tool_use_id"), stringField(m, "toolCallId"), stringField(m, "tool_use_id")),
			IsError: boolField(msg, "isError") || boolField(msg, "is_error") || boolField(m, "isError") || boolField(m, "is_error"),
		}
		if code, ok := int64Field(msg, "exit_code", "exitCode"); ok {
			res.ExitCode, res.ExitCodeValid = code, true
			if code != 0 {
				res.IsError = true
			}
		}
		if ec.text != "" {
			res.OutcomeText = truncateUTF8(ec.text, maxOutcomeTextBytes)
		}
		ec.toolResults = append(ec.toolResults, res)
		if ec.tool == "" {
			ec.tool = firstNonEmpty(nestedString(m, "message", "toolName"), stringField(m, "toolName"))
		}
	}
	return ec
}

func toolResultText(content any) string {
	switch x := content.(type) {
	case string:
		return strings.TrimSpace(x)
	case []any:
		var parts []string
		for _, it := range x {
			if mm, ok := it.(map[string]any); ok {
				if s := stringField(mm, "text"); s != "" {
					parts = append(parts, s)
				}
			} else if s, ok := it.(string); ok && s != "" {
				parts = append(parts, s)
			}
		}
		return strings.TrimSpace(strings.Join(parts, "\n"))
	default:
		return ""
	}
}

func truncateUTF8(s string, max int) string {
	if len(s) <= max {
		return s
	}
	b := []byte(s)[:max]
	for len(b) > 0 && !utf8.Valid(b) {
		b = b[:len(b)-1]
	}
	return string(b)
}

func parseImageAsset(block map[string]any, idx, order int) model.ParsedAsset {
	a := model.ParsedAsset{AssetKind: "image", ContentIndex: idx, PromptOrder: order, RawRef: mustJSON(block), Metadata: map[string]any{"block_type": stringField(block, "type")}}
	if src, ok := block["source"].(map[string]any); ok {
		a.MimeType = firstNonEmpty(stringField(src, "media_type"), stringField(src, "mime_type"))
		if data := stringField(src, "data"); data != "" {
			if b, err := base64.StdEncoding.DecodeString(data); err == nil {
				a.Data = b
				a.Width, a.Height = imageDimensions(b)
			}
		}
	}
	if a.MimeType == "" {
		a.MimeType = firstNonEmpty(stringField(block, "mimeType"), stringField(block, "mime_type"), stringField(block, "media_type"))
	}
	if len(a.Data) == 0 {
		if data := stringField(block, "data"); data != "" {
			if b, err := base64.StdEncoding.DecodeString(data); err == nil {
				a.Data = b
				a.Width, a.Height = imageDimensions(b)
			}
		}
	}
	if u := stringField(block, "url"); u != "" {
		a.RawRef = u
	}
	if a.MimeType == "" {
		a.MimeType = "application/octet-stream"
	}
	return a
}

func imageDimensions(b []byte) (int, int) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		return 0, 0
	}
	return cfg.Width, cfg.Height
}

func DecodeClaudeProjectPath(name string) string {
	if strings.HasPrefix(name, "-") {
		return "/" + strings.ReplaceAll(strings.TrimPrefix(name, "-"), "-", "/")
	}
	return name
}

func SessionIDFromPath(p string) string { return strings.TrimSuffix(filepath.Base(p), filepath.Ext(p)) }

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }
func stringField(m map[string]any, k string) string {
	if m == nil {
		return ""
	}
	switch v := m[k].(type) {
	case string:
		return v
	case json.Number:
		return v.String()
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return ""
}
func numField(m map[string]any, k string) float64 {
	switch v := m[k].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case json.Number:
		f, _ := v.Float64()
		return f
	}
	return 0
}

func int64Field(m map[string]any, keys ...string) (int64, bool) {
	for _, k := range keys {
		switch v := m[k].(type) {
		case float64:
			return int64(v), true
		case int:
			return int64(v), true
		case int64:
			return v, true
		case json.Number:
			n, err := v.Int64()
			return n, err == nil
		case string:
			n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			return n, err == nil
		}
	}
	return 0, false
}

func boolField(m map[string]any, k string) bool {
	b, _ := firstBool(m, []string{k})
	return b
}

func firstBool(m map[string]any, paths ...[]string) (bool, bool) {
	for _, path := range paths {
		if v, ok := nestedValue(m, path...); ok {
			switch b := v.(type) {
			case bool:
				return b, true
			case string:
				switch strings.ToLower(strings.TrimSpace(b)) {
				case "true", "1", "yes":
					return true, true
				case "false", "0", "no":
					return false, true
				}
			}
		}
	}
	return false, false
}

func contentHasBlockType(m map[string]any, want string) bool {
	msg, _ := m["message"].(map[string]any)
	content, ok := msg["content"]
	if !ok {
		content = m["content"]
	}
	var walk func(any) bool
	walk = func(v any) bool {
		switch x := v.(type) {
		case []any:
			for _, it := range x {
				if walk(it) {
					return true
				}
			}
		case map[string]any:
			if stringField(x, "type") == want {
				return true
			}
			if inner, ok := x["content"]; ok {
				return walk(inner)
			}
		}
		return false
	}
	return walk(content)
}

func nestedValue(m map[string]any, path ...string) (any, bool) {
	if len(path) == 0 {
		return nil, false
	}
	cur := any(m)
	for _, p := range path {
		mm, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = mm[p]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

func nestedMap(m map[string]any, path ...string) (map[string]any, bool) {
	cur := m
	for i, p := range path {
		v, ok := cur[p]
		if !ok {
			return nil, false
		}
		mm, ok := v.(map[string]any)
		if !ok {
			return nil, false
		}
		if i == len(path)-1 {
			return mm, true
		}
		cur = mm
	}
	return cur, true
}
func nestedString(m map[string]any, path ...string) string {
	if len(path) == 0 {
		return ""
	}
	cur := m
	for _, p := range path[:len(path)-1] {
		mm, ok := cur[p].(map[string]any)
		if !ok {
			return ""
		}
		cur = mm
	}
	return stringField(cur, path[len(path)-1])
}

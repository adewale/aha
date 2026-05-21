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
		raw := strings.TrimSpace(line)
		if raw == "" {
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
		pe := model.ParsedEntry{EntryID: entryID, ParentID: firstNonEmpty(stringField(m, "parentId"), stringField(m, "parent_id"), stringField(m, "parentUuid")), LineNo: lineNo, EntryType: typ, Timestamp: stringField(m, "timestamp"), Role: role, RawJSON: raw, Model: nestedString(m, "message", "model"), Metadata: map[string]any{}}
		pe.Text, pe.ToolName, pe.Command, pe.FilesJSON, pe.Assets = extractContent(m)
		if pe.ToolName == "" {
			pe.ToolName = firstNonEmpty(stringField(m, "toolName"), nestedString(m, "message", "toolName"))
		}
		if pe.Model == "" {
			pe.Model = stringField(m, "model")
		}
		if usage, ok := nestedMap(m, "message", "usage"); ok {
			pe.Tokens = int64(numField(usage, "input_tokens") + numField(usage, "output_tokens") + numField(usage, "cache_creation_input_tokens") + numField(usage, "cache_read_input_tokens"))
		}
		if pe.Text == "" && (role == "branchSummary" || role == "compactionSummary" || typ == "summary") {
			pe.Text = firstNonEmpty(stringField(m, "summary"), stringField(m, "text"))
		}
		ps.Entries = append(ps.Entries, pe)
	}
	return ps, nil
}

func extractContent(m map[string]any) (text, tool, command, files string, assets []model.ParsedAsset) {
	var parts []string
	msg, _ := m["message"].(map[string]any)
	content, ok := msg["content"]
	if !ok {
		content = m["content"]
	}
	order := 0
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
			case "tool_use":
				if tool == "" {
					tool = stringField(x, "name")
				}
				if b, err := json.Marshal(x["input"]); err == nil {
					files = string(b)
					if mm, ok := x["input"].(map[string]any); ok {
						command = stringField(mm, "command")
					}
				}
			case "image", "image_url":
				assets = append(assets, parseImageAsset(x, idx, order))
				order++
			case "tool_result":
				// Preserved raw, not indexed in v1.
			default:
				if s := stringField(x, "text"); s != "" {
					parts = append(parts, s)
				}
				if src, ok := x["source"].(map[string]any); ok && (stringField(src, "media_type") != "" || stringField(src, "data") != "") {
					assets = append(assets, parseImageAsset(x, idx, order))
					order++
				}
			}
		}
	}
	walkContent(content, 0)
	return strings.TrimSpace(strings.Join(parts, "\n")), tool, command, files, assets
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

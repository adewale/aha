package adapters

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/adewale/aha/internal/model"
)

type Pi struct{}

func (Pi) Name() string    { return "pi" }
func (Pi) Version() string { return "v1" }
func (Pi) DefaultRoots() []model.DefaultRoot {
	return []model.DefaultRoot{{OS: "all", Path: "~/.pi/agent/sessions"}}
}
func (Pi) Capabilities() model.AdapterCapabilities {
	return model.AdapterCapabilities{HasThreads: true, HasSubagents: true, HasImages: true, HasToolCalls: true, HasStableEntryIDs: true, CanLinkSubagents: true}
}
func (Pi) Discover(ctx context.Context, config model.SourceConfig) ([]model.SessionFile, error) {
	files, err := discoverJSONL(config.Root, "pi", true)
	if err != nil {
		return nil, err
	}
	for i := range files {
		if id := readPiHeaderID(files[i].Path); id != "" {
			files[i].SessionID = id
		}
	}
	return files, nil
}
func (Pi) DiscoverArtifacts(ctx context.Context, session model.SessionFile) ([]model.ArtifactFile, error) {
	var out []model.ArtifactFile
	roots := []string{filepath.Join(filepath.Dir(session.Path), "subagent-artifacts")}
	seen := map[string]bool{}
	for _, root := range roots {
		err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || seen[p] || d.Type()&os.ModeSymlink != 0 {
				return nil
			}
			seen[p] = true
			parentHint := ""
			if strings.Contains(filepath.Base(p), session.SessionID) {
				parentHint = session.SessionID
			}
			rel, _ := filepath.Rel(session.Root, p)
			out = append(out, model.ArtifactFile{Source: "pi", Root: root, Path: p, RelativePath: filepath.ToSlash(rel), Kind: "artifact", ParentHint: parentHint})
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	}
	return out, nil
}
func readPiHeaderID(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	line, err := bufio.NewReader(f).ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return ""
	}
	return piHeaderIDFromRaw(line)
}

func piHeaderIDFromRaw(raw []byte) string {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return ""
	}
	if typ, _ := m["type"].(string); typ != "session" {
		return ""
	}
	id, _ := m["id"].(string)
	return id
}

func (Pi) ParseSession(ctx context.Context, file model.SessionFile, r io.Reader) (*model.ParsedSession, error) {
	ps, err := parseGenericJSONL("pi", file, r)
	if err != nil {
		return nil, err
	}
	if len(ps.Entries) > 0 && ps.Entries[0].EntryType == "session" {
		ps.Metadata["header"] = ps.Entries[0].RawJSON
		if id := piHeaderIDFromRaw([]byte(ps.Entries[0].RawJSON)); id != "" {
			ps.SourceSessionID = id
		}
		ps.Entries = ps.Entries[1:]
	}
	projectPiNativeShapes(ps)
	return ps, nil
}

// projectPiNativeShapes fills typed projection columns from Pi's own
// content-block and usage conventions, which differ from the
// Anthropic-shaped schema the generic parser understands:
//
//   - content blocks use type "toolCall" (name + arguments) rather than
//     "tool_use" (name + input), and "thinking" rather than reasoning
//     embedded in text;
//   - message.usage uses camelCase keys (input/output/cacheRead/
//     cacheWrite/totalTokens) and a nested cost object.
//
// Without this pass, Pi tool calls project no tool_name/command and Pi
// assistant turns project zero tokens. The data survives in raw_json;
// this makes it queryable. No-op for entries already populated by the
// generic parser (Anthropic-shaped Pi entries, if any).
func projectPiNativeShapes(ps *model.ParsedSession) {
	for i := range ps.Entries {
		e := &ps.Entries[i]
		var m map[string]any
		if err := json.Unmarshal([]byte(e.RawJSON), &m); err != nil {
			continue
		}
		if img, ok := m["image"].(map[string]any); ok {
			block := map[string]any{"type": "image"}
			for k, v := range img {
				block[k] = v
			}
			e.Assets = append(e.Assets, parseImageAsset(block, len(e.Assets), len(e.Assets)))
		}
		msg, _ := m["message"].(map[string]any)
		if msg == nil {
			continue
		}
		if content, ok := msg["content"].([]any); ok {
			projectPiContentBlocks(e, content)
		}
		if usage, ok := msg["usage"].(map[string]any); ok {
			projectPiUsage(e, usage)
		}
	}
}

func projectPiContentBlocks(e *model.ParsedEntry, content []any) {
	var thinking []string
	for _, raw := range content {
		block, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		switch stringField(block, "type") {
		case "toolCall":
			if e.ToolName == "" {
				e.ToolName = stringField(block, "name")
			}
			if args, ok := block["arguments"].(map[string]any); ok {
				if e.FilesJSON == "" {
					if b, err := json.Marshal(args); err == nil {
						e.FilesJSON = string(b)
					}
				}
				if e.Command == "" {
					e.Command = stringField(args, "command")
				}
			}
		case "thinking":
			if t := stringField(block, "thinking"); t != "" {
				thinking = append(thinking, t)
			}
		}
	}
	if len(thinking) > 0 {
		joined := strings.Join(thinking, "\n")
		if e.Text == "" {
			e.Text = joined
		} else {
			e.Text = joined + "\n" + e.Text
		}
	}
}

func projectPiUsage(e *model.ParsedEntry, usage map[string]any) {
	total := int64(numField(usage, "totalTokens"))
	if total == 0 {
		total = int64(numField(usage, "input") + numField(usage, "output") + numField(usage, "cacheRead") + numField(usage, "cacheWrite"))
	}
	if total > 0 && e.Tokens == 0 {
		e.Tokens = total
	}
	if cr := int64(numField(usage, "cacheRead")); cr > 0 && e.CacheReadTokens == 0 {
		e.CacheReadTokens = cr
	}
	if cw := int64(numField(usage, "cacheWrite")); cw > 0 && e.CacheWriteTokens == 0 {
		e.CacheWriteTokens = cw
	}
	if cost, ok := usage["cost"].(map[string]any); ok && e.Cost == 0 {
		e.Cost = numField(cost, "total")
	}
}

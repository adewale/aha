package adapters

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/adewale/aha/internal/hash"
	"github.com/adewale/aha/internal/model"
	"github.com/adewale/aha/internal/opencodeexport"
	"github.com/adewale/aha/internal/paths"
)

// OpenCode reads sessions from OpenCode's local SQLite database. Because the
// rest of the pipeline (snapshot, bundle, ingest) is built around JSONL
// session files, the adapter converts the database into deterministic, lossless
// JSONL files during Discover and then parses those files like any other JSONL
// source. The conversion is delegated to internal/opencodeexport so this file
// stays free of filesystem-mutation calls (see read_only_test.go).
type OpenCode struct{}

func (OpenCode) Name() string    { return "opencode" }
func (OpenCode) Version() string { return "v1" }
func (OpenCode) DefaultRoots() []model.DefaultRoot {
	return []model.DefaultRoot{{OS: "all", Path: "~/.local/share/opencode"}}
}
func (OpenCode) Capabilities() model.AdapterCapabilities {
	return model.AdapterCapabilities{HasThreads: true, HasSubagents: false, HasImages: true, HasToolCalls: true, HasStableEntryIDs: true, CanLinkSubagents: false}
}

func (OpenCode) Discover(ctx context.Context, config model.SourceConfig) ([]model.SessionFile, error) {
	var out []model.SessionFile
	for _, db := range openCodeDBPaths(config.Root) {
		dest, err := openCodeExportDir(db)
		if err != nil {
			return nil, err
		}
		sessions, err := opencodeexport.Run(ctx, db, dest)
		if err != nil {
			return nil, err
		}
		for _, s := range sessions {
			out = append(out, model.SessionFile{Source: "opencode", Root: dest, Path: s.Path, RelativePath: s.File, SessionID: s.ID})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func (OpenCode) DiscoverArtifacts(ctx context.Context, session model.SessionFile) ([]model.ArtifactFile, error) {
	return nil, nil
}

func (OpenCode) ParseSession(ctx context.Context, file model.SessionFile, r io.Reader) (*model.ParsedSession, error) {
	return parseOpenCode(file, r)
}

// openCodeDBPaths resolves the OpenCode database files to read, honouring the
// OPENCODE_DB override and any release-channel databases beside the default.
func openCodeDBPaths(root string) []string {
	var cands []string
	if env := strings.TrimSpace(os.Getenv("OPENCODE_DB")); env != "" {
		if p, err := paths.Expand(env); err == nil {
			cands = append(cands, p)
		}
	}
	if base, err := paths.Expand(root); err == nil && base != "" {
		if strings.HasSuffix(base, ".db") {
			cands = append(cands, base)
		} else {
			cands = append(cands, filepath.Join(base, "opencode.db"))
			if matches, err := filepath.Glob(filepath.Join(base, "opencode-*.db")); err == nil {
				sort.Strings(matches)
				cands = append(cands, matches...)
			}
		}
	}
	seen := map[string]bool{}
	var out []string
	for _, c := range cands {
		abs, err := filepath.Abs(c)
		if err != nil || seen[abs] {
			continue
		}
		seen[abs] = true
		if st, err := os.Stat(abs); err == nil && st.Mode().IsRegular() {
			out = append(out, abs)
		}
	}
	return out
}

// openCodeExportDir returns a stable per-database directory for the generated
// JSONL files. Stability matters: the snapshot reuse fingerprint includes each
// file's path, so a non-deterministic location would defeat unchanged-state
// detection and re-upload on every refresh. AHA_OPENCODE_EXPORT_DIR overrides
// the base location (used by the smoketest to keep all artifacts under /tmp).
func openCodeExportDir(dbPath string) (string, error) {
	base := strings.TrimSpace(os.Getenv("AHA_OPENCODE_EXPORT_DIR"))
	if base == "" {
		cache, err := os.UserCacheDir()
		if err != nil || cache == "" {
			cache = filepath.Join(os.TempDir(), "aha-cache")
		}
		base = filepath.Join(cache, "aha", "opencode-export")
	}
	return filepath.Join(base, hash.SHA256Bytes([]byte(dbPath))[:12]), nil
}

func parseOpenCode(file model.SessionFile, r io.Reader) (*model.ParsedSession, error) {
	ps := &model.ParsedSession{Source: "opencode", SourceSessionID: file.SessionID, CWD: file.CWD, StartedAt: file.StartedAt, IsSubagent: file.IsSubagent, Metadata: map[string]any{"relative_path": file.RelativePath}}
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
		if jErr := json.Unmarshal([]byte(raw), &m); jErr != nil {
			ps.Diagnostics = append(ps.Diagnostics, fmt.Sprintf("line %d: %v", lineNo, jErr))
			continue
		}
		row, _ := m["row"].(map[string]any)
		switch stringField(m, "type") {
		case "session":
			if id := stringField(m, "id"); id != "" {
				ps.SourceSessionID = id
			}
			data := openCodeData(row)
			if ps.CWD == "" {
				ps.CWD = firstNonEmpty(stringField(data, "directory"), stringField(data, "cwd"), stringField(data, "worktree"))
			}
			if ps.StartedAt == "" {
				ps.StartedAt = openCodeTime(data)
			}
			ps.Metadata["session"] = raw
		case "message":
			ps.Entries = append(ps.Entries, openCodeMessageEntry(m, row, lineNo, raw))
		}
		if err == io.EOF {
			break
		}
	}
	return ps, nil
}

func openCodeMessageEntry(m, row map[string]any, lineNo int, raw string) model.ParsedEntry {
	data := openCodeData(row)
	pe := model.ParsedEntry{
		EntryID:   firstNonEmpty(stringField(m, "id"), stringField(row, "id")),
		LineNo:    lineNo,
		EntryType: "message",
		Role:      firstNonEmpty(stringField(data, "role"), stringField(row, "role")),
		Timestamp: openCodeTime(data),
		RawJSON:   raw,
		Model:     firstNonEmpty(stringField(data, "modelID"), stringField(data, "model")),
		Provider:  stringField(data, "providerID"),
		Cost:      numField(data, "cost"),
		Tokens:    openCodeTokens(data),
		Metadata:  map[string]any{},
	}
	parts, _ := m["parts"].([]any)
	pe.Text, pe.ToolName, pe.Command, pe.FilesJSON, pe.Assets = openCodeParts(parts)
	return pe
}

// openCodeData returns the parsed JSON `data` column of a row, which is where
// OpenCode stores most message/session attributes.
func openCodeData(row map[string]any) map[string]any {
	if row == nil {
		return nil
	}
	if d, ok := row["data"].(map[string]any); ok {
		return d
	}
	return row
}

func openCodeParts(parts []any) (text, tool, command, filesJSON string, assets []model.ParsedAsset) {
	var texts []string
	order := 0
	for i, p := range parts {
		pm, ok := p.(map[string]any)
		if !ok {
			continue
		}
		data := openCodeData(pm)
		switch stringField(data, "type") {
		case "text":
			if s := stringField(data, "text"); s != "" {
				texts = append(texts, s)
			}
		case "tool":
			if tool == "" {
				tool = firstNonEmpty(stringField(data, "tool"), stringField(data, "name"))
			}
			if state, ok := data["state"].(map[string]any); ok {
				if in, ok := state["input"].(map[string]any); ok {
					if command == "" {
						command = stringField(in, "command")
					}
					if filesJSON == "" {
						filesJSON = mustJSON(in)
					}
				}
			}
		case "file":
			if a, ok := openCodeFileAsset(data, i, order); ok {
				assets = append(assets, a)
				order++
			}
		}
	}
	return strings.TrimSpace(strings.Join(texts, "\n")), tool, command, filesJSON, assets
}

func openCodeFileAsset(data map[string]any, idx, order int) (model.ParsedAsset, bool) {
	mime := firstNonEmpty(stringField(data, "mime"), stringField(data, "mimeType"), stringField(data, "media_type"))
	url := stringField(data, "url")
	if !strings.HasPrefix(mime, "image/") && !strings.HasPrefix(url, "data:image/") {
		return model.ParsedAsset{}, false
	}
	a := model.ParsedAsset{AssetKind: "image", ContentIndex: idx, PromptOrder: order, RawRef: url, MimeType: mime, Metadata: map[string]any{"block_type": "file"}}
	if strings.HasPrefix(url, "data:") {
		if comma := strings.IndexByte(url, ','); comma >= 0 {
			meta := url[5:comma]
			if a.MimeType == "" {
				a.MimeType = strings.TrimSuffix(meta, ";base64")
			}
			if strings.Contains(meta, "base64") {
				if b, err := base64.StdEncoding.DecodeString(url[comma+1:]); err == nil {
					a.Data = b
					a.Width, a.Height = imageDimensions(b)
				}
			}
		}
	}
	if a.RawRef == "" {
		a.RawRef = mustJSON(data)
	}
	if a.MimeType == "" {
		a.MimeType = "application/octet-stream"
	}
	return a, true
}

func openCodeTokens(data map[string]any) int64 {
	tk, ok := data["tokens"].(map[string]any)
	if !ok {
		return 0
	}
	sum := numField(tk, "input") + numField(tk, "output") + numField(tk, "reasoning")
	if cache, ok := tk["cache"].(map[string]any); ok {
		sum += numField(cache, "read") + numField(cache, "write")
	}
	return int64(sum)
}

func openCodeTime(data map[string]any) string {
	if data == nil {
		return ""
	}
	if tm, ok := data["time"].(map[string]any); ok {
		if ms := numField(tm, "created"); ms > 0 {
			return time.UnixMilli(int64(ms)).UTC().Format(time.RFC3339)
		}
		if s := stringField(tm, "created"); s != "" {
			return s
		}
	}
	if s := stringField(data, "time"); s != "" {
		return s
	}
	return ""
}

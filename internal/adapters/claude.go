package adapters

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/adewale/aha/internal/model"
)

type ClaudeCode struct{}

func (ClaudeCode) Name() string    { return "claude-code" }
func (ClaudeCode) Version() string { return "v1" }
func (ClaudeCode) DefaultRoots() []model.DefaultRoot {
	return []model.DefaultRoot{{OS: "all", Path: "~/.claude/projects"}}
}
func (ClaudeCode) Capabilities() model.AdapterCapabilities {
	return model.AdapterCapabilities{HasThreads: false, HasSubagents: true, HasImages: true, HasToolCalls: true, HasStableEntryIDs: false, CanLinkSubagents: false}
}
func (ClaudeCode) Discover(ctx context.Context, config model.SourceConfig) ([]model.SessionFile, error) {
	items, err := os.ReadDir(config.Root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []model.SessionFile
	for _, it := range items {
		if !it.IsDir() || strings.HasPrefix(it.Name(), ".") {
			continue
		}
		dir := filepath.Join(config.Root, it.Name())
		matches, _ := filepath.Glob(filepath.Join(dir, "*.jsonl"))
		sort.Strings(matches)
		for _, p := range matches {
			if info, err := os.Lstat(p); err != nil || info.Mode()&os.ModeSymlink != 0 {
				continue
			}
			sid := SessionIDFromPath(p)
			out = append(out, model.SessionFile{Source: "claude-code", Root: config.Root, Path: p, RelativePath: filepath.ToSlash(filepath.Join(it.Name(), filepath.Base(p))), SessionID: sid, CWD: DecodeClaudeProjectPath(it.Name()), IsSubagent: strings.HasPrefix(filepath.Base(p), "agent-")})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}
func (ClaudeCode) DiscoverArtifacts(ctx context.Context, session model.SessionFile) ([]model.ArtifactFile, error) {
	return nil, nil
}
func (ClaudeCode) ParseSession(ctx context.Context, file model.SessionFile, r io.Reader) (*model.ParsedSession, error) {
	return parseGenericJSONL("claude-code", file, r)
}

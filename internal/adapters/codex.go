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
	return parseGenericJSONL("codex", file, r)
}

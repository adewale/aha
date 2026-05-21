package adapters

import (
	"context"
	"io"

	"github.com/adewale/aha/internal/model"
)

type SourceAdapter interface {
	Name() string
	Version() string
	DefaultRoots() []model.DefaultRoot
	Capabilities() model.AdapterCapabilities
	Discover(ctx context.Context, config model.SourceConfig) ([]model.SessionFile, error)
	DiscoverArtifacts(ctx context.Context, session model.SessionFile) ([]model.ArtifactFile, error)
	ParseSession(ctx context.Context, file model.SessionFile, r io.Reader) (*model.ParsedSession, error)
}

func Builtins() map[string]SourceAdapter {
	return map[string]SourceAdapter{
		"pi":          Pi{},
		"claude-code": ClaudeCode{},
		"codex":       CodexCLI{},
	}
}

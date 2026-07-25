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
	// VersionSignal reports where this source records its version and what
	// that version measures, read from the record's raw line. It is on the
	// interface so a new adapter cannot be registered without declaring
	// either where its version lives or that it has none — the declaration
	// is what lets the source-version audit say how much of a producer's
	// release history the fixtures actually cover.
	VersionSignal(entry model.ParsedEntry) model.VersionSignal
}

func Builtins() map[string]SourceAdapter {
	return map[string]SourceAdapter{
		"pi":          Pi{},
		"claude-code": ClaudeCode{},
		"codex":       CodexCLI{},
		"opencode":    OpenCode{},
	}
}

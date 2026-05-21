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
	return ps, nil
}

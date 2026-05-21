package adapters

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/adewale/aha/internal/model"
)

func discoverJSONL(root, source string, recursive bool) ([]model.SessionFile, error) {
	var out []model.SessionFile
	walk := func(p string, d os.DirEntry, err error) error {
		if err != nil {
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
		rel, _ := filepath.Rel(root, p)
		sid := strings.TrimSuffix(filepath.Base(p), filepath.Ext(p))
		out = append(out, model.SessionFile{Source: source, Root: root, Path: p, RelativePath: filepath.ToSlash(rel), SessionID: sid, IsSubagent: strings.HasPrefix(filepath.Base(p), "agent-")})
		return nil
	}
	if recursive {
		err := filepath.WalkDir(root, walk)
		if os.IsNotExist(err) {
			return nil, nil
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
		return out, err
	}
	items, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	for _, it := range items {
		_ = walk(filepath.Join(root, it.Name()), it, nil)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

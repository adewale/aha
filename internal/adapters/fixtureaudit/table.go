package fixtureaudit

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

// SourcePath identifies one classified field: a key-path as observed under one
// adapter's on-disk shape. Two producers can spell a field the same way and
// mean different things — Pi's `version` is an on-disk schema revision, Claude
// Code's is a producer version — so the source is part of the key, not a note
// in the classification text.
type SourcePath struct {
	Source string
	Path   string
}

// ProjectionTable records what the corpus does with every observed field.
type ProjectionTable struct {
	version int
	byPair  map[SourcePath]string
}

// ProjectionTableVersion is the only on-disk layout this loader accepts.
// Version 1 was keyed by key-path alone, which gave same-named fields from
// different producers one shared classification; it is rejected rather than
// read leniently, because reading it at all would reintroduce the collision.
const ProjectionTableVersion = 2

type projectionTableDoc struct {
	Version int                          `json:"version"`
	Doc     string                       `json:"doc"`
	Sources map[string]map[string]string `json:"sources"`
}

// LoadProjectionTable reads the classification table at path.
func LoadProjectionTable(path string) (ProjectionTable, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return ProjectionTable{}, err
	}
	var doc projectionTableDoc
	if err := json.Unmarshal(b, &doc); err != nil {
		return ProjectionTable{}, fmt.Errorf("decode %s: %w", path, err)
	}
	if doc.Version != ProjectionTableVersion {
		return ProjectionTable{}, fmt.Errorf("%s is version %d, want %d keyed by (source, path)", path, doc.Version, ProjectionTableVersion)
	}
	table := ProjectionTable{version: doc.Version, byPair: map[SourcePath]string{}}
	for source, paths := range doc.Sources {
		for p, classification := range paths {
			if classification == "" {
				return ProjectionTable{}, fmt.Errorf("%s: %s %s has an empty classification", path, source, p)
			}
			table.byPair[SourcePath{Source: source, Path: p}] = classification
		}
	}
	if len(table.byPair) == 0 {
		return ProjectionTable{}, fmt.Errorf("%s classifies no paths", path)
	}
	return table, nil
}

// Version reports the on-disk schema version of the table.
func (t ProjectionTable) Version() int { return t.version }

// Classification returns what the corpus does with path as observed under
// source.
func (t ProjectionTable) Classification(source, path string) (string, bool) {
	c, ok := t.byPair[SourcePath{Source: source, Path: path}]
	return c, ok
}

// Pairs returns every classified (source, path), ordered by source then path.
func (t ProjectionTable) Pairs() []SourcePath {
	out := make([]SourcePath, 0, len(t.byPair))
	for pair := range t.byPair {
		out = append(out, pair)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Source != out[j].Source {
			return out[i].Source < out[j].Source
		}
		return out[i].Path < out[j].Path
	})
	return out
}

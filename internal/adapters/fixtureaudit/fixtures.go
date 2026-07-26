// Package fixtureaudit inventories the committed adapter fixtures and derives
// the artefacts that keep the projection surface honest: the (source, path)
// key-path census behind the coverage gate, and the generated source-version
// audit.
//
// Fixtures are attributed to the adapter whose on-disk shape they carry.
// Attribution is declared, never inferred from the filename at read time:
// Inventory fails on any fixture it cannot attribute and on any declared
// fixture that has gone missing, so the corpus cannot grow or shrink without
// the change being named here.
package fixtureaudit

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// handFixtureSources attributes each hand-written fixture at the root of the
// fixture directory to the adapter whose shape it imitates.
var handFixtureSources = map[string]string{
	"claude_realish.jsonl":       "claude-code",
	"codex_modern_realish.jsonl": "codex",
	"codex_realish.jsonl":        "codex",
	"coverage_claude.jsonl":      "claude-code",
	"coverage_codex.jsonl":       "codex",
	"coverage_pi.jsonl":          "pi",
	"opencode_realish.jsonl":     "opencode",
	"pi_realish.jsonl":           "pi",
}

// corpusSources attributes each vendored corpus directory to the adapter that
// produced it. Every .jsonl beneath a listed directory inherits its source.
var corpusSources = map[string]string{
	"claude-code-sample": "claude-code",
	"codex-sample":       "codex",
	"pi-mono-sample":     "pi",
}

// Sources returns every adapter name the fixture corpus can attribute a file
// to, sorted. A source appears here whether or not it currently owns a
// fixture, so an adapter with no evidence is visible as an empty row rather
// than as an absence.
func Sources() []string {
	seen := map[string]struct{}{}
	for _, s := range handFixtureSources {
		seen[s] = struct{}{}
	}
	for _, s := range corpusSources {
		seen[s] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// Fixture is one committed JSONL fixture attributed to a single adapter.
type Fixture struct {
	// Source is the adapter name, matching a key of adapters.Builtins().
	Source string
	// Path is the fixture's location on disk.
	Path string
	// Label is the slash-separated path relative to the fixture root. It is
	// the stable identity used in failure messages and in the audit, so the
	// artefacts do not embed the checkout location.
	Label string
}

// Inventory returns every committed fixture beneath root, attributed to its
// source and ordered by label. It fails rather than guess: an unattributed
// .jsonl at the root, an unknown corpus directory, or a declared fixture that
// no longer exists are all errors.
func Inventory(root string) ([]Fixture, error) {
	out, err := handFixtures(root)
	if err != nil {
		return nil, err
	}
	corpora, err := corpusFixtures(root)
	if err != nil {
		return nil, err
	}
	out = append(out, corpora...)
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out, nil
}

func handFixtures(root string) ([]Fixture, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	present := map[string]struct{}{}
	var out []Fixture
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		source, ok := handFixtureSources[e.Name()]
		if !ok {
			return nil, fmt.Errorf("fixture %s has no declared source: add it to handFixtureSources in internal/adapters/fixtureaudit/fixtures.go", e.Name())
		}
		present[e.Name()] = struct{}{}
		out = append(out, Fixture{Source: source, Path: filepath.Join(root, e.Name()), Label: e.Name()})
	}
	for name := range handFixtureSources {
		if _, ok := present[name]; !ok {
			return nil, fmt.Errorf("declared fixture %s is missing from %s: remove it from handFixtureSources or restore the file", name, root)
		}
	}
	return out, nil
}

func corpusFixtures(root string) ([]Fixture, error) {
	corporaDir := filepath.Join(root, "corpora")
	entries, err := os.ReadDir(corporaDir)
	if err != nil {
		return nil, err
	}
	present := map[string]struct{}{}
	var out []Fixture
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		source, ok := corpusSources[e.Name()]
		if !ok {
			return nil, fmt.Errorf("corpus directory %s has no declared source: add it to corpusSources in internal/adapters/fixtureaudit/fixtures.go", e.Name())
		}
		present[e.Name()] = struct{}{}
		files, err := filepath.Glob(filepath.Join(corporaDir, e.Name(), "*.jsonl"))
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			out = append(out, Fixture{Source: source, Path: f, Label: filepath.ToSlash(filepath.Join("corpora", e.Name(), filepath.Base(f)))})
		}
	}
	for name := range corpusSources {
		if _, ok := present[name]; !ok {
			return nil, fmt.Errorf("declared corpus %s is missing from %s: remove it from corpusSources or restore the directory", name, corporaDir)
		}
	}
	return out, nil
}

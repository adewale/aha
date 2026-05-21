package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/adewale/aha/internal/corpus"
	"github.com/adewale/aha/internal/model"
	"github.com/adewale/aha/internal/search"
)

type renderMode string

const (
	renderHuman renderMode = "human"
	renderJSON  renderMode = "json"
	renderRefs  renderMode = "refs"
	renderFiles renderMode = "files"
	renderMD    renderMode = "md"
)

func writeJSON(w io.Writer, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
}

func renderSearchResults(w io.Writer, results []search.Result, mode renderMode) error {
	switch mode {
	case renderJSON:
		return writeJSON(w, results)
	case renderRefs:
		for _, r := range results {
			fmt.Fprintf(w, "%s\t%s\t%s\n", resultRef(r), r.Role, r.Snippet)
		}
	case renderFiles:
		for _, r := range results {
			fmt.Fprintf(w, "%s\t%s\t%s\n", resultRef(r), r.Project, r.Snippet)
		}
	case renderMD:
		for _, r := range results {
			fmt.Fprintf(w, "- `%s` **%s** `%s` score=%.4f — %s\n", resultRef(r), r.Role, r.Source, r.Score, strings.ReplaceAll(r.Snippet, "\n", " "))
		}
	default:
		for _, r := range results {
			fmt.Fprintf(w, "%.4f %s %s %s %s %s %s %s %s\n", r.Score, r.Timestamp, r.Source, r.Machine, shortProject(r.Project), r.Role, r.Snippet, r.SessionKey, r.EntryID)
		}
	}
	return nil
}

func resultRef(r search.Result) string {
	if r.RefText != "" {
		return r.RefText
	}
	if r.Ref.Kind != "" {
		return model.FormatHitRef(r.Ref)
	}
	if r.EntryID == "" {
		return r.SessionKey
	}
	return fmt.Sprintf("%s#%s", r.SessionKey, r.EntryID)
}

func renderReadEntries(w io.Writer, entries []corpus.ReadEntry, mode renderMode) error {
	switch mode {
	case renderJSON:
		return writeJSON(w, entries)
	case renderMD:
		for _, e := range entries {
			body := e.Text
			if body == "" {
				body = e.RawJSON
			}
			fmt.Fprintf(w, "## %s %s %s\n\n%s\n\n", e.EntryID, e.Timestamp, e.Role, body)
		}
	default:
		for _, e := range entries {
			body := e.Text
			if body == "" {
				body = e.RawJSON
			}
			fmt.Fprintf(w, "--- %d %s %s %s ---\n%s\n", e.LineNo, e.EntryID, e.Timestamp, e.Role, body)
		}
	}
	return nil
}

func renderMap(w io.Writer, stats map[string]any) error {
	keys := make([]string, 0, len(stats))
	for k := range stats {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(w, "%s: %v\n", k, stats[k])
	}
	return nil
}

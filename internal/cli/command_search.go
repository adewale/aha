package cli

import (
	"errors"
	"flag"
	"io"
	"strings"

	"github.com/adewale/aha/internal/search"
)

func cmdSearch(args []string, stdout, stderr io.Writer) error {
	args = reorderSearchArgs(args)
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cf := registerCorpusFlags(fs)
	source := fs.String("source", "", "source filter")
	machine := fs.String("machine", "", "machine filter")
	role := fs.String("role", "", "role filter")
	after := fs.String("after", "", "after date")
	before := fs.String("before", "", "before date")
	pathFilter := fs.String("path", "", "path/cwd filter")
	jsonOut := fs.Bool("json", false, "JSON output")
	refsOut := fs.Bool("refs", false, "refs output")
	filesOut := fs.Bool("files", false, "files output")
	mdOut := fs.Bool("md", false, "Markdown output")
	limit := fs.Int("limit", 20, "limit")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return errors.New("search requires query")
	}
	if err := requireAtMostOneOutputMode(*jsonOut, *refsOut, *filesOut, *mdOut); err != nil {
		return err
	}
	cfg, err := cf.loadConfig()
	if err != nil {
		return err
	}
	store, err := openCorpusForCommand(cfg, false)
	if err != nil {
		return err
	}
	defer store.Close()
	results, err := search.Query(store.DB, strings.Join(fs.Args(), " "), search.Filters{Source: *source, Machine: *machine, Role: *role, After: *after, Before: *before, Path: *pathFilter, Limit: *limit})
	if err != nil {
		return err
	}
	mode := renderHuman
	if *jsonOut {
		mode = renderJSON
	} else if *refsOut {
		mode = renderRefs
	} else if *filesOut {
		mode = renderFiles
	} else if *mdOut {
		mode = renderMD
	}
	return renderSearchResults(stdout, results, mode)
}

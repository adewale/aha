package cli

import (
	"errors"
	"io"
	"strings"

	"github.com/adewale/aha/internal/search"
)

func cmdSearch(args []string, stdout, stderr io.Writer) error {
	args = reorderSearchArgs(args)
	pf, err := parseFlagSpecs("search", args, stderr, searchFlagSpecs)
	if err != nil {
		return err
	}
	cf := corpusFlags{corpusDir: stringPtr(pf.String("corpus")), repoDir: stringPtr(pf.String("repo")), config: stringPtr(pf.String("config"))}
	if pf.NArg() == 0 {
		return errors.New("search requires query")
	}
	if err := requireAtMostOneOutputMode(pf.Bool("json"), pf.Bool("refs"), pf.Bool("files"), pf.Bool("md")); err != nil {
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
	results, err := search.Query(store.DB, strings.Join(pf.Args(), " "), search.Filters{Source: pf.String("source"), Machine: pf.String("machine"), Role: pf.String("role"), After: pf.String("after"), Before: pf.String("before"), Path: pf.String("path"), Limit: pf.Int("limit")})
	if err != nil {
		return err
	}
	mode := renderHuman
	if pf.Bool("json") {
		mode = renderJSON
	} else if pf.Bool("refs") {
		mode = renderRefs
	} else if pf.Bool("files") {
		mode = renderFiles
	} else if pf.Bool("md") {
		mode = renderMD
	}
	return renderSearchResults(stdout, results, mode)
}

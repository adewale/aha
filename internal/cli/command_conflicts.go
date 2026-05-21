package cli

import (
	"flag"
	"fmt"
	"io"

	"github.com/adewale/aha/internal/corpus"
)

func cmdConflicts(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("conflicts", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cf := registerCorpusFlags(fs)
	jsonOut := fs.Bool("json", false, "JSON output")
	if err := fs.Parse(args); err != nil {
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
	cs, err := corpus.Conflicts(store.DB)
	if err != nil {
		return err
	}
	if *jsonOut {
		return writeJSON(stdout, cs)
	}
	for _, c := range cs {
		fmt.Fprintf(stdout, "%d %s %s %s %s %s\n", c.ID, c.SessionKey, c.EntryID, c.First, c.Second, c.CreatedAt)
	}
	return nil
}

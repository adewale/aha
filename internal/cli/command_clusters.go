package cli

import (
	"flag"
	"fmt"
	"io"

	"github.com/adewale/aha/internal/corpus"
)

func cmdClusters(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("clusters", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cf := registerCorpusFlags(fs)
	jsonOut := fs.Bool("json", false, "JSON output")
	limit := fs.Int("limit", 50, "maximum clusters to return (default 50, max 200)")
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
	clusters, err := corpus.Clusters(store.DB, *limit)
	if err != nil {
		return err
	}
	if *jsonOut {
		return writeJSON(stdout, clusters)
	}
	if len(clusters) == 0 {
		fmt.Fprintln(stdout, "no error clusters found")
		return nil
	}
	for _, c := range clusters {
		ref := c.SampleRef
		if ref == "" {
			ref = "(no sample_ref)"
		}
		fmt.Fprintf(stdout, "%-6.1f x%-4d s=%-3d p=%-3d  %s :: %s :: %s  ref=%s\n",
			c.Score, c.Count, c.Sessions, c.Projects, c.ToolName, c.CommandFamily, c.ErrorSignature, ref)
	}
	return nil
}

package cli

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/adewale/aha/internal/corpus"
)

func cmdClusters(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("clusters", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cf := registerCorpusFlags(fs)
	jsonOut := fs.Bool("json", false, "JSON output")
	limit := fs.Int("limit", 50, "maximum clusters to return (default 50, max 200)")
	withFixes := fs.Bool("with-fixes", false, "rank resolved clusters with the resolution path(s) that fixed them")
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

	if *withFixes {
		candidates, err := corpus.SkillCandidates(store.DB, *limit)
		if err != nil {
			return err
		}
		if *jsonOut {
			return writeJSON(stdout, candidates)
		}
		if len(candidates) == 0 {
			fmt.Fprintln(stdout, "no resolved error clusters found")
			return nil
		}
		for _, c := range candidates {
			// Pad the resolved n/m field so the tool :: family :: signature tail
			// starts at a fixed column regardless of count width.
			resolved := fmt.Sprintf("%d/%d", c.Resolved, c.Episodes)
			fmt.Fprintf(stdout, "%-6.1f %-11s resolved %-7s  %s :: %s :: %s\n",
				c.Score, c.Tier, resolved, c.ToolName, c.CommandFamily, c.ErrorSignature)
			for _, p := range c.Paths {
				ref := p.SampleRef
				if ref == "" {
					ref = "(no sample_ref)"
				}
				// Same s=/p= width budget as the plain cluster output below, so
				// the two output modes line up.
				fmt.Fprintf(stdout, "    fix conf=%.2f x%-4d s=%-3d p=%-3d  %s  ref=%s\n",
					p.Confidence, p.Support, p.Sessions, p.Projects, strings.Join(p.Families, " > "), ref)
			}
		}
		return nil
	}

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

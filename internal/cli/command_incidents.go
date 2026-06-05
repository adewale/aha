package cli

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/adewale/aha/internal/corpus"
)

func cmdIncidents(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("incidents", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cf := registerCorpusFlags(fs)
	jsonOut := fs.Bool("json", false, "JSON output")
	limit := fs.Int("limit", 50, "maximum incidents to return (default 50, max 200)")
	state := fs.String("state", "", "filter by state: unresolved | partial | resolved")
	project := fs.String("project", "", "filter to one project key")
	source := fs.String("source", "", "filter to one source adapter")
	machine := fs.String("machine", "", "filter to one machine id")
	tool := fs.String("tool", "", "filter to one tool name")
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

	incidents, err := corpus.Incidents(store.DB, corpus.IncidentFilter{
		Limit:   *limit,
		Project: *project,
		Source:  *source,
		Machine: *machine,
		Tool:    *tool,
		State:   *state,
	})
	if err != nil {
		return err
	}

	if *jsonOut {
		return writeJSON(stdout, incidents)
	}
	if len(incidents) == 0 {
		fmt.Fprintln(stdout, "no incidents found")
		return nil
	}
	for _, in := range incidents {
		fmt.Fprintf(stdout, "%-6.1f %-10s %s %-7s  %s :: %s :: %s\n",
			in.Score, in.State, tierOrDash(in.Tier), fmt.Sprintf("%d/%d", in.Resolved, in.Episodes),
			in.ToolName, in.CommandFamily, in.ErrorSignature)
		for _, p := range in.Paths {
			ref := p.SampleRef
			if ref == "" {
				ref = "(no sample_ref)"
			}
			fmt.Fprintf(stdout, "    fix conf=%.2f x%-4d s=%-3d p=%-3d  %s  ref=%s\n",
				p.Confidence, p.Support, p.Sessions, p.Projects, strings.Join(p.Families, " > "), ref)
		}
	}
	return nil
}

func tierOrDash(tier string) string {
	if tier == "" {
		return "-"
	}
	return tier
}

package cli

import (
	"flag"
	"fmt"
	"io"

	"github.com/adewale/aha/internal/corpus"
)

func cmdVerify(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cf := registerCorpusFlags(fs)
	jsonOut := fs.Bool("json", false, "JSON output")
	repairFTS := fs.Bool("repair-fts", false, "rebuild FTS tables from corpus rows before reporting")
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
	if *repairFTS {
		if err := corpus.ReconcileFTS(store); err != nil {
			return err
		}
	}
	report, err := corpus.Verify(store)
	if err != nil {
		return err
	}
	if *jsonOut {
		return writeJSON(stdout, struct {
			corpus.VerifyReport
			RepairedFTS bool `json:"repaired_fts"`
		}{VerifyReport: report, RepairedFTS: *repairFTS})
	}
	fmt.Fprintf(stdout, "root=%s problems=%d repaired_fts=%v\n", report.Root, len(report.Problems), *repairFTS)
	for _, p := range report.Problems {
		if p.Count > 0 {
			fmt.Fprintf(stdout, "%s\t%d\t%s\n", p.Code, p.Count, p.Message)
		} else {
			fmt.Fprintf(stdout, "%s\t%s\n", p.Code, p.Message)
		}
	}
	return nil
}

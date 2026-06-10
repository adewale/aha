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
	var repairReport corpus.FTSRepairReport
	if *repairFTS {
		var err error
		repairReport, err = corpus.ReconcileFTSWithReport(store)
		if err != nil {
			return err
		}
	}
	report, err := corpus.Verify(store)
	if err != nil {
		return err
	}
	if *jsonOut {
		var repair *corpus.FTSRepairReport
		if *repairFTS {
			repair = &repairReport
		}
		return writeJSON(stdout, struct {
			corpus.VerifyReport
			RepairedFTS bool                    `json:"repaired_fts"`
			FTSRepair   *corpus.FTSRepairReport `json:"fts_repair,omitempty"`
		}{VerifyReport: report, RepairedFTS: *repairFTS, FTSRepair: repair})
	}
	fmt.Fprintf(stdout, "root=%s problems=%d repaired_fts=%v messages=%d artifacts=%d fts_messages=%d fts_artifacts=%d snapshots=%d\n", report.Root, len(report.Problems), *repairFTS, report.Stats.Messages, report.Stats.Artifacts, report.Stats.FTSMessages, report.Stats.FTSArtifacts, report.Stats.Snapshots)
	if *repairFTS {
		fmt.Fprintf(stdout, "fts_repair deleted_messages=%d inserted_messages=%d deleted_artifacts=%d inserted_artifacts=%d\n", repairReport.DeletedMessageRows, repairReport.InsertedMessageRows, repairReport.DeletedArtifactRows, repairReport.InsertedArtifactRows)
	}
	for _, p := range report.Problems {
		if p.Count > 0 {
			fmt.Fprintf(stdout, "%s\t%d\t%s\n", p.Code, p.Count, p.Message)
		} else {
			fmt.Fprintf(stdout, "%s\t%s\n", p.Code, p.Message)
		}
	}
	return nil
}

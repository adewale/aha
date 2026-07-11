package cli

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/adewale/aha/internal/corpus"
	ahaprogress "github.com/adewale/aha/internal/progress"
)

func cmdVerify(args []string, stdout, stderr io.Writer) error {
	return runVerifyContext(context.Background(), args, stdout, stderr)
}

func runVerifyContext(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cf := registerCorpusFlags(fs)
	jsonOut := fs.Bool("json", false, "JSON output")
	repairFTS := fs.Bool("repair-fts", false, "rebuild FTS tables from corpus rows before reporting")
	progressSetting := fs.String("progress", "auto", "progress mode: auto, off, plain, tty, or json (stderr only)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := cf.loadConfig()
	if err != nil {
		return err
	}
	progress, err := newProgressSession(stderr, *progressSetting, *jsonOut)
	if err != nil {
		return err
	}
	defer progress.Close()
	totalSteps := uint64(1)
	if *repairFTS {
		totalSteps = 2
	}
	verifyTotal := ahaprogress.KnownTotal(totalSteps)
	progress.Tracker.Start(ahaprogress.PhaseVerify, verifyTotal, ahaprogress.UnitSteps)
	store, err := openCorpusForCommand(cfg, false)
	if err != nil {
		if ctx.Err() != nil {
			progress.Tracker.Cancel(ahaprogress.PhaseVerify, 0, verifyTotal, ahaprogress.UnitSteps)
		} else {
			progress.Tracker.Fail(ahaprogress.PhaseVerify, 0, verifyTotal, ahaprogress.UnitSteps)
		}
		return err
	}
	defer store.Close()
	var repairReport corpus.FTSRepairReport
	if *repairFTS {
		if err := ctx.Err(); err != nil {
			progress.Tracker.Cancel(ahaprogress.PhaseVerify, 0, verifyTotal, ahaprogress.UnitSteps)
			return err
		}
		var err error
		repairReport, err = corpus.ReconcileFTSWithReportContext(ctx, store)
		if err != nil {
			if ctx.Err() != nil {
				progress.Tracker.Cancel(ahaprogress.PhaseVerify, 0, verifyTotal, ahaprogress.UnitSteps)
			} else {
				progress.Tracker.Fail(ahaprogress.PhaseVerify, 0, verifyTotal, ahaprogress.UnitSteps)
			}
			return err
		}
		progress.Tracker.Advance(ahaprogress.PhaseVerify, 1, verifyTotal, ahaprogress.UnitSteps)
	}
	if err := ctx.Err(); err != nil {
		progress.Tracker.Cancel(ahaprogress.PhaseVerify, totalSteps-1, verifyTotal, ahaprogress.UnitSteps)
		return err
	}
	report, err := corpus.VerifyContext(ctx, store)
	if err != nil {
		completedSteps := totalSteps - 1
		if ctx.Err() != nil {
			progress.Tracker.Cancel(ahaprogress.PhaseVerify, completedSteps, verifyTotal, ahaprogress.UnitSteps)
		} else {
			progress.Tracker.Fail(ahaprogress.PhaseVerify, completedSteps, verifyTotal, ahaprogress.UnitSteps)
		}
		return err
	}
	progress.Tracker.Complete(ahaprogress.PhaseVerify, totalSteps, verifyTotal, ahaprogress.UnitSteps)
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

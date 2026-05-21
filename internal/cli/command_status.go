package cli

import (
	"flag"
	"io"

	"github.com/adewale/aha/internal/corpus"
)

func cmdStatus(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
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
	stats := corpus.Status(store.DB, store.Root)
	stats["next"] = statusNext(stats)
	if *jsonOut {
		return writeJSON(stdout, stats)
	}
	return renderMap(stdout, stats)
}

func statusNext(stats map[string]any) []string {
	sessions, _ := stats["sessions"].(int)
	if sessions == 0 {
		return []string{"aha refresh", "aha ingest <bundle.tar.zst>", "aha doctor"}
	}
	conflicts, _ := stats["conflicts"].(int)
	next := []string{"aha search <query>", "aha read <ref>"}
	if conflicts > 0 {
		next = append(next, "aha conflicts")
	}
	return next
}

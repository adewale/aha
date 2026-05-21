package cli

import (
	"errors"
	"flag"
	"io"
	"strings"

	"github.com/adewale/aha/internal/corpus"
)

func cmdRead(args []string, stdout, stderr io.Writer) error {
	args = reorderReadArgs(args)
	fs := flag.NewFlagSet("read", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cf := registerCorpusFlags(fs)
	session := fs.String("session", "", "session key/id")
	entry := fs.String("entry", "", "entry id")
	before := fs.Int("before", 3, "entries before")
	after := fs.Int("after", 5, "entries after")
	jsonOut := fs.Bool("json", false, "JSON output")
	mdOut := fs.Bool("md", false, "Markdown output")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *session == "" && fs.NArg() > 0 {
		*session = fs.Arg(0)
	}
	if *session == "" {
		return errors.New("--session required")
	}
	if strings.Contains(*session, "#") && *entry == "" {
		parts := strings.SplitN(*session, "#", 2)
		*session, *entry = parts[0], parts[1]
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
	entries, err := corpus.ReadContext(store.DB, *session, *entry, *before, *after)
	if err != nil {
		return err
	}
	mode := renderHuman
	if *jsonOut {
		mode = renderJSON
	} else if *mdOut {
		mode = renderMD
	}
	return renderReadEntries(stdout, entries, mode)
}

func reorderReadArgs(args []string) []string {
	valueFlags := map[string]bool{"--corpus": true, "--repo": true, "--config": true, "--session": true, "--entry": true, "--before": true, "--after": true}
	boolFlags := map[string]bool{"--json": true, "--md": true}
	var flags []string
	var pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		name := a
		if eq := strings.IndexByte(a, '='); eq >= 0 {
			name = a[:eq]
		}
		if boolFlags[name] || strings.Contains(a, "=") && valueFlags[name] {
			flags = append(flags, a)
			continue
		}
		if valueFlags[name] && i+1 < len(args) {
			flags = append(flags, a, args[i+1])
			i++
			continue
		}
		pos = append(pos, a)
	}
	return append(flags, pos...)
}

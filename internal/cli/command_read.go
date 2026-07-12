package cli

import (
	"errors"
	"io"

	"github.com/adewale/aha/internal/corpus"
	"github.com/adewale/aha/internal/model"
)

func cmdShow(args []string, stdout, stderr io.Writer) error {
	args = reorderShowArgs(args)
	pf, err := parseFlagSpecs("show", args, stderr, showFlagSpecs)
	if err != nil {
		return err
	}
	cf := workspaceFlags{workspaceDir: stringPtr(pf.String("workspace")), config: stringPtr(pf.String("config"))}
	session := pf.String("session")
	entry := pf.String("entry")
	branch := pf.String("branch")
	live := pf.String("live")
	if pf.NArg() > 1 {
		return errors.New("show accepts exactly one positional REF")
	}
	if (pf.NArg() == 1) == (session != "") {
		return errors.New("show requires exactly one of positional REF or --session")
	}
	if err := requireAtMostOneLeafMode(branch, live, entry); err != nil {
		return err
	}
	if err := requireAtMostOneOutputMode(pf.Bool("json"), pf.Bool("md")); err != nil {
		return err
	}
	var ref model.Ref
	useRef := pf.NArg() == 1
	if useRef {
		if entry != "" || branch != "" || live != "" {
			return errors.New("positional REF cannot be combined with --entry, --branch, or --live")
		}
		parsedRef, err := model.ParseRef(pf.Arg(0))
		if err != nil {
			return err
		}
		ref = parsedRef
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
	var entries []corpus.ReadEntry
	switch {
	case branch != "":
		entries, err = corpus.ReadBranch(store.DB, session, branch)
	case live != "":
		entries, err = corpus.LiveContext(store.DB, session, live)
	case useRef:
		entries, err = corpus.ReadCanonical(store.DB, ref, pf.Int("before"), pf.Int("after"))
	default:
		entries, err = corpus.ReadContext(store.DB, session, entry, pf.Int("before"), pf.Int("after"))
	}
	if err != nil {
		return err
	}
	mode := renderHuman
	if pf.Bool("json") {
		mode = renderJSON
	} else if pf.Bool("md") {
		mode = renderMD
	}
	return renderReadEntries(stdout, entries, mode)
}

// requireAtMostOneLeafMode rejects combinations of the leaf-oriented
// read selectors. --branch and --live both name a leaf entry to walk
// back from and are mutually exclusive with each other and with the
// window-oriented --entry.
func requireAtMostOneLeafMode(branch, live, entry string) error {
	set := 0
	for _, v := range []string{branch, live, entry} {
		if v != "" {
			set++
		}
	}
	if set > 1 {
		return errors.New("--branch, --live, and --entry are mutually exclusive")
	}
	return nil
}

func reorderShowArgs(args []string) []string {
	return reorderArgsBySpec(args, showFlagSpecs)
}

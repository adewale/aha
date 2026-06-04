package cli

import (
	"errors"
	"io"
	"strings"

	"github.com/adewale/aha/internal/corpus"
	"github.com/adewale/aha/internal/model"
)

func cmdRead(args []string, stdout, stderr io.Writer) error {
	args = reorderReadArgs(args)
	pf, err := parseFlagSpecs("read", args, stderr, readFlagSpecs)
	if err != nil {
		return err
	}
	cf := corpusFlags{corpusDir: stringPtr(pf.String("corpus")), repoDir: stringPtr(pf.String("repo")), config: stringPtr(pf.String("config"))}
	session := pf.String("session")
	entry := pf.String("entry")
	branch := pf.String("branch")
	live := pf.String("live")
	if session == "" && pf.NArg() > 0 {
		session = pf.Arg(0)
	}
	if session == "" {
		return errors.New("--session required")
	}
	if err := requireAtMostOneLeafMode(branch, live, entry); err != nil {
		return err
	}
	if err := requireAtMostOneOutputMode(pf.Bool("json"), pf.Bool("md")); err != nil {
		return err
	}
	var ref model.Ref
	useRef := false
	if entry == "" && branch == "" && live == "" && looksLikeRef(session) {
		parsedRef, err := model.ParseRef(session)
		if err != nil {
			return err
		}
		ref = parsedRef
		useRef = true
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

func looksLikeRef(s string) bool {
	return strings.HasPrefix(s, "msg:v1:") || strings.HasPrefix(s, "session:v1:") || strings.HasPrefix(s, "artifact:v1:") || strings.Contains(s, "#") || strings.HasPrefix(s, "artifact:")
}

func reorderReadArgs(args []string) []string {
	return reorderArgsBySpec(args, readFlagSpecs)
}

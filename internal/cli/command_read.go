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
	if session == "" && pf.NArg() > 0 {
		session = pf.Arg(0)
	}
	if session == "" {
		return errors.New("--session required")
	}
	if err := requireAtMostOneOutputMode(pf.Bool("json"), pf.Bool("md")); err != nil {
		return err
	}
	var ref model.HitRef
	useRef := false
	if strings.Contains(session, "#") && entry == "" {
		parsed, err := model.ParseHitRef(session)
		if err != nil {
			return err
		}
		ref = parsed
		useRef = true
		session, entry = ref.SessionKey, ref.EntryID
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
	if useRef {
		entries, err = corpus.ReadRef(store.DB, ref, pf.Int("before"), pf.Int("after"))
	} else {
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

func reorderReadArgs(args []string) []string {
	return reorderArgsBySpec(args, readFlagSpecs)
}

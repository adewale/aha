package cli

import (
	"flag"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

type FlagSpec struct {
	Name         string `json:"name"`
	Kind         string `json:"kind"`
	Default      string `json:"default,omitempty"`
	Help         string `json:"help"`
	PostPosition bool   `json:"post_position,omitempty"`
}

const (
	flagString = "string"
	flagBool   = "bool"
	flagInt    = "int"
)

var corpusFlagSpecs = []FlagSpec{
	{Name: "corpus", Kind: flagString, Help: "corpus dir", PostPosition: true},
	{Name: "repo", Kind: flagString, Help: "repo/corpus dir", PostPosition: true},
	{Name: "config", Kind: flagString, Help: "config path", PostPosition: true},
}

var searchFlagSpecs = appendFlagSpecs(corpusFlagSpecs,
	FlagSpec{Name: "source", Kind: flagString, Help: "source filter", PostPosition: true},
	FlagSpec{Name: "machine", Kind: flagString, Help: "machine filter", PostPosition: true},
	FlagSpec{Name: "role", Kind: flagString, Help: "role filter", PostPosition: true},
	FlagSpec{Name: "after", Kind: flagString, Help: "after date", PostPosition: true},
	FlagSpec{Name: "before", Kind: flagString, Help: "before date", PostPosition: true},
	FlagSpec{Name: "path", Kind: flagString, Help: "path/cwd contains filter (convenient but not indexed)", PostPosition: true},
	FlagSpec{Name: "path-token", Kind: flagString, Help: "indexed path segment token filter", PostPosition: true},
	FlagSpec{Name: "project", Kind: flagString, Help: "exact project key filter", PostPosition: true},
	FlagSpec{Name: "json", Kind: flagBool, Help: "JSON output", PostPosition: true},
	FlagSpec{Name: "refs", Kind: flagBool, Help: "refs output", PostPosition: true},
	FlagSpec{Name: "files", Kind: flagBool, Help: "files output", PostPosition: true},
	FlagSpec{Name: "md", Kind: flagBool, Help: "Markdown output", PostPosition: true},
	FlagSpec{Name: "limit", Kind: flagInt, Default: "20", Help: "limit (capped at 200)", PostPosition: true},
)

var readFlagSpecs = appendFlagSpecs(corpusFlagSpecs,
	FlagSpec{Name: "session", Kind: flagString, Help: "session key/id", PostPosition: true},
	FlagSpec{Name: "entry", Kind: flagString, Help: "entry id", PostPosition: true},
	FlagSpec{Name: "branch", Kind: flagString, Help: "walk parent_id from this leaf entry back to root (Pi tree-walking read)", PostPosition: true},
	FlagSpec{Name: "live", Kind: flagString, Help: "reconstruct live context from this leaf: branch walk with Pi compaction collapse and non-participating entries filtered", PostPosition: true},
	FlagSpec{Name: "before", Kind: flagInt, Default: "3", Help: "entries before", PostPosition: true},
	FlagSpec{Name: "after", Kind: flagInt, Default: "5", Help: "entries after", PostPosition: true},
	FlagSpec{Name: "json", Kind: flagBool, Help: "JSON output", PostPosition: true},
	FlagSpec{Name: "md", Kind: flagBool, Help: "Markdown output", PostPosition: true},
)

func appendFlagSpecs(base []FlagSpec, rest ...FlagSpec) []FlagSpec {
	out := append([]FlagSpec(nil), base...)
	out = append(out, rest...)
	return out
}

func flagNames(specs []FlagSpec) []string {
	out := make([]string, len(specs))
	for i, spec := range specs {
		out[i] = "--" + spec.Name
	}
	sort.Strings(out)
	return out
}

type parsedFlags struct {
	fs      *flag.FlagSet
	strings map[string]*string
	bools   map[string]*bool
	ints    map[string]*int
}

func parseFlagSpecs(name string, args []string, stderr io.Writer, specs []FlagSpec) (parsedFlags, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	parsed := parsedFlags{fs: fs, strings: map[string]*string{}, bools: map[string]*bool{}, ints: map[string]*int{}}
	for _, spec := range specs {
		switch spec.Kind {
		case flagString:
			parsed.strings[spec.Name] = fs.String(spec.Name, spec.Default, spec.Help)
		case flagBool:
			def, err := strconv.ParseBool(defaultString(spec.Default, "false"))
			if err != nil {
				return parsedFlags{}, fmt.Errorf("invalid bool default for --%s: %w", spec.Name, err)
			}
			parsed.bools[spec.Name] = fs.Bool(spec.Name, def, spec.Help)
		case flagInt:
			def, err := strconv.Atoi(defaultString(spec.Default, "0"))
			if err != nil {
				return parsedFlags{}, fmt.Errorf("invalid int default for --%s: %w", spec.Name, err)
			}
			parsed.ints[spec.Name] = fs.Int(spec.Name, def, spec.Help)
		default:
			return parsedFlags{}, fmt.Errorf("unknown flag kind %q for --%s", spec.Kind, spec.Name)
		}
	}
	if err := fs.Parse(args); err != nil {
		return parsedFlags{}, err
	}
	return parsed, nil
}

func (p parsedFlags) String(name string) string { return *p.strings[name] }
func (p parsedFlags) Bool(name string) bool     { return *p.bools[name] }
func (p parsedFlags) Int(name string) int       { return *p.ints[name] }
func (p parsedFlags) NArg() int                 { return p.fs.NArg() }
func (p parsedFlags) Args() []string            { return p.fs.Args() }
func (p parsedFlags) Arg(i int) string          { return p.fs.Arg(i) }

func stringPtr(value string) *string { return &value }

func defaultString(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func reorderArgsBySpec(args []string, specs []FlagSpec) []string {
	literal := []string(nil)
	for i, a := range args {
		if a == "--" {
			literal = args[i+1:]
			args = args[:i]
			break
		}
	}
	valueFlags := map[string]bool{}
	boolFlags := map[string]bool{}
	for _, spec := range specs {
		if !spec.PostPosition {
			continue
		}
		name := "--" + spec.Name
		if spec.Kind == flagBool {
			boolFlags[name] = true
		} else {
			valueFlags[name] = true
		}
	}
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
	out := append(flags, pos...)
	if literal != nil {
		out = append(out, "--")
		out = append(out, literal...)
	}
	return out
}

package cli_test

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/adewale/aha/internal/cli"
)

func TestCommandFlagMetadataMatchesFlagSets(t *testing.T) {
	actual := parseCommandFlags(t)
	registry := cli.Registry()
	for name, cmd := range registry {
		want := normalizeFlagNames(cmd.Flags)
		if len(cmd.FlagSpecs) > 0 {
			fromSpecs := normalizeFlagNames(flagNamesFromSpecsForTest(cmd.FlagSpecs))
			if strings.Join(fromSpecs, ",") != strings.Join(want, ",") {
				t.Fatalf("command %s flag metadata drift\nflag specs: %v\nregistry flags: %v", name, fromSpecs, want)
			}
			continue
		}
		flags, ok := actual[name]
		if !ok {
			t.Fatalf("registry command %s has no parsed flag contract", name)
		}
		if strings.Join(flags, ",") != strings.Join(want, ",") {
			t.Fatalf("command %s flag metadata drift\nactual flagset: %v\nregistry flags: %v", name, flags, want)
		}
	}
	for name := range actual {
		if _, ok := registry[name]; !ok {
			t.Fatalf("parsed flags for unregistered command %s", name)
		}
	}
}

func TestFlagSpecsDriveHelpText(t *testing.T) {
	for _, name := range []string{"search", "read"} {
		cmd := cli.Registry()[name]
		if len(cmd.FlagSpecs) == 0 {
			t.Fatalf("%s has no flag specs", name)
		}
		var out bytes.Buffer
		if err := cli.Run([]string{name, "--help"}, &out, &out); err != nil {
			t.Fatalf("%s --help: %v", name, err)
		}
		help := out.String()
		for _, spec := range cmd.FlagSpecs {
			if !strings.Contains(help, "-"+spec.Name) || !strings.Contains(help, spec.Help) {
				t.Fatalf("%s help missing --%s (%q):\n%s", name, spec.Name, spec.Help, help)
			}
			if spec.Default != "" && !strings.Contains(help, "default "+spec.Default) {
				t.Fatalf("%s help missing default for --%s=%s:\n%s", name, spec.Name, spec.Default, help)
			}
		}
	}
}

func parseCommandFlags(t *testing.T) map[string][]string {
	t.Helper()
	files, err := filepath.Glob("command_*.go")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]map[string]bool{}
	funcFlags := map[string]map[string]bool{}
	funcCalls := map[string][]string{}
	for _, file := range files {
		f, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			funcFlags[fn.Name.Name] = map[string]bool{}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				if ident, ok := call.Fun.(*ast.Ident); ok {
					funcCalls[fn.Name.Name] = append(funcCalls[fn.Name.Name], ident.Name)
					if ident.Name == "registerCorpusFlags" {
						for _, flag := range []string{"config", "corpus", "repo"} {
							funcFlags[fn.Name.Name][flag] = true
						}
						return true
					}
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				argIndex := 0
				switch sel.Sel.Name {
				case "String", "Bool", "Int", "Int64", "Float64", "Uint", "Uint64", "Duration":
					argIndex = 0
				case "Var":
					argIndex = 1
				default:
					return true
				}
				if len(call.Args) <= argIndex {
					return true
				}
				lit, ok := call.Args[argIndex].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				flag, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatal(err)
				}
				funcFlags[fn.Name.Name][flag] = true
				return true
			})
		}
	}
	for fn := range funcFlags {
		if !strings.HasPrefix(fn, "cmd") {
			continue
		}
		name := commandNameFromFunc(fn)
		out[name] = collectFuncFlags(fn, funcFlags, funcCalls, map[string]bool{})
	}
	final := map[string][]string{}
	for name, set := range out {
		for flag := range set {
			final[name] = append(final[name], flag)
		}
		sort.Strings(final[name])
	}
	return final
}

func collectFuncFlags(fn string, flags map[string]map[string]bool, calls map[string][]string, seen map[string]bool) map[string]bool {
	out := map[string]bool{}
	if seen[fn] {
		return out
	}
	seen[fn] = true
	for flag := range flags[fn] {
		out[flag] = true
	}
	for _, callee := range calls[fn] {
		for flag := range collectFuncFlags(callee, flags, calls, seen) {
			out[flag] = true
		}
	}
	return out
}

func commandNameFromFunc(fn string) string {
	name := strings.TrimPrefix(fn, "cmd")
	var out []rune
	for i, r := range name {
		if i > 0 && r >= 'A' && r <= 'Z' {
			out = append(out, '-')
		}
		out = append(out, r)
	}
	return strings.ToLower(string(out))
}

func flagNamesFromSpecsForTest(specs []cli.FlagSpec) []string {
	out := make([]string, len(specs))
	for i, spec := range specs {
		out[i] = "--" + spec.Name
	}
	return out
}

func normalizeFlagNames(flags []string) []string {
	out := make([]string, len(flags))
	for i, flag := range flags {
		out[i] = strings.TrimPrefix(flag, "--")
	}
	sort.Strings(out)
	return out
}

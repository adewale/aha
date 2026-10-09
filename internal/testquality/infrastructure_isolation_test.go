package testquality_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

func readProjectFile(t *testing.T, parts ...string) string {
	t.Helper()
	path := filepath.Join(append([]string{"..", ".."}, parts...)...)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestVerifyUsesPrivateRunWorkspaceAndNeverInstallsDependencies(t *testing.T) {
	body := readProjectFile(t, "scripts", "verify.sh")
	for _, forbidden := range []string{"/tmp/aha", "/tmp/aha-ref-mcp", "npm ci", "npm install"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("verify.sh contains unsafe shared/mutating behaviour %q", forbidden)
		}
	}
	for _, required := range []string{"mktemp -d", "AHA_MCP_CONFORMANCE_ROOT", "AHA_MCP_CONFORMANCE_TOKEN", "cross_compile"} {
		if !strings.Contains(body, required) {
			t.Fatalf("verify.sh missing isolation/cross-compile contract %q", required)
		}
	}
}

// seedOnlyFuzzTargets lists fuzz targets that deliberately run only their seed
// corpus (as ordinary tests via go test ./...) and are never actively fuzzed by
// scripts/verify.sh fuzz. Every entry needs a reason. It is empty: every target
// is fuzzed. Prefer adding a target to fuzz() over adding it here.
var seedOnlyFuzzTargets = map[string]string{}

// fuzzCommand is one `go test <pkg> ... -fuzz=<target>` line from verify.sh.
type fuzzCommand struct {
	pkg    string // e.g. "internal/model"
	target string // e.g. "FuzzRefParseFormat"
}

var (
	verifyFuzzCommandPattern = regexp.MustCompile(`go test \./(\S+) [^\n]*-fuzz=\^?(Fuzz[[:alnum:]_]+)\$?`)
	fuzzFuncPattern          = regexp.MustCompile(`(?m)^func (Fuzz[[:alnum:]_]+)\(`)
)

// fuzzListDrift compares the fuzz commands in verify.sh with the fuzz targets
// defined in the repository (package dir -> target names) in both directions:
// every command must name a target defined in that package, and every defined
// target must be fuzzed or explicitly listed as seed-only.
func fuzzListDrift(commands []fuzzCommand, defined map[string][]string, seedOnly map[string]string) []string {
	var problems []string
	definedIn := map[string]string{}
	for pkg, targets := range defined {
		for _, target := range targets {
			definedIn[target] = pkg
		}
	}
	fuzzed := map[string]bool{}
	for _, cmd := range commands {
		fuzzed[cmd.target] = true
		pkg, ok := definedIn[cmd.target]
		switch {
		case !ok:
			problems = append(problems, fmt.Sprintf("verify.sh fuzzes %s, which is not defined anywhere", cmd.target))
		case pkg != cmd.pkg:
			problems = append(problems, fmt.Sprintf("verify.sh fuzzes %s in ./%s, but it is defined in ./%s (go test would fuzz nothing and pass)", cmd.target, cmd.pkg, pkg))
		}
	}
	for target, pkg := range definedIn {
		_, seedOnlyTarget := seedOnly[target]
		switch {
		case fuzzed[target] && seedOnlyTarget:
			problems = append(problems, fmt.Sprintf("%s is both fuzzed by verify.sh and listed as seed-only", target))
		case !fuzzed[target] && !seedOnlyTarget:
			problems = append(problems, fmt.Sprintf("./%s defines %s, but verify.sh fuzz() never fuzzes it; add it there or to seedOnlyFuzzTargets with a reason", pkg, target))
		}
	}
	for target, reason := range seedOnly {
		if _, ok := definedIn[target]; !ok {
			problems = append(problems, fmt.Sprintf("seedOnlyFuzzTargets lists %s, which is not defined anywhere", target))
		}
		if strings.TrimSpace(reason) == "" {
			problems = append(problems, fmt.Sprintf("seedOnlyFuzzTargets entry %s has no reason", target))
		}
	}
	sort.Strings(problems)
	return problems
}

func verifyFuzzCommands(t *testing.T) []fuzzCommand {
	t.Helper()
	verify := readProjectFile(t, "scripts", "verify.sh")
	var commands []fuzzCommand
	for _, match := range verifyFuzzCommandPattern.FindAllStringSubmatch(verify, -1) {
		commands = append(commands, fuzzCommand{pkg: match[1], target: match[2]})
	}
	return commands
}

func definedFuzzTargets(t *testing.T) map[string][]string {
	t.Helper()
	root := filepath.Join("..", "..")
	defined := map[string][]string{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, filepath.Dir(path))
		if err != nil {
			return err
		}
		for _, match := range fuzzFuncPattern.FindAllStringSubmatch(string(body), -1) {
			defined[filepath.ToSlash(rel)] = append(defined[filepath.ToSlash(rel)], match[1])
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return defined
}

func TestVerifyFuzzListMatchesFuzzTargetsInBothDirections(t *testing.T) {
	commands := verifyFuzzCommands(t)
	if len(commands) == 0 {
		t.Fatal("verify.sh does not run any fuzz targets")
	}
	defined := definedFuzzTargets(t)
	if len(defined) == 0 {
		t.Fatal("found no fuzz targets in the repository; the scan is broken")
	}
	if problems := fuzzListDrift(commands, defined, seedOnlyFuzzTargets); len(problems) > 0 {
		t.Fatalf("verify.sh fuzz list has drifted from the fuzz targets:\n  %s", strings.Join(problems, "\n  "))
	}
}

// TestFuzzListDriftDetectsBothDirections gives the drift guard teeth: each
// case is a drift that the guard above must report.
func TestFuzzListDriftDetectsBothDirections(t *testing.T) {
	defined := map[string][]string{
		"internal/a": {"FuzzA", "FuzzA2"},
		"internal/b": {"FuzzB"},
	}
	all := []fuzzCommand{{"internal/a", "FuzzA"}, {"internal/a", "FuzzA2"}, {"internal/b", "FuzzB"}}
	cases := []struct {
		name     string
		commands []fuzzCommand
		seedOnly map[string]string
		want     string // substring of the single expected problem; "" means no problems
	}{
		{"in sync", all, nil, ""},
		{"defined but never fuzzed", all[:2], nil, "./internal/b defines FuzzB, but verify.sh fuzz() never fuzzes it"},
		{"seed-only with a reason", all[:2], map[string]string{"FuzzB": "slow"}, ""},
		{"fuzzed target no longer exists", append(all[:3:3], fuzzCommand{"internal/b", "FuzzGone"}), nil, "verify.sh fuzzes FuzzGone, which is not defined anywhere"},
		{"fuzzed in the wrong package", []fuzzCommand{{"internal/a", "FuzzA"}, {"internal/a", "FuzzA2"}, {"internal/a", "FuzzB"}}, nil, "verify.sh fuzzes FuzzB in ./internal/a, but it is defined in ./internal/b"},
		{"both fuzzed and seed-only", all, map[string]string{"FuzzA": "slow"}, "FuzzA is both fuzzed by verify.sh and listed as seed-only"},
		{"stale seed-only entry", all, map[string]string{"FuzzGone": "slow"}, "seedOnlyFuzzTargets lists FuzzGone, which is not defined anywhere"},
		{"seed-only without a reason", all[:2], map[string]string{"FuzzB": " "}, "seedOnlyFuzzTargets entry FuzzB has no reason"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			problems := fuzzListDrift(tc.commands, defined, tc.seedOnly)
			if tc.want == "" {
				if len(problems) != 0 {
					t.Fatalf("problems = %q, want none", problems)
				}
				return
			}
			if len(problems) != 1 || !strings.Contains(problems[0], tc.want) {
				t.Fatalf("problems = %q, want exactly one containing %q", problems, tc.want)
			}
		})
	}
}

// TestVerifyFuzzCommandPatternParsesVerifyScript pins the parser to the real
// command shape in verify.sh, so a reformatted fuzz() cannot silently yield an
// empty or partial command list.
func TestVerifyFuzzCommandPatternParsesVerifyScript(t *testing.T) {
	verify := readProjectFile(t, "scripts", "verify.sh")
	if got, want := len(verifyFuzzCommands(t)), strings.Count(verify, "-fuzz="); got != want {
		t.Fatalf("parsed %d fuzz commands but verify.sh contains %d -fuzz= flags", got, want)
	}
}

// TestCIRequiresEveryMCPConformanceLeg keeps the cross-SDK conformance legs
// that have found real bugs (Python and TS) mandatory in CI: CI must install
// their pinned dependencies and set AHA_MCP_REQUIRE_ALL_LEGS=1, so a leg that
// cannot run fails the build instead of skipping with exit 0.
func TestCIRequiresEveryMCPConformanceLeg(t *testing.T) {
	ci := readProjectFile(t, ".github", "workflows", "ci.yml")
	for _, required := range []string{
		"npm ci --prefix clients/typescript",
		"npm ci --prefix scripts/mcp-conformance",
		"pip install -r scripts/mcp-conformance/requirements.txt",
		`AHA_MCP_REQUIRE_ALL_LEGS: "1"`,
		"scripts/verify.sh ci",
	} {
		if !strings.Contains(ci, required) {
			t.Errorf("ci.yml missing %q", required)
		}
	}
	requirements := readProjectFile(t, "scripts", "mcp-conformance", "requirements.txt")
	if !regexp.MustCompile(`(?m)^mcp==[0-9][^\s]*$`).MatchString(requirements) {
		t.Errorf("scripts/mcp-conformance/requirements.txt must pin the Python mcp SDK with ==; got:\n%s", requirements)
	}
	verify := readProjectFile(t, "scripts", "verify.sh")
	if !strings.Contains(verify, "AHA_MCP_REQUIRE_ALL_LEGS") {
		t.Error("verify.sh no longer honours AHA_MCP_REQUIRE_ALL_LEGS")
	}
}

// ciProfileSteps returns the steps of full() in verify.sh (what CI runs), with
// the `run` logging prefix removed.
func ciProfileSteps(t *testing.T) []string {
	t.Helper()
	verify := readProjectFile(t, "scripts", "verify.sh")
	body := regexp.MustCompile(`(?s)\nfull\(\) \{\n(.*?)\n\}`).FindStringSubmatch(verify)
	if body == nil {
		t.Fatal("verify.sh has no full() function")
	}
	var steps []string
	for _, line := range strings.Split(body[1], "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		steps = append(steps, strings.TrimPrefix(line, "run "))
	}
	return steps
}

// documentedCIProfileSteps returns the leading `code` span of each bullet in
// the "## CI profile" section of docs/verification.md.
func documentedCIProfileSteps(t *testing.T) []string {
	t.Helper()
	doc := readProjectFile(t, "docs", "verification.md")
	section := regexp.MustCompile(`(?s)\n## CI profile\n(.*?)(\n## |$)`).FindStringSubmatch(doc)
	if section == nil {
		t.Fatal("docs/verification.md has no \"## CI profile\" section")
	}
	var steps []string
	for _, match := range regexp.MustCompile("(?m)^- `([^`]+)`").FindAllStringSubmatch(section[1], -1) {
		steps = append(steps, match[1])
	}
	return steps
}

// TestVerificationDocListsEveryCIProfileStep keeps docs/verification.md's CI
// profile identical to what `scripts/verify.sh ci` actually runs, in order.
func TestVerificationDocListsEveryCIProfileStep(t *testing.T) {
	script, doc := ciProfileSteps(t), documentedCIProfileSteps(t)
	if len(script) == 0 {
		t.Fatal("parsed no steps from verify.sh full(); the parser is broken")
	}
	if strings.Join(script, "\n") != strings.Join(doc, "\n") {
		t.Fatalf("docs/verification.md CI profile has drifted from verify.sh full().\nverify.sh full():\n  %s\ndocs/verification.md:\n  %s",
			strings.Join(script, "\n  "), strings.Join(doc, "\n  "))
	}
}

func TestConformanceClientsRequireHarnessAttestationAndNoTmpFallback(t *testing.T) {
	direct := [][]string{
		{"scripts", "mcp-conformance", "client_against_aha.py"},
		{"internal", "mcp", "conformance", "go_sdk_test.go"},
	}
	for _, parts := range direct {
		body := readProjectFile(t, parts...)
		name := filepath.Join(parts...)
		for _, required := range []string{"AHA_MCP_CONFORMANCE_ROOT", "AHA_MCP_CONFORMANCE_TOKEN"} {
			if !strings.Contains(body, required) {
				t.Fatalf("%s missing %s", name, required)
			}
		}
	}
	for _, parts := range [][]string{{"scripts", "mcp-conformance", "client_against_aha.ts"}, {"scripts", "mcp-conformance", "codemode_workflow.ts"}} {
		body := readProjectFile(t, parts...)
		if !strings.Contains(body, "assertAttestedConformance") {
			t.Fatalf("%s does not enforce shared TS attestation", filepath.Join(parts...))
		}
	}
	attestation := readProjectFile(t, "scripts", "mcp-conformance", "attestation.ts")
	for _, required := range []string{"AHA_MCP_CONFORMANCE_ROOT", "AHA_MCP_CONFORMANCE_TOKEN"} {
		if !strings.Contains(attestation, required) {
			t.Fatalf("TS attestation missing %s", required)
		}
	}
	for _, parts := range append(direct, []string{"scripts", "mcp-conformance", "attestation.ts"}) {
		body := readProjectFile(t, parts...)
		if strings.Contains(body, `"/tmp/aha"`) || strings.Contains(body, `?? "/tmp/aha"`) {
			t.Fatalf("%s can fall back to shared /tmp binary", filepath.Join(parts...))
		}
	}
}

func TestGeneratorsRequireExplicitOutputAndDocsTestsStayReadOnly(t *testing.T) {
	for _, parts := range [][]string{{"cmd", "aha-gen-ts", "main.go"}, {"cmd", "aha-gen-docs", "main.go"}} {
		body := readProjectFile(t, parts...)
		if !strings.Contains(body, `flag.String("out", ""`) || !strings.Contains(body, "-out is required") {
			t.Fatalf("%s does not require an explicit output", filepath.Join(parts...))
		}
	}
	if _, err := os.Stat(filepath.Join("..", "cli", "gen_docs_helper_test.go")); !os.IsNotExist(err) {
		t.Fatalf("ordinary test tree still contains ambient docs writer: %v", err)
	}
	makefile := readProjectFile(t, "Makefile")
	for _, required := range []string{"gen-docs:", "./cmd/aha-gen-docs -out docs/commands.md", "./cmd/aha-gen-ts -out clients/typescript/aha-mcp.ts"} {
		if !strings.Contains(makefile, required) {
			t.Fatalf("Makefile missing explicit generator invocation %q", required)
		}
	}
}

// TestBundleRoundTripFuzzIsBoundedByIterations keeps FuzzWalkBundleRoundTrip,
// which does a real compressed-file round trip per input, bounded by an
// iteration count rather than wall time. With a wall-time budget a slow CI
// runner can still be inside an input when the budget expires, and go test
// then fails teardown with "context deadline exceeded" even though no input
// failed.
func TestBundleRoundTripFuzzIsBoundedByIterations(t *testing.T) {
	verify := readProjectFile(t, "scripts", "verify.sh")
	iterationBudget := regexp.MustCompile(`-fuzztime="?\$\{FUZZ_BUNDLE_EXECS:-[1-9][0-9]*\}x"?`)
	var found bool
	for _, line := range strings.Split(verify, "\n") {
		if !strings.Contains(line, "-fuzz=FuzzWalkBundleRoundTrip") {
			continue
		}
		found = true
		if !iterationBudget.MatchString(line) {
			t.Fatalf("FuzzWalkBundleRoundTrip must use an iteration budget (-fuzztime=\"${FUZZ_BUNDLE_EXECS:-N}x\"), got:\n  %s", strings.TrimSpace(line))
		}
		if !strings.Contains(line, "-parallel=1") {
			t.Fatalf("FuzzWalkBundleRoundTrip must stay single-worker (-parallel=1), got:\n  %s", strings.TrimSpace(line))
		}
	}
	if !found {
		t.Fatal("verify.sh no longer fuzzes FuzzWalkBundleRoundTrip")
	}
}

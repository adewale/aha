package testquality_test

import (
	"os"
	"path/filepath"
	"regexp"
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
			t.Fatalf("verify.sh contains unsafe shared/mutating behavior %q", forbidden)
		}
	}
	for _, required := range []string{"mktemp -d", "AHA_MCP_CONFORMANCE_ROOT", "AHA_MCP_CONFORMANCE_TOKEN", "cross_compile"} {
		if !strings.Contains(body, required) {
			t.Fatalf("verify.sh missing isolation/cross-compile contract %q", required)
		}
	}
}

func TestVerifyFuzzCommandsNameRealFuzzTargets(t *testing.T) {
	verify := readProjectFile(t, "scripts", "verify.sh")
	names := regexp.MustCompile(`-fuzz=(Fuzz[[:alnum:]_]+)`).FindAllStringSubmatch(verify, -1)
	if len(names) == 0 {
		t.Fatal("verify.sh does not run any fuzz targets")
	}
	var tests strings.Builder
	err := filepath.Walk(filepath.Join("..", "..", "internal"), func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(path, "_test.go") {
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			tests.Write(body)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, match := range names {
		if !strings.Contains(tests.String(), "func "+match[1]+"(") {
			t.Fatalf("verify.sh names missing fuzz target %s", match[1])
		}
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

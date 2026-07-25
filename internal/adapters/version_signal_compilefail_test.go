package adapters_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestAdapterWithoutVersionSignalDoesNotCompile is the construction-time half
// of "every adapter declares its version signal". The exhaustive check over
// Builtins() asserts each registered adapter returns a kind from the closed
// set; this asserts the registration itself is impossible without the method,
// so the guarantee holds for adapter number five before any test runs.
//
// The case is stored as testdata/compilefail/*.go.txt and copied into a
// temporary package under testdata so the normal build never sees it. It has
// to be built from inside the module: internal/adapters is an internal
// package and no separate module could import it.
func TestAdapterWithoutVersionSignalDoesNotCompile(t *testing.T) {
	golden := filepath.Join("testdata", "compilefail", "adapter_without_version_signal.go.txt")
	body, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(filepath.Join("testdata", "compilefail"), "build-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	src := filepath.Join(dir, "adapter_without_version_signal.go")
	if err := os.WriteFile(src, body, 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := exec.Command("go", "build", "-o", os.DevNull, src).CombinedOutput()
	if err == nil {
		t.Fatalf("an adapter without VersionSignal compiled as a SourceAdapter; the interface no longer forces a new adapter to declare where its version lives\n%s", out)
	}
	if !strings.Contains(string(out), "VersionSignal") {
		t.Fatalf("build failed for some other reason than the missing VersionSignal method:\n%s", out)
	}
}

// TestCompileFailCaseCompilesOnceTheMethodIsAdded proves the case above fails
// for the stated reason and nothing else: the same source, with VersionSignal
// supplied, builds. Without this a typo in the fixture would look like the
// invariant holding.
func TestCompileFailCaseCompilesOnceTheMethodIsAdded(t *testing.T) {
	golden := filepath.Join("testdata", "compilefail", "adapter_without_version_signal.go.txt")
	body, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	fixed := string(body) + `

func (adapterWithoutVersionSignal) VersionSignal(entry model.ParsedEntry) model.VersionSignal {
	return model.NoVersion()
}
`
	dir, err := os.MkdirTemp(filepath.Join("testdata", "compilefail"), "build-fixed-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	src := filepath.Join(dir, "adapter_with_version_signal.go")
	if err := os.WriteFile(src, []byte(fixed), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("go", "build", "-o", os.DevNull, src).CombinedOutput(); err != nil {
		t.Fatalf("the compile-fail case does not build even with VersionSignal supplied, so its failure proves nothing about the method:\n%s", out)
	}
}

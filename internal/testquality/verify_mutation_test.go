package testquality_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// fakeMutationTools writes fake `go` and `gremlins` commands that append one
// line per call to a log, and returns the directory holding them, the fake
// gremlins path and the log path. The gremlins line records the directory it
// ran in (relative to the repository root) and whether git would report diff
// paths relative to that directory.
func fakeMutationTools(t *testing.T) (bin, gremlins, callLog string) {
	t.Helper()
	bin = t.TempDir()
	callLog = filepath.Join(t.TempDir(), "calls.log")
	scripts := map[string]string{
		"go":       "#!/usr/bin/env bash\necho \"go $*\" >> \"$FAKE_CALL_LOG\"\n",
		"gremlins": "#!/usr/bin/env bash\necho \"gremlins [/$(git rev-parse --show-prefix) diff.relative=$(git config --get diff.relative)] $*\" >> \"$FAKE_CALL_LOG\"\n",
	}
	for name, body := range scripts {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return bin, filepath.Join(bin, "gremlins"), callLog
}

func runVerifyMutation(t *testing.T, dir, script string, env ...string) string {
	t.Helper()
	bin, gremlins, callLog := fakeMutationTools(t)
	cmd := exec.Command("bash", script, "mutation")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"FAKE_CALL_LOG="+callLog,
		"GREMLINS="+gremlins,
		"MUTATION_PKGS=",
		"MUTATION_DIFF=",
	)
	cmd.Env = append(cmd.Env, env...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("verify.sh mutation failed: %v\n%s", err, stderr.String())
	}
	calls, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatalf("no tool was called: %v\n%s", err, stderr.String())
	}
	return string(calls)
}

// gremlins sets each mutant's timeout from how long its coverage run took. A
// warm Go test cache replays that run in milliseconds and nearly every mutant
// is then reported TIMED OUT, so verify.sh must clear the test cache before
// the first gremlins run. MUTATION_PKGS narrows a run to the named packages.
func TestVerifyMutationClearsTestCacheFirstAndHonoursMutationPkgs(t *testing.T) {
	calls := runVerifyMutation(t, filepath.Join("..", ".."), filepath.Join("scripts", "verify.sh"),
		"MUTATION_PKGS=./internal/model ./internal/depot")
	want := "go clean -testcache\n" +
		"gremlins [/ diff.relative=] unleash ./internal/model --workers 2\n" +
		"gremlins [/ diff.relative=] unleash ./internal/depot --workers 2\n"
	if calls != want {
		t.Fatalf("verify.sh mutation calls:\n%s\nwant:\n%s", calls, want)
	}
}

// gremlins v0.6.0 --diff compares repository-relative `git diff` paths with
// package-relative file names, so it has to run inside the package directory
// with diff.relative set, or it marks every mutant SKIPPED. An empty diff makes
// it mutate the whole package, so unchanged packages must not reach gremlins.
func TestVerifyMutationDiffRunsInsideChangedPackagesOnly(t *testing.T) {
	repo := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.invalid", "-c", "commit.gpgsign=false"}, args...)...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	files := map[string]string{
		"scripts/verify.sh":       readProjectFile(t, "scripts", "verify.sh"),
		"internal/model/ref.go":   "package model\n",
		"internal/depot/depot.go": "package depot\n",
	}
	for name, body := range files {
		path := filepath.Join(repo, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-q")
	git("add", ".")
	git("commit", "-q", "-m", "base")
	if err := os.WriteFile(filepath.Join(repo, "internal", "model", "ref.go"), []byte("package model\n\nvar changed = 1 + 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	calls := runVerifyMutation(t, repo, filepath.Join("scripts", "verify.sh"),
		"MUTATION_PKGS=./internal/model ./internal/depot", "MUTATION_DIFF=HEAD")
	want := "go clean -testcache\n" +
		"gremlins [/internal/model/ diff.relative=true] unleash --diff HEAD --workers 2\n"
	if calls != want {
		t.Fatalf("verify.sh mutation calls:\n%s\nwant:\n%s", calls, want)
	}
}

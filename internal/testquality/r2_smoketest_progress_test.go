package testquality_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestR2SmoketestProgressUsesStderrAndPreservesChildExit(t *testing.T) {
	bin := t.TempDir()
	fakeGo := filepath.Join(bin, "go")
	if err := os.WriteFile(fakeGo, []byte("#!/usr/bin/env bash\necho child-test-output\nexit \"${FAKE_GO_EXIT:-0}\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(exit string) (string, string, error) {
		cmd := exec.Command("bash", filepath.Join("..", "..", "scripts", "r2-smoketest.sh"))
		cmd.Env = append(os.Environ(),
			"PATH="+bin+":"+os.Getenv("PATH"),
			"AHA_R2_TEST_BUCKET=aha-depot-test",
			"AHA_R2_ACCOUNT_ID=0123456789abcdef0123456789abcdef",
			"AHA_R2_ACCESS_KEY_ID=key",
			"AHA_R2_SECRET_ACCESS_KEY=secret",
			"FAKE_GO_EXIT="+exit,
		)
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		return stdout.String(), stderr.String(), err
	}
	stdout, stderr, err := run("0")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "child-test-output") || strings.Contains(stdout, "progress phase=") {
		t.Fatalf("stdout=%q", stdout)
	}
	if !strings.Contains(stderr, "phase=preflight state=completed") || !strings.Contains(stderr, "phase=integration_test state=completed") {
		t.Fatalf("stderr=%q", stderr)
	}
	_, stderr, err = run("7")
	if err == nil || !strings.Contains(stderr, "state=failed") {
		t.Fatalf("failure err=%v stderr=%q", err, stderr)
	}
}

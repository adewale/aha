package testquality_test

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestR2SmoketestRejectsConflictingAliasesBeforeRunningGo(t *testing.T) {
	bin := t.TempDir()
	marker := filepath.Join(t.TempDir(), "go-ran")
	fakeGo := filepath.Join(bin, "go")
	if err := os.WriteFile(fakeGo, []byte("#!/usr/bin/env bash\ntouch \"$FAKE_GO_MARKER\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", filepath.Join("..", "..", "scripts", "r2-smoketest.sh"))
	cmd.Env = append(os.Environ(),
		"PATH="+bin+":"+os.Getenv("PATH"),
		"AHA_R2_TEST_BUCKET=aha-depot-test",
		"AHA_R2_ACCOUNT_ID=0123456789abcdef0123456789abcdef",
		"AHA_R2_ACCESS_KEY_ID=access-canary-a",
		"R2_ACCESS_KEY_ID=access-canary-b",
		"AHA_R2_SECRET_ACCESS_KEY=secret-canary-a",
		"R2_SECRET_ACCESS_KEY=secret-canary-b",
		"FAKE_GO_MARKER="+marker,
	)
	var stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = io.Discard, &stderr
	if err := cmd.Run(); err == nil {
		t.Fatal("conflicting aliases unexpectedly succeeded")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("go ran despite preflight conflict: %v", err)
	}
	text := stderr.String()
	if !strings.Contains(text, "conflicting") || strings.Count(text, "next:") != 1 {
		t.Fatalf("stderr=%q want conflict and one next action", text)
	}
	for _, value := range []string{"access-canary-a", "access-canary-b", "secret-canary-a", "secret-canary-b"} {
		if strings.Contains(text, value) {
			t.Fatalf("stderr leaked value %q: %s", value, text)
		}
	}
}

func TestR2SmoketestProgressUsesStderrAndPreservesChildExit(t *testing.T) {
	bin := t.TempDir()
	fakeGo := filepath.Join(bin, "go")
	fakeGoScript := `#!/usr/bin/env bash
if [ "${FAKE_GO_FORBIDDEN:-0}" = 1 ]; then
  echo 'operation error S3: HeadBucket, https response error StatusCode: 403, api error Forbidden: Forbidden'
else
  echo child-test-output
fi
exit "${FAKE_GO_EXIT:-0}"
`
	if err := os.WriteFile(fakeGo, []byte(fakeGoScript), 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(exit, forbidden string) (string, string, error) {
		cmd := exec.Command("bash", filepath.Join("..", "..", "scripts", "r2-smoketest.sh"))
		cmd.Env = append(os.Environ(),
			"PATH="+bin+":"+os.Getenv("PATH"),
			"AHA_R2_TEST_BUCKET=aha-depot-test",
			"AHA_R2_ACCOUNT_ID=0123456789abcdef0123456789abcdef",
			"AHA_R2_ACCESS_KEY_ID=key",
			"AHA_R2_SECRET_ACCESS_KEY=secret-canary",
			"FAKE_GO_EXIT="+exit,
			"FAKE_GO_FORBIDDEN="+forbidden,
		)
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		return stdout.String(), stderr.String(), err
	}
	stdout, stderr, err := run("0", "0")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "child-test-output") || strings.Contains(stdout, "progress phase=") {
		t.Fatalf("stdout=%q", stdout)
	}
	if !strings.Contains(stderr, "phase=preflight state=completed") || !strings.Contains(stderr, "phase=integration_test state=completed") {
		t.Fatalf("stderr=%q", stderr)
	}
	_, stderr, err = run("7", "0")
	if err == nil || !strings.Contains(stderr, "state=failed") {
		t.Fatalf("failure err=%v stderr=%q", err, stderr)
	}
	stdout, stderr, err = run("1", "1")
	if err == nil {
		t.Fatal("forbidden smoke test unexpectedly succeeded")
	}
	for _, want := range []string{"R2 authorization denied during HeadBucket", "before any smoke objects were written", "Object Read & Write", "matching access key and secret", "next:"} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("forbidden stderr=%q missing %q", stderr, want)
		}
	}
	if strings.Count(stderr, "next:") != 1 {
		t.Fatalf("forbidden stderr=%q want exactly one next action", stderr)
	}
	if strings.Contains(stdout, "StatusCode") || strings.Contains(stdout, "api error") || strings.Contains(stdout, "HeadBucket") {
		t.Fatalf("default forbidden stdout leaked raw child diagnostics: %q", stdout)
	}
	if strings.Contains(stderr, "secret-canary") {
		t.Fatalf("forbidden stderr leaked credentials: %q", stderr)
	}
}

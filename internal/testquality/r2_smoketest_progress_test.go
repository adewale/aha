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

var r2ProductionEnv = []string{
	"AHA_R2_ACCOUNT_ID", "AHA_R2_ENDPOINT", "AHA_R2_REGION", "AHA_R2_ACCESS_KEY_ID", "AHA_R2_SECRET_ACCESS_KEY",
	"R2_ACCOUNT_ID", "R2_ENDPOINT", "R2_REGION", "R2_ACCESS_KEY_ID", "R2_SECRET_ACCESS_KEY",
	"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN", "AWS_ENDPOINT_URL", "AWS_ENDPOINT_URL_S3",
	"AWS_PROFILE", "AWS_DEFAULT_PROFILE", "AWS_SHARED_CREDENTIALS_FILE", "AWS_CONFIG_FILE", "AWS_REGION", "AWS_DEFAULT_REGION",
	"AWS_WEB_IDENTITY_TOKEN_FILE", "AWS_ROLE_ARN", "AWS_ROLE_SESSION_NAME", "AWS_CONTAINER_CREDENTIALS_FULL_URI", "AWS_CONTAINER_CREDENTIALS_RELATIVE_URI",
	"AWS_CONTAINER_AUTHORIZATION_TOKEN", "AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE",
}

func envWithout(keys []string) []string {
	blocked := map[string]bool{}
	for _, key := range keys {
		blocked[key] = true
	}
	var out []string
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		if !blocked[key] {
			out = append(out, item)
		}
	}
	return out
}

func TestLocalSmoketestDepotIsAlwaysUnderFreshWorkspace(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "scripts", "smoketest.sh"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, required := range []string{`WORK="$(mktemp -d`, `DEPOT="$WORK/depot"`, `"depot": { "type": "local", "location": "$DEPOT" }`} {
		if !strings.Contains(text, required) {
			t.Fatalf("local smoketest lacks fresh-workspace invariant %q", required)
		}
	}
	for _, forbidden := range []string{`DEPOT="${AHA_DEPOT`, `DEPOT="$HOME`, `location": "~/.aha`} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("local smoketest can inherit production depot via %q", forbidden)
		}
	}
}

func TestR2IntegrationTestUsesOnlyExplicitSmoketestCapability(t *testing.T) {
	path := filepath.Join("..", "depot", "r2_integration_v2_test.go")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, required := range []string{"pinnedR2SmoketestBucket", "pinnedR2SmoketestAccountID", "pinnedR2SmoketestTargetID", "AHA_R2_SMOKETEST_ACCESS_KEY_ID", "AHA_R2_SMOKETEST_SECRET_ACCESS_KEY", "ResolveR2ConfigExplicit", "matchingAmbientProductionCredential", "attestLiveR2Target"} {
		if !strings.Contains(text, required) {
			t.Fatalf("integration test missing explicit capability component %s", required)
		}
	}
	for _, forbidden := range []string{`firstTestEnv("AHA_R2_ACCESS_KEY_ID"`, `ResolveR2Config(model.R2DepotConfig{})`, `os.Getenv("AHA_R2_SMOKETEST_BUCKET")`, `os.Getenv("AHA_R2_SMOKETEST_ACCOUNT_ID")`, `os.Getenv("AHA_R2_SMOKETEST_ENDPOINT")`, `os.Getenv("AHA_R2_SMOKETEST_TARGET_ID")`} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("integration test can consult production credential source %s", forbidden)
		}
	}
}

func TestR2SmoketestProvisionerIsPinnedAndHasNoTargetArguments(t *testing.T) {
	path := filepath.Join("..", "..", "scripts", "r2-smoketest-provision.sh")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, required := range []string{"aha-depot-test-ebb92642-3301-4021-84b7-31ae4c34e7cd", "aha-r2-smoketest-target-v1.json", "r2-smoketest-target.json", "--remote"} {
		if !strings.Contains(text, required) {
			t.Fatalf("provisioner missing pinned component %q", required)
		}
	}
	if strings.Contains(text, "$1/") || strings.Contains(text, "--bucket") {
		t.Fatalf("provisioner accepts a target override: %s", text)
	}
}

func TestR2SmoketestDefaultsToPinnedTestBucketAndAccount(t *testing.T) {
	bin := t.TempDir()
	fakeGo := filepath.Join(bin, "go")
	fakeGoScript := `#!/usr/bin/env bash
for name in AHA_R2_SMOKETEST_BUCKET AHA_R2_SMOKETEST_ACCOUNT_ID AHA_R2_SMOKETEST_ENDPOINT AHA_R2_SMOKETEST_TARGET_ID; do
  eval "value=\${$name:-}"
  if [ -n "$value" ]; then echo "target override leaked through environment: $name"; exit 81; fi
done
exit 0
`
	if err := os.WriteFile(fakeGo, []byte(fakeGoScript), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", filepath.Join("..", "..", "scripts", "r2-smoketest.sh"))
	cmd.Env = append(envWithout(r2ProductionEnv),
		"PATH="+bin+":"+os.Getenv("PATH"),
		"AHA_R2_SMOKETEST_ACCESS_KEY_ID=smoke-access",
		"AHA_R2_SMOKETEST_SECRET_ACCESS_KEY=smoke-secret",
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("default test target failed: %v stderr=%s", err, stderr.String())
	}
	if !strings.Contains(stderr.String(), "bucket=aha-depot-test-ebb92642-3301-4021-84b7-31ae4c34e7cd") {
		t.Fatalf("stderr does not identify pinned test target: %s", stderr.String())
	}
}

func TestR2SmoketestRejectsTargetOverridesBeforeRunningGo(t *testing.T) {
	bin := t.TempDir()
	marker := filepath.Join(t.TempDir(), "go-ran")
	fakeGo := filepath.Join(bin, "go")
	if err := os.WriteFile(fakeGo, []byte("#!/usr/bin/env bash\ntouch \"$FAKE_GO_MARKER\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", filepath.Join("..", "..", "scripts", "r2-smoketest.sh"), "--bucket", "production-bucket", "--account-id", "0123456789abcdef0123456789abcdef", "--target-id", "custom-target")
	cmd.Env = append(envWithout(r2ProductionEnv),
		"PATH="+bin+":"+os.Getenv("PATH"),
		"AHA_R2_SMOKETEST_ACCESS_KEY_ID=smoke-access",
		"AHA_R2_SMOKETEST_SECRET_ACCESS_KEY=smoke-secret",
		"FAKE_GO_MARKER="+marker,
	)
	var stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = io.Discard, &stderr
	if err := cmd.Run(); err == nil {
		t.Fatal("smoketest accepted a target override")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("go ran despite target override: %v", err)
	}
	if !strings.Contains(stderr.String(), "pinned") || strings.Count(stderr.String(), "next:") != 1 {
		t.Fatalf("stderr=%q want pinned-target rejection and one action", stderr.String())
	}
}

func TestR2SmoketestNeverFallsBackToProductionCredentials(t *testing.T) {
	bin := t.TempDir()
	marker := filepath.Join(t.TempDir(), "go-ran")
	fakeGo := filepath.Join(bin, "go")
	if err := os.WriteFile(fakeGo, []byte("#!/usr/bin/env bash\ntouch \"$FAKE_GO_MARKER\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", filepath.Join("..", "..", "scripts", "r2-smoketest.sh"))
	cmd.Env = append(envWithout(r2ProductionEnv),
		"PATH="+bin+":"+os.Getenv("PATH"),
		"AHA_R2_ACCESS_KEY_ID=production-access-canary",
		"AHA_R2_SECRET_ACCESS_KEY=production-secret-canary",
		"FAKE_GO_MARKER="+marker,
	)
	var stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = io.Discard, &stderr
	if err := cmd.Run(); err == nil {
		t.Fatal("production-only credentials unexpectedly authorized the smoketest")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("go ran despite absent smoketest credentials: %v", err)
	}
	text := stderr.String()
	if !strings.Contains(text, "AHA_R2_SMOKETEST_ACCESS_KEY_ID") || strings.Count(text, "next:") != 1 {
		t.Fatalf("stderr=%q want test-specific credential request and one action", text)
	}
	if strings.Contains(text, "production-access-canary") || strings.Contains(text, "production-secret-canary") {
		t.Fatalf("stderr leaked production credentials: %s", text)
	}
}

func TestR2SmoketestRejectsCredentialsMatchingAmbientProduction(t *testing.T) {
	bin := t.TempDir()
	marker := filepath.Join(t.TempDir(), "go-ran")
	fakeGo := filepath.Join(bin, "go")
	if err := os.WriteFile(fakeGo, []byte("#!/usr/bin/env bash\ntouch \"$FAKE_GO_MARKER\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", filepath.Join("..", "..", "scripts", "r2-smoketest.sh"))
	cmd.Env = append(envWithout(r2ProductionEnv),
		"PATH="+bin+":"+os.Getenv("PATH"),
		"AHA_R2_ACCESS_KEY_ID=same-access-canary",
		"AHA_R2_SECRET_ACCESS_KEY=same-secret-canary",
		"AHA_R2_SMOKETEST_ACCESS_KEY_ID=same-access-canary",
		"AHA_R2_SMOKETEST_SECRET_ACCESS_KEY=same-secret-canary",
		"FAKE_GO_MARKER="+marker,
	)
	var stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = io.Discard, &stderr
	if err := cmd.Run(); err == nil {
		t.Fatal("production credentials were accepted as smoketest credentials")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("go ran with production credentials: %v", err)
	}
	if !strings.Contains(stderr.String(), "distinct test-scoped") || strings.Count(stderr.String(), "next:") != 1 {
		t.Fatalf("stderr=%q want isolation failure and one action", stderr.String())
	}
	if strings.Contains(stderr.String(), "same-access-canary") || strings.Contains(stderr.String(), "same-secret-canary") {
		t.Fatalf("stderr leaked credentials: %s", stderr.String())
	}
}

func TestR2SmoketestProgressUsesStderrAndPreservesChildExit(t *testing.T) {
	bin := t.TempDir()
	fakeGo := filepath.Join(bin, "go")
	fakeGoScript := `#!/usr/bin/env bash
for name in AHA_R2_ACCOUNT_ID AHA_R2_ENDPOINT AHA_R2_REGION AHA_R2_ACCESS_KEY_ID AHA_R2_SECRET_ACCESS_KEY R2_ACCESS_KEY_ID R2_SECRET_ACCESS_KEY AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY AWS_SESSION_TOKEN AWS_PROFILE AWS_SHARED_CREDENTIALS_FILE AWS_CONFIG_FILE AWS_WEB_IDENTITY_TOKEN_FILE AWS_ROLE_ARN AWS_CONTAINER_CREDENTIALS_FULL_URI AWS_CONTAINER_CREDENTIALS_RELATIVE_URI AWS_CONTAINER_AUTHORIZATION_TOKEN AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE; do
  eval "value=\${$name:-}"
  if [ -n "$value" ]; then echo "production environment leaked: $name"; exit 88; fi
done
if [ "$AHA_R2_SMOKETEST_ACCESS_KEY_ID" != "smoke-access" ] || [ "$AHA_R2_SMOKETEST_SECRET_ACCESS_KEY" != "smoke-secret-canary" ]; then
  echo 'wrong smoketest capability'; exit 89
fi
if [ "${FAKE_GO_FORBIDDEN:-0}" = 1 ]; then
  echo 'operation error S3: HeadBucket, https response error StatusCode: 403, api error Forbidden: Forbidden'
else
  echo "child-test-output access=$AHA_R2_SMOKETEST_ACCESS_KEY_ID secret=$AHA_R2_SMOKETEST_SECRET_ACCESS_KEY"
fi
exit "${FAKE_GO_EXIT:-0}"
`
	if err := os.WriteFile(fakeGo, []byte(fakeGoScript), 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(exit, forbidden string) (string, string, error) {
		cmd := exec.Command("bash", filepath.Join("..", "..", "scripts", "r2-smoketest.sh"))
		cmd.Env = append(envWithout(r2ProductionEnv),
			"PATH="+bin+":"+os.Getenv("PATH"),
			"AHA_R2_SMOKETEST_ACCESS_KEY_ID=smoke-access",
			"AHA_R2_SMOKETEST_SECRET_ACCESS_KEY=smoke-secret-canary",
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
	if stdout != "" {
		t.Fatalf("child output must stay private, stdout=%q", stdout)
	}
	if strings.Contains(stderr, "smoke-access") || strings.Contains(stderr, "smoke-secret-canary") || strings.Contains(stderr, "child-test-output") {
		t.Fatalf("success output leaked child log or credentials: %q", stderr)
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
	for _, want := range []string{"R2 authorization denied during HeadBucket", "before any smoke objects were written", "explicit test credential", "Object Read & Write", "AHA_R2_SMOKETEST_*", "next:"} {
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
	if strings.Contains(stderr, "smoke-access") || strings.Contains(stderr, "smoke-secret-canary") {
		t.Fatalf("forbidden stderr leaked credentials: %q", stderr)
	}
}

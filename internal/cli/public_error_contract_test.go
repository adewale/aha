package cli_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/adewale/aha/internal/cli"
)

func TestEveryPublicCommandNormalisesHumanAndJSONErrors(t *testing.T) {
	cases := map[string][]string{
		"analyse":   {"analyse", "failures", "--definitely-invalid", "secret-canary"},
		"archive":   {"archive", "status", "--definitely-invalid", "secret-canary"},
		"dashboard": {"dashboard", "--definitely-invalid", "secret-canary"},
		"init":      {"init", "--definitely-invalid", "secret-canary"},
		"mcp":       {"mcp", "check", "--definitely-invalid", "secret-canary"},
		"search":    {"search", "needle", "--definitely-invalid", "secret-canary"},
		"show":      {"show", "session:v1:c2Vzc2lvbg", "--definitely-invalid", "secret-canary"},
		"status":    {"status", "--definitely-invalid", "secret-canary"},
		"workspace": {"workspace", "status", "--definitely-invalid", "secret-canary"},
	}
	if len(cases) != len(cli.Registry()) {
		t.Fatalf("error-contract cases=%d public commands=%d", len(cases), len(cli.Registry()))
	}
	for name, args := range cases {
		t.Run(name+"/human", func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := cli.RunMain(args, &stdout, &stderr); code != 1 {
				t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
			if stdout.Len() != 0 {
				t.Fatalf("error polluted stdout: %q", stdout.String())
			}
			text := stderr.String()
			if strings.Contains(text, "secret-canary") || strings.Count(text, "next:") != 1 {
				t.Fatalf("unsafe or ambiguous human error: %q", text)
			}
		})
		t.Run(name+"/json", func(t *testing.T) {
			jsonArgs := append(append([]string(nil), args...), "--json")
			var stdout, stderr bytes.Buffer
			if code := cli.RunMain(jsonArgs, &stdout, &stderr); code != 1 {
				t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
			if stdout.Len() != 0 || strings.Contains(stderr.String(), "secret-canary") {
				t.Fatalf("unsafe JSON boundary stdout=%q stderr=%q", stdout.String(), stderr.String())
			}
			var envelope struct {
				Schema string `json:"schema"`
				Error  struct {
					Next        []string `json:"next"`
					Diagnostics []any    `json:"diagnostics"`
				} `json:"error"`
			}
			if err := json.Unmarshal(stderr.Bytes(), &envelope); err != nil {
				t.Fatalf("JSON error=%q: %v", stderr.String(), err)
			}
			if envelope.Schema != "aha.error.v1" || len(envelope.Error.Next) != 1 || len(envelope.Error.Diagnostics) != 0 {
				t.Fatalf("error envelope=%+v", envelope)
			}
		})
	}
}

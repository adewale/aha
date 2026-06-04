package redact_test

import (
	"strings"
	"testing"

	"github.com/adewale/aha/internal/redact"
)

// TestBuiltInPatternsReplace pins the v1.1 contract from
// docs/redaction-spec.md: every built-in pattern, when present in
// input text, is replaced with [REDACTED:<type>], and the hit is
// counted under that type's name.
func TestBuiltInPatternsReplace(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		wantType string
	}{
		{"anthropic_key", "sk-ant-api03-abcdefghijklmnopqrstuvwxyz", "anthropic_key"},
		{"openai_key", "sk-proj-abcdefghijklmnopqrstuvwxyz", "openai_key"},
		{"github_pat", "github_pat_abcdefghijklmnopqrstuvwxyz", "github_fine_grained_token"},
		{"github_token_ghp", "ghp_abcdefghijklmnopqrstuvwxyz", "github_token"},
		{"slack_bot", "xoxb-1234567890-abcdefghij", "slack_token"},
		{"npm_token", "npm_abcdefghijklmnopqrstuvwxyz", "npm_token"},
		{"pypi_token", "pypi-AgEIcHlwaS5vcmcabcdefghij", "pypi_token"},
		{"aws_access_key", "AKIAIOSFODNN7EXAMPLE", "aws_access_key"},
		{"google_api_key", "AIzaSyDdI0hCZtE6vySjMm-WEfRq3CPzqKqqsHI", "google_api_key"},
		{"stripe_live", "sk" + "_live_" + "abcdefghijklmnopqrstuvwx", "stripe_key"},
		{"gcp_service_account", "service-account@example-project.iam.gserviceaccount.com", "gcp_service_account"},
		{"cloudflare_token", "CLOUDFLARE_API_TOKEN=" + strings.Repeat("a", 40), "cloudflare_token"},
		{"jwt_token", "eyJhbGciOiJIUzI1NiIs.eyJzdWIiOiIxMjM0NTY3.SflKxwRJSMeKKF2QT4f", "jwt_token"},
		{"auth_bearer", "Authorization: Bearer abc123def456", "authorization_bearer"},
		{"url_credentials", "https://admin:hunter2@example.com/path", "url_credentials"},
		{"assignment_secret", `API_KEY="abcdefghij1234567890"`, "assignment_secret"},
		{"private_key_block", "-----BEGIN RSA PRIVATE KEY-----\nMIIBPAIBAAJBAKj\n-----END RSA PRIVATE KEY-----", "private_key_block"},
	}
	r := redact.NewDefault()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, hits := r.Apply(tc.input)
			if strings.Contains(out, "[REDACTED:"+tc.wantType+"]") == false {
				t.Fatalf("output missing [REDACTED:%s]:\n  in:  %q\n  out: %q", tc.wantType, tc.input, out)
			}
			if hits[tc.wantType] < 1 {
				t.Fatalf("hits[%q]=%d, want >=1; full hits=%v", tc.wantType, hits[tc.wantType], hits)
			}
		})
	}
}

func TestAdjacentBearerHeaderDoesNotExposeSecretOnSecondPass(t *testing.T) {
	r := redact.NewDefault()
	input := "AKIAIOSFODNN7EXAMPL2Authorization: Bearer abc123def456"
	once, _ := r.Apply(input)
	twice, _ := r.Apply(once)
	if once != twice {
		t.Fatalf("redactor is not idempotent for adjacent auth header:\n  once:  %q\n  twice: %q", once, twice)
	}
}

func TestNewWithExtrasRejectsEmptyAndMarkerMatches(t *testing.T) {
	for _, tc := range []redact.ExtraPattern{
		{Name: "empty", Regex: `.*`},
		{Name: "marker", Regex: `\[REDACTED:[^\]]+\]`},
	} {
		if _, err := redact.NewWithExtras([]redact.ExtraPattern{tc}); err == nil {
			t.Fatalf("NewWithExtras accepted unsafe extra pattern %+v", tc)
		}
	}
}

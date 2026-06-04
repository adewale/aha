package redact_test

import (
	"strings"
	"testing"

	"github.com/adewale/aha/internal/redact"
	"pgregory.net/rapid"
)

// TestApplyIsIdempotent pins the property from docs/redaction-spec.md:
// applying the redactor twice produces the same output as applying it
// once. This is the load-bearing guarantee for the marker syntax —
// [REDACTED:<name>] must not itself contain bytes that match any
// built-in pattern.
func TestApplyIsIdempotent(t *testing.T) {
	r := redact.NewDefault()
	rapid.Check(t, func(t *rapid.T) {
		text := messyText(t)
		first, _ := r.Apply(text)
		second, _ := r.Apply(first)
		if first != second {
			t.Fatalf("redact is not idempotent:\n  in:     %q\n  first:  %q\n  second: %q", text, first, second)
		}
	})
}

// TestSecretsNeverSurviveBuiltIns pins the v1.1 spec: for every
// synthetic match generated against a built-in pattern, the original
// match must not appear as a substring of the redacted output, *when
// the match sits at token boundaries*. The spec's regexes use \b on
// purpose (the alternative — matching mid-token — has unacceptable
// false-positive rates), so a secret glued into the middle of a
// longer alphanumeric run is intentionally out of scope. The
// generator below preserves boundaries by separating prefix/suffix
// with a non-word character when they are non-empty.
func TestSecretsNeverSurviveBuiltIns(t *testing.T) {
	r := redact.NewDefault()
	rapid.Check(t, func(t *rapid.T) {
		gen := rapid.SampledFrom(syntheticSecrets)
		secret := gen.Draw(t, "secret")
		boundaryChars := []rune(" \t\n.,;:|()[]{}<>\"'`")
		prefix := rapid.StringOfN(rapid.RuneFrom([]rune("abc def 012")), 0, 24, -1).Draw(t, "prefix")
		suffix := rapid.StringOfN(rapid.RuneFrom([]rune("abc def 012")), 0, 24, -1).Draw(t, "suffix")
		sep1 := ""
		if prefix != "" {
			sep1 = string(rapid.SampledFrom(boundaryChars).Draw(t, "sep1"))
		}
		sep2 := ""
		if suffix != "" {
			sep2 = string(rapid.SampledFrom(boundaryChars).Draw(t, "sep2"))
		}
		input := prefix + sep1 + secret + sep2 + suffix
		out, hits := r.Apply(input)
		if strings.Contains(out, secret) {
			t.Fatalf("secret survived redaction:\n  input: %q\n  out:   %q\n  hits:  %v", input, out, hits)
		}
		if len(hits) == 0 {
			t.Fatalf("redactor reported zero hits but should have matched %q in %q", secret, input)
		}
	})
}

// TestExtraPatternsCompose pins the spec contract that user-supplied
// patterns run after built-ins, do not collide by name, and that the
// composed redactor still satisfies the idempotence and survival
// properties.
func TestExtraPatternsCompose(t *testing.T) {
	r, err := redact.NewWithExtras([]redact.ExtraPattern{
		{Name: "internal_jwt", Regex: `\bint_[A-Za-z0-9]{32}\b`},
		{Name: "company_secret", Regex: `\bACME-[A-Z0-9]{16}\b`},
	})
	if err != nil {
		t.Fatalf("NewWithExtras: %v", err)
	}
	input := "look int_abcdefghijklmnopqrstuvwxyz123456 and also ACME-ABCD1234EFGH5678 ok"
	out, hits := r.Apply(input)
	if !strings.Contains(out, "[REDACTED:internal_jwt]") {
		t.Fatalf("extra internal_jwt did not fire: %q", out)
	}
	if !strings.Contains(out, "[REDACTED:company_secret]") {
		t.Fatalf("extra company_secret did not fire: %q", out)
	}
	if hits["internal_jwt"] != 1 || hits["company_secret"] != 1 {
		t.Fatalf("hits=%v", hits)
	}
	// Idempotence still holds.
	once, _ := r.Apply(input)
	twice, _ := r.Apply(once)
	if once != twice {
		t.Fatalf("extras break idempotence:\n  once:  %q\n  twice: %q", once, twice)
	}
}

func TestExtraPatternsRejectInvalidConfig(t *testing.T) {
	cases := []struct {
		name    string
		extras  []redact.ExtraPattern
		wantMsg string
	}{
		{"empty name", []redact.ExtraPattern{{Name: "", Regex: `.`}}, "empty name"},
		{"name collision", []redact.ExtraPattern{{Name: "anthropic_key", Regex: `.`}}, "collides"},
		{"bad regex", []redact.ExtraPattern{{Name: "x", Regex: `[`}}, "extra pattern \"x\""},
		{"extra collision", []redact.ExtraPattern{{Name: "dup", Regex: `.`}, {Name: "dup", Regex: `.`}}, "collides"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := redact.NewWithExtras(tc.extras)
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Fatalf("error %q does not contain %q", err, tc.wantMsg)
			}
		})
	}
}

// syntheticSecrets covers one realization of each built-in pattern.
// Generated rather than checked-in so they don't trigger upstream
// secret scanners.
var syntheticSecrets = []string{
	"sk-ant-api03-" + strings.Repeat("a", 30),
	"sk-proj-" + strings.Repeat("b", 30),
	"github_pat_" + strings.Repeat("c", 30),
	"ghp_" + strings.Repeat("d", 30),
	"xoxb-1234-" + strings.Repeat("e", 20),
	"npm_" + strings.Repeat("f", 30),
	"pypi-AgEI" + strings.Repeat("g", 25),
	"AKIAIOSFODNN7EXAMPL2",
	"AIza" + strings.Repeat("h", 35),
	"sk" + "_live_" + strings.Repeat("i", 30),
	"eyJ" + strings.Repeat("j", 15) + "." + strings.Repeat("k", 15) + "." + strings.Repeat("l", 15),
	"Authorization: Bearer " + strings.Repeat("m", 20),
	"https://user:" + strings.Repeat("n", 12) + "@host.example",
}

// messyText returns arbitrary text the property tests can feed into
// Apply: a mix of plain ascii, marker-shaped substrings, and synthetic
// secrets embedded at random.
func messyText(t *rapid.T) string {
	chunkGen := rapid.OneOf(
		rapid.StringOf(rapid.RuneFrom([]rune("abc def_ 0123\t\n:=/-"))),
		rapid.SampledFrom(syntheticSecrets),
		rapid.SampledFrom([]string{"[REDACTED:anthropic_key]", "[REDACTED:assignment_secret]", "[REDACTED:made_up]"}),
	)
	chunks := rapid.SliceOfN(chunkGen, 0, 6).Draw(t, "chunks")
	return strings.Join(chunks, "")
}

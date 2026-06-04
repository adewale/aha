package redact_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/adewale/aha/internal/redact"
)

// FuzzApplyNoPanic feeds arbitrary bytes to the redactor and asserts
// (a) no panic, (b) output length is bounded by a small multiple of
// input length (defends against pathological replacement growth), and
// (c) the output is valid UTF-8 whenever the input is. Idempotence is
// not asserted here because mid-pattern fuzz inputs can technically
// create marker-shaped substrings that the second pass treats as data;
// that case is covered explicitly by the rapid-based property test.
func FuzzApplyNoPanic(f *testing.F) {
	seeds := []string{
		"",
		"sk-ant-api03-" + strings.Repeat("a", 30),
		"ghp_" + strings.Repeat("b", 30),
		"[REDACTED:anthropic_key]",
		"hello world",
		"\x00\x01\x02\xff",
		"sk_live_" + strings.Repeat("x", 30) + " github_pat_" + strings.Repeat("y", 30),
	}
	for _, s := range seeds {
		f.Add(s)
	}
	r := redact.NewDefault()
	f.Fuzz(func(t *testing.T, input string) {
		out, hits := r.Apply(input)
		if len(out) > 4*len(input)+64 {
			t.Fatalf("output growth exceeded bound: |in|=%d |out|=%d", len(input), len(out))
		}
		if utf8.ValidString(input) && !utf8.ValidString(out) {
			t.Fatalf("redaction produced invalid UTF-8 from valid input")
		}
		// Hits map keys must all be known pattern names so a downstream
		// surface can render them safely.
		known := map[string]bool{}
		for _, n := range r.PatternNames() {
			known[n] = true
		}
		for name := range hits {
			if !known[name] {
				t.Fatalf("unknown hit name %q", name)
			}
		}
	})
}

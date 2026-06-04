// Package redact implements v1.1 of docs/redaction-spec.md: a
// deterministic, idempotent, regex-based redactor for text that aha
// projects into its corpus index. The redactor never touches bundles
// (the source of truth) and only the indexed projections
// (messages.text, messages.command, artifacts.text_*, entries.raw_json
// substrings, FTS tokens).
//
// The package is the foundation for later spec levels: v1.2 layers an
// exact-secret env-file pre-pass on top, v1.5 layers a signed audit
// trail on top. v1.3 (TruffleHog) and v1.4 (LLM review) require
// external dependencies and are not implemented here.
package redact

import (
	"fmt"
	"regexp"
	"sort"
)

// Pattern is a compiled redaction rule. Built-ins are constructed by
// NewDefault; user-supplied extras may be added via NewWithExtras.
type Pattern struct {
	Name string
	Re   *regexp.Regexp
}

// Redactor applies a sequence of patterns to text. Each Apply call is
// stateless; the Redactor itself is immutable after construction and
// safe for concurrent use.
type Redactor struct {
	patterns []Pattern
}

// Hits is a per-pattern hit count returned from Apply.
type Hits map[string]int

// builtInSources is the canonical list from the spec, in declaration
// order. Order matters: openai_key uses a negative lookahead in spec
// prose, but Go RE2 has no lookaround, so we run anthropic_key first
// and exclude matched bytes from later passes via a non-overlapping
// scan.
//
// Each entry is name → regex source. Compiled once by NewDefault.
var builtInSources = []struct {
	name string
	re   string
}{
	// Provider keys with distinctive prefixes — these must run before the
	// generic openai_key rule so the more-specific marker wins.
	{"anthropic_key", `\bsk-ant-[A-Za-z0-9_\-]{20,}\b`},
	// PEM-encoded private keys. Multi-line; Go's RE2 needs (?s) to make
	// `.` match newlines.
	{"private_key_block", `(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`},
	// GitHub fine-grained PATs.
	{"github_fine_grained_token", `\bgithub_pat_[A-Za-z0-9_]{20,}\b`},
	// Classic GitHub tokens (ghp_, gho_, ghu_, ghs_, ghr_).
	{"github_token", `\bgh[pousr]_[A-Za-z0-9_]{20,}\b`},
	// Slack tokens (xoxb-, xoxa-, xoxp-, xoxr-, xoxs-).
	{"slack_token", `\bxox[baprs]-[A-Za-z0-9-]{10,}\b`},
	// npm publish tokens.
	{"npm_token", `\bnpm_[A-Za-z0-9]{20,}\b`},
	// PyPI tokens (carry an `AgEI` payload prefix that distinguishes from
	// generic pypi-prefixed words).
	{"pypi_token", `\bpypi-[A-Za-z0-9_\-]{20,}\b`},
	// AWS access key IDs.
	{"aws_access_key", `\bAKIA[0-9A-Z]{16}\b`},
	// Google API keys are exactly AIza + 35 chars.
	{"google_api_key", `\bAIza[0-9A-Za-z\-_]{35}\b`},
	// Stripe live or test secret keys.
	{"stripe_key", `\bsk_(?:live|test)_[A-Za-z0-9]{24,}\b`},
	// GCP service-account identifiers. Not credential material by itself,
	// but it is a stable cloud principal and the spec treats it as context
	// worth redacting in shared corpora.
	{"gcp_service_account", `\b[A-Za-z0-9_\-]{6,}@[a-z0-9\-]+\.iam\.gserviceaccount\.com\b`},
	// Cloudflare API tokens are not lexically distinctive, so require a
	// nearby Cloudflare/CF token label rather than matching arbitrary
	// 40-character strings.
	{"cloudflare_token", `(?i)\b(?:CF_API_TOKEN|CLOUDFLARE(?:_API)?_TOKEN)\s*[:=]\s*(?:"[A-Za-z0-9_\-]{20,}"|'[A-Za-z0-9_\-]{20,}'|[A-Za-z0-9_\-]{20,})`},
	// Three-segment JWTs.
	{"jwt_token", `\beyJ[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{10,}\b`},
	// HTTP bearer headers. Require a word boundary before Authorization so a
	// prior redaction cannot expose an adjacent boundary-delimited secret on
	// a later pass (for example AKIA...Authorization: Bearer ...).
	{"authorization_bearer", `(?i)\bAuthorization\s*:\s*Bearer\s+[A-Za-z0-9._~+/=\-]{8,}`},
	// scheme://user:pass@host
	{"url_credentials", `\b[a-z][a-z0-9+.\-]*://[^/\s:@]+:[^/\s@]+@`},
	// Generic KEY=value assignment. Matched last so distinctive patterns
	// (sk_live_*, ghp_*, etc.) win first.
	{"assignment_secret", `(?i)\b(?:[A-Z0-9_]*(?:PASSWORD|SECRET|TOKEN|API[_-]?KEY|ACCESS[_-]?KEY)[A-Z0-9_]*|password|secret|token|api[_-]?key)\s*[:=]\s*(?:"[^"\n]{8,}"|'[^'\n]{8,}'|[^\s'"]{8,})`},
	// OpenAI keys (run last so anthropic_key wins for sk-ant-*).
	{"openai_key", `\bsk-[A-Za-z0-9_\-]{20,}\b`},
}

// NewDefault returns a Redactor with the v1.1 built-in patterns
// compiled and ordered as specified.
func NewDefault() *Redactor {
	r := &Redactor{patterns: make([]Pattern, 0, len(builtInSources))}
	for _, b := range builtInSources {
		r.patterns = append(r.patterns, Pattern{Name: b.name, Re: regexp.MustCompile(b.re)})
	}
	return r
}

// ExtraPattern is a user-supplied rule from config.
type ExtraPattern struct {
	Name  string
	Regex string
}

// NewWithExtras returns NewDefault() with the given extra patterns
// appended in order. Extras run after built-ins. Returns an error if
// any extra fails to compile, names collide with a built-in or another
// extra, or a name is empty.
func NewWithExtras(extras []ExtraPattern) (*Redactor, error) {
	r := NewDefault()
	seen := map[string]bool{}
	for _, p := range r.patterns {
		seen[p.Name] = true
	}
	for i, e := range extras {
		if e.Name == "" {
			return nil, fmt.Errorf("extra pattern %d: empty name", i)
		}
		if seen[e.Name] {
			return nil, fmt.Errorf("extra pattern %q collides with an existing pattern", e.Name)
		}
		re, err := regexp.Compile(e.Regex)
		if err != nil {
			return nil, fmt.Errorf("extra pattern %q: %w", e.Name, err)
		}
		if re.MatchString("") {
			return nil, fmt.Errorf("extra pattern %q: must not match the empty string", e.Name)
		}
		if re.MatchString("[REDACTED:"+e.Name+"]") || re.MatchString("[REDACTED:openai_key]") {
			return nil, fmt.Errorf("extra pattern %q: must not match redaction markers", e.Name)
		}
		seen[e.Name] = true
		r.patterns = append(r.patterns, Pattern{Name: e.Name, Re: re})
	}
	return r, nil
}

// Apply scans text against every pattern in order and replaces matches
// with [REDACTED:<name>]. Returns the redacted text and per-pattern
// hit counts.
//
// Implementation strategy: each pass is non-overlapping per-pattern
// (the regex engine already guarantees this), and successive patterns
// see the output of the previous one. Marker text [REDACTED:<name>]
// is constructed not to match any built-in pattern, so a second Apply
// call is a no-op — the idempotence property test in the suite
// confirms this for the built-in list.
func (r *Redactor) Apply(text string) (string, Hits) {
	hits := Hits{}
	for _, p := range r.patterns {
		idx := p.Re.FindAllStringIndex(text, -1)
		if len(idx) == 0 {
			continue
		}
		hits[p.Name] += len(idx)
		text = p.Re.ReplaceAllString(text, "[REDACTED:"+p.Name+"]")
	}
	return text, hits
}

// PatternNames returns the active pattern names in apply order, for
// surfacing via aha doctor / status.
func (r *Redactor) PatternNames() []string {
	out := make([]string, len(r.patterns))
	for i, p := range r.patterns {
		out[i] = p.Name
	}
	return out
}

// SortedHitNames returns the keys of hits sorted alphabetically — a
// convenience for deterministic surfacing.
func SortedHitNames(h Hits) []string {
	out := make([]string, 0, len(h))
	for k := range h {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

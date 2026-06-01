# Redaction spec

Status: design, not yet implemented. Targets a `v1.1` change to aha.

## Why

V1 ships with `"redaction": "none-v1"` and the README warns users to treat
bundles and corpora as private. That's defensible posture but a real adoption
blocker:

- Coding-agent transcripts routinely contain secrets that landed there by
  accident (a `cat .env` for debugging, an `Authorization:` header in a
  curl reproduction, an API key the user pasted to ask "is this scoped
  correctly?").
- Once those bytes flow into `~/.aha/corpus/corpus.db`, every `aha search`
  hit, every dashboard row, every MCP `tools/call`, and every `aha read`
  response can echo them back — to the user, to an agent, or to a teammate
  inspecting a shared depot.
- Tracebase, the closest neighbour, treats redaction-on-ingest as a core
  feature (`src/redact.js`, 11 patterns, observable hit counts). Punting on
  it puts aha behind the bar for "safe-to-index transcripts."

The fix is not "encrypt the corpus." Encryption protects bytes at rest from a
machine thief; it does nothing about the user's own agent reading a secret
back through MCP. We need to *not put the secret in the indexed projection
in the first place.*

## Goals

- **Indexed text is safe to surface.** Anywhere aha projects text into the
  corpus (messages.text, messages.command, artifacts.text_*, FTS rows) is
  redacted before it lands.
- **Redactions are observable.** Per-session and per-entry hit counts,
  grouped by pattern, are persisted and surfaced via `aha status` and the
  dashboard. "How much was redacted" is part of the trust model the user
  can verify.
- **Bundles stay raw.** Bundles are content-addressed, deterministic, and
  exist for provenance and recovery. Redaction is applied to the *derived
  index*, not the source-of-truth bundle.
- **Opt-out is explicit.** `config.redaction = "none-v1"` (the existing
  default) preserves today's behaviour; the new default `"v1"` turns on
  the redactor.
- **Extensible.** Users can add custom patterns in config without
  recompiling.

## Non-goals

- **Detecting PII in general.** Names, addresses, phone numbers, account
  numbers, customer IDs — these are out of scope for v1.1. Redaction targets
  *credentials and tokens*, not personal data.
- **DLP / DLP-grade entropy detection.** The pattern list is hand-curated.
  We do not run an entropy classifier or a trained model. Patterns will
  miss novel secret formats.
- **Bundle-time redaction.** Bundles preserve raw evidence; redacting them
  would break content addressing and remove the recovery path. A future
  `aha snapshot --redact` flag could opt in, but it's not v1.1.
- **Reversible / format-preserving redaction.** Replacing a secret with
  `[REDACTED:<type>]` is irreversible by design. If you need the original,
  the bundle still has it.
- **Encrypting the corpus.** Out of scope; a separate proposal.

## Threat model

In scope:
1. A user `aha search`es and a secret appears in the result snippet.
2. A coding agent calls the MCP `search`/`read` tool and a secret shows up in
   the tool result text content.
3. A teammate runs `aha read` on a shared depot bundle and sees a credential
   that was never meant to leave the original machine.
4. The dashboard renders a session and a `.env` line is visible.
5. The user accidentally screen-shares the dashboard.
6. The user pastes an `aha read` output into a public bug report.

Out of scope:
1. An attacker with full read access to `~/.aha/`. The bundles are raw; an
   attacker who can read them can read the secrets. Redaction is about the
   projection surface, not the storage substrate.
2. The original source files (`~/.claude/projects/...`, `~/.codex/sessions/...`).
   aha is read-only with respect to those. If your secret is in your Claude
   Code transcript, it's already on disk before aha ever runs.
3. Custom or proprietary secret formats not matched by the pattern list.

## Where redaction applies

aha has two "tiers" of state. Redaction applies to *exactly one* of them.

| Tier         | Examples                                                                  | Redacted? | Why                                                                |
| ------------ | ------------------------------------------------------------------------- | --------- | ------------------------------------------------------------------ |
| Source       | `~/.claude/projects/*.jsonl`, `~/.codex/sessions/...`                     | No        | Read-only by design; aha never writes there.                       |
| Bundles      | `~/.aha/depot/bundles/v1/<sha>.tar.zst`                                  | No        | Content-addressed; bundles are the recovery + provenance path.     |
| Corpus index | `messages.text`, `messages.command`, `artifacts.text_*`, FTS5 virtual tables, `entries.raw_json`, `entries.source_metadata_json` | **Yes**   | Everything an agent, dashboard, or `aha read` consumer can observe. |

`entries.raw_json` is included even though it is the "parsed source bytes."
That's deliberate: `aha read --json` returns `raw_json`, and the MCP `read`
tool returns it via `corpus.ReadCanonical`. Leaving it raw would defeat the
goal. The bundle remains the unredacted source of truth.

## Patterns

Borrow Tracebase's 11 and extend. Each pattern emits a typed marker so the
replacement carries information.

| Type                          | Pattern                                                                                                    | Notes                                                                  |
| ----------------------------- | ---------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------- |
| `anthropic_key`               | `\bsk-ant-[A-Za-z0-9_\-]{20,}\b`                                                                           | Claude API key                                                         |
| `openai_key`                  | `\bsk-(?!ant-)[A-Za-z0-9_\-]{20,}\b`                                                                       | OpenAI / compatibles; negative lookahead excludes anthropic            |
| `private_key_block`           | `-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----`                                | PEM-encoded private keys (RSA / EC / OpenSSH)                          |
| `github_fine_grained_token`   | `\bgithub_pat_[A-Za-z0-9_]{20,}\b`                                                                         | GitHub fine-grained PAT                                                |
| `github_token`                | `\bgh[pousr]_[A-Za-z0-9_]{20,}\b`                                                                          | Classic GitHub tokens                                                  |
| `slack_token`                 | `\bxox[baprs]-[A-Za-z0-9-]{10,}\b`                                                                         | Slack bot/user/app tokens                                              |
| `npm_token`                   | `\bnpm_[A-Za-z0-9]{20,}\b`                                                                                 | npm publish tokens                                                     |
| `pypi_token`                  | `\bpypi-[A-Za-z0-9_\-]{20,}\b`                                                                             | PyPI tokens                                                            |
| `aws_access_key`              | `\bAKIA[0-9A-Z]{16}\b`                                                                                     | AWS access key ID                                                      |
| `gcp_service_account`         | `\b[A-Za-z0-9_\-]{20,}@[a-z0-9\-]+\.iam\.gserviceaccount\.com\b`                                           | GCP SA email — not a secret but useful for context redaction           |
| `google_api_key`              | `\bAIza[0-9A-Za-z\-_]{35}\b`                                                                               | Google API keys                                                        |
| `stripe_key`                  | `\bsk_(live\|test)_[A-Za-z0-9]{24,}\b`                                                                     | Stripe API keys                                                        |
| `cloudflare_token`            | `\b[A-Za-z0-9_\-]{40}\b` *guarded by `(?:cf\|cloudflare)`* context                                         | Context-required; Cloudflare API tokens are not lexically distinctive |
| `jwt_token`                   | `\beyJ[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{10,}\b`                                     | Three-segment dotted base64; high false-positive risk on test fixtures |
| `authorization_bearer`        | `\bAuthorization\s*:\s*Bearer\s+[A-Za-z0-9._~+/=\-]{8,}`                                                   | HTTP bearer header                                                     |
| `url_credentials`             | `\b[a-z][a-z0-9+.\-]*://[^/\s:@]+:[^/\s@]+@`                                                               | `scheme://user:pass@…`                                                 |
| `assignment_secret`           | `\b([A-Z0-9_]*(?:PASSWORD\|SECRET\|TOKEN\|API[_-]?KEY\|ACCESS[_-]?KEY)[A-Z0-9_]*\|password\|secret\|token\|api[_-]?key)\s*[:=]\s*(?:"[^"\n]{8,}"\|'[^'\n]{8,}'\|[^\s'"]{8,})` | Generic `KEY=value` assignment                                          |

Replacement format: `[REDACTED:<type>]` (matching Tracebase's convention, so
external tools that already grok the marker continue to work). The original
match length is *not* preserved — replacing AKIA-20 with `[REDACTED:aws_access_key]`
makes text *longer*, not shorter. That's intentional: marker uniqueness
beats round-trip preservation.

## Data model

Three changes to `internal/corpus/schema.go`:

1. **Add `redaction_level` column** to `sessions`:
   ```sql
   alter table sessions add column redaction_level text default 'none-v1';
   ```
   Every session records the level it was indexed under. Existing rows
   backfill to `"none-v1"`. New ingests stamp the config's current level.

2. **Add `redactions` table:**
   ```sql
   create table redactions(
     session_key text,
     entry_id    text,
     pattern     text,        -- e.g. "aws_access_key"
     count       integer,
     primary key(session_key, entry_id, pattern),
     foreign key(session_key, entry_id) references entries(session_key, entry_id)
   );
   ```
   Append-only; one row per (entry, pattern). Total redactions per session
   are a simple SUM.

3. **Add `corpus_redaction_level` schema-migration record** so we can detect
   mixed-level corpora (some sessions ingested under `none-v1`, some under
   `v1`).

## Surface

- `aha status --json` adds `redactions_total` and `redactions_by_pattern`.
- `aha verify` reports any mismatch between `sessions.redaction_level` and
  the config's active level (so a user can see "200 sessions are indexed
  at none-v1; consider re-ingesting").
- `aha read --json` strips redactions from `raw_json` too (the parsed
  source projection, not the bundle).
- `aha doctor` lists the active redaction level and its pattern count.
- MCP `status` and `doctor` tools surface the same.
- Dashboard adds a "Redactions" strip in the header (count + breakdown).

## Configuration

```jsonc
{
  // existing v1 setting
  "redaction": "v1",                       // "v1" | "none-v1"

  // new: extension patterns (regex, applied after built-ins)
  "redaction_extra_patterns": [
    { "name": "internal_jwt", "regex": "\\bint_[A-Za-z0-9]{32}\\b" },
    { "name": "company_secret", "regex": "\\bACME-[A-Z0-9]{16}\\b" }
  ]
}
```

Validation:
- Each extra pattern must compile as a Go RE2 regex (no backreferences).
- A pattern name collision with a built-in is rejected.
- Compiled patterns are cached per process.

## Re-ingest behaviour

When the config redaction level changes:

- Existing sessions keep their stamped `redaction_level`. They are *not*
  silently re-redacted on read.
- A new command `aha reindex [--from-level LEVEL] [--session ID...]`
  rebuilds the corpus projection from the bundles using the *current*
  redaction config. This is the supported migration path.
- `aha status` warns when the corpus contains sessions at a lower level
  than the config currently asks for.

## Behaviour details

- **Idempotent.** `redact(redact(s)) == redact(s)`. The marker `[REDACTED:type]`
  contains no characters that match any built-in pattern, so a second pass
  is a no-op.
- **Output never contains the secret.** Property test: for every input the
  generator can produce, the redacted output does not contain the original
  match as a substring.
- **Pattern order is deterministic.** Built-ins fire first, then extras in
  config order. The first match at a given offset wins; nested matches are
  not re-scanned.
- **Tool outputs are first-class.** Tool result content (stdout/stderr from
  `Bash`, file contents from `Read`) is the highest-risk surface and is
  redacted at the same boundary as user/assistant text.
- **Errors do not leak.** A regex compilation panic during redaction aborts
  the ingest of that bundle with a clear error rather than silently writing
  unredacted text.

## Test strategy

Match aha's existing rigor (`rapid`, fuzz, golden):

1. **Golden corpus**: a fixture containing one of every pattern, plus
   negative cases (test/example tokens, common false positives). Test that
   the redacted output matches a pinned golden file.
2. **Property: idempotent.** `rapid.Generator[string]` produces messy
   text; assert `redact(redact(s)) == redact(s)`.
3. **Property: secrets never survive.** For each pattern, generate
   synthetic matches, embed them in random text, assert the original match
   does not appear as a substring of the redacted output.
4. **Property: extra patterns compose.** Given N user patterns and the
   built-ins, no ordering of inputs produces output containing any of the
   matches.
5. **Fuzz**: `FuzzRedactNoPanic` runs the redactor against arbitrary bytes
   and asserts no panic and bounded output growth (input × small constant).
6. **Migration test**: ingest a bundle under `none-v1`, switch config to
   `v1`, run `aha reindex --session X`, assert `sessions.redaction_level`
   becomes `v1` and `redactions` rows appear.
7. **Schema test**: every column in the redactions table is queried by at
   least one `aha status` / `aha verify` path.
8. **Static test**: assert that every code path projecting text into
   `messages.text`/`artifacts.text_*`/`entries.raw_json`/FTS calls the
   redactor exactly once.

## Performance estimate

For a 50,000-entry corpus with average 2 KB of indexable text per entry:

- Per entry: ~17 regex passes (14 built-ins + 3 user extras typical).
  RE2 runtime is linear in input length; ~2 KB × 17 ≈ 34 KB of scanning
  per entry.
- Total ingest cost: 50,000 × 34 KB = ~1.6 GB of scanning, plus the
  underlying parsing/copying that's already in the ingest path. Net
  estimate: **~5–20 seconds** on a typical laptop, well inside the
  existing ingest budget.
- FTS index size: marginal change. Most redactions replace ~20-byte secrets
  with ~25-byte markers; FTS tokenization treats `REDACTED:aws_access_key`
  as a single uncommon token. Net size delta: < 1 %.
- `redactions` table size: bounded by total hits, typically a few hundred
  rows even on large corpora. Negligible.
- Read time: redaction does not run on read. The text is already redacted
  in storage.

## Phased rollout

- **Phase 1 — landing.** Ship the redactor, the schema migration, the
  `redactions` table, the config knob (defaulting to `none-v1` for one
  release cycle), and the status/verify surfaces. No behaviour change for
  existing users.
- **Phase 2 — flip the default.** After one release with explicit
  documentation, flip `config.redaction` default to `v1`. Existing corpora
  remain at their stamped level; `aha status` warns; users opt into
  `aha reindex` when they're ready.
- **Phase 3 — bundle redaction (optional).** If shared depots demand it,
  add `aha snapshot --redact` that produces a parallel, redacted bundle for
  sharing while keeping the canonical raw bundle local.

## Open questions

1. Should the redactor pre-emptively redact `entry_assets.metadata_json`
   too? It rarely contains secrets, but the policy "all projected text"
   says yes.
2. Should the dashboard expose a per-pattern toggle so a user can see
   "what would my history look like if I also redacted `customer_email`?"
   — useful for tuning extra patterns before committing them.
3. What's the right `aha reindex` UX when a bundle is no longer locally
   available? Skip with a warning, or refuse?
4. Image redaction (OCR of base64 PNG content) is explicitly deferred to
   v2; do we surface a count of images-not-redacted to the user?

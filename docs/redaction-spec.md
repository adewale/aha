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

## v1.2 — Exact-secret redaction (spec only; deferred implementation)

Prior art: Mario Zechner's `pi-share-hf` runs deterministic exact-secret
redaction *before* its pattern pass (see
`docs/research/pi-observability.md`). For secrets that exist on disk in a
session's `cwd` (env files, dotenv variants, locally-checked credentials),
literal-string redaction is strictly better than regex: zero false
positives, and the marker carries the exact key name so an auditor can
trace what was scrubbed.

### Scope

Read every value from the configured set of env files in the session's
`cwd` at parse time. For each non-trivial value (≥ 8 chars, not in a
curated allow-list like `PORT=3000` / `DEBUG=true`), substitute every
literal occurrence in the indexed text with `[REDACTED:env:<KEY>]`. Run
this pass *before* the regex pass, so the pattern matcher never sees the
literal value and a generic `assignment_secret` rule does not double-tag
it.

### Inputs

```jsonc
{
  "redaction": "v1.2",
  "redaction_env_files": [".env", ".envrc", ".env.local", ".env.${ENV}"],
  "redaction_env_allowlist": ["PORT", "DEBUG", "NODE_ENV", "LOG_LEVEL"]
}
```

The env-file list is glob-expanded against the session's `cwd` (which is
already in `sessions.raw_cwd`). If a bundle preserves the env file as a
captured artifact (it currently does not — see open questions below),
the literal-match table is built from that artifact. Otherwise the
table is empty and v1.2 collapses to v1.1 behaviour for that session.

### Schema

```sql
alter table sessions add column exact_redactions_count integer default 0;
```

The existing `redactions` table tracks per-(entry, pattern) counts;
exact-secret hits use a synthetic pattern name `env:<KEY>` so they
share that table without a schema change.

### Surfaces

- `aha status --json` adds `exact_redactions_total` and
  `exact_redactions_by_env_key`.
- `aha doctor` reports which env files were read for the most recent
  session and how many literal substitutions resulted.
- `aha verify` does not change.

### Test strategy

1. **Golden**: a fixture with a `.env` file containing
   `STRIPE_KEY=sk_test_redact_me_value_here_1234` plus a session whose
   raw transcript contains that literal in two messages; assert both
   are replaced with `[REDACTED:env:STRIPE_KEY]` and the regex pass
   sees no `stripe_key` hit.
2. **Property**: for any `.env` file the generator can produce, no
   literal value survives in indexed text.
3. **Idempotency**: applying the exact-secret pass twice is a no-op
   (markers contain no `=` so they don't match
   `assignment_secret`).

### Open questions

1. The bundle does not currently preserve env files (they live outside
   the session JSONL). Adding them under
   `sources/<src>/sessions/<sid>/.env` of the bundle's tar would make
   them available to the redactor and to `aha reindex`. That's a
   bundle-format change worth doing alongside this; if not, v1.2 is
   limited to sessions where the bundle has been re-snapshotted with
   env support.
2. Should `redaction_env_files` accept absolute paths (e.g.
   `~/.config/aha/global-secrets.env`)? Operator-friendly but escalates
   trust scope.

## v1.3 — Per-session audit trail for shared depots (spec only; deferred implementation)

Prior art: pi-share-hf keeps a per-session workspace
(`.pi/hf-sessions/`) with redacted file, raw redactor report,
second-opinion report, and LLM review side-by-side. An auditor or
teammate can answer "why was this session published?" months later
from that one folder.

Roadmap note: the second-opinion and LLM-review inputs the original
pi-share-hf workspace combined are themselves *indefinitely
postponed* in aha (see the section below). v1.3 ships with the inputs
it can actually produce — `redactor.json` from v1.1 and
`exact-secret.json` from v1.2 — and reserves the slots for the
postponed outputs so the bundle layout does not need to change if
they later ship.

### Scope

Mirror that workspace into the depot, scoped to bundles that were
explicitly produced for sharing. For every session in an
`aha snapshot --redact` bundle, persist alongside the redacted
session:

- `redactor.json` — per-pattern hit counts (already known from the
  `redactions` table).
- `exact-secret.json` — per-env-key counts from v1.2.
- `second-opinion.json` — verbatim findings from the postponed
  second-opinion scanner pass. Slot reserved; absent in v1.3
  bundles.
- `review.json` — verdict + rationale from the postponed LLM review
  gate. Slot reserved; absent in v1.3 bundles.

### Bundle layout

```
sources/<src>/sessions/<sid>/
  redacted.jsonl
  audit/
    redactor.json
    exact-secret.json
    second-opinion.json
    review.json
```

The redacted JSONL is the source of truth for downstream readers;
the `audit/` directory is opt-in evidence.

### Schema

No corpus-side schema change. The audit files live in the bundle
only; they are not ingested.

### Surfaces

- `aha snapshot --redact --audit-trail` produces the audit directory
  alongside the redacted session.
- `aha depot inspect --session <ref>` prints a summary of the audit
  files inside a depot bundle.
- `aha verify --audit-trail` confirms every shared session has a
  complete audit directory and the counts match the redaction
  configuration recorded in `sessions.redaction_level`.

### Test strategy

1. **Round-trip**: snapshot a session with `--redact --audit-trail`,
   inspect the bundle, assert each audit file is well-formed JSON
   matching the per-session counts.
2. **Tamper detection**: `aha verify --audit-trail` fails when an
   audit file is missing or its counts disagree with the redaction
   configuration.

### Open questions

1. Should the audit files be content-addressed inside the bundle the
   same way session JSONL is, or is filename-addressed (under
   `<sid>/audit/`) sufficient? Content addressing would defend
   against silent edits but complicates the layout.
2. Should `aha ingest` warn (or refuse) when ingesting a bundle whose
   `audit/` directory is present but incomplete? Currently `audit/`
   is ignored on ingest.
3. Once the postponed outputs ship, should the reserved
   `second-opinion.json` / `review.json` slots become *required*
   files (verify fails if absent) or stay optional (so v1.3 bundles
   produced before the upgrade remain valid)?

## Indefinitely postponed (requires external dependencies)

The two designs below were originally numbered v1.3 (second-opinion
scanner) and v1.4 (LLM review gate). Both are *indefinitely
postponed* — not the next item in the roadmap, not blocked on a
specific decision; they will ship only if and when the external
dependencies they require become acceptable to bake into aha.

The barrier is concrete: the second-opinion design requires a
**TruffleHog binary on `$PATH`** (a third-party Go binary aha does not
vendor); the review gate requires an **LLM endpoint** (Anthropic /
OpenAI API access or a local model) plus credentials handling aha
explicitly excludes from its trust model. Both contradict aha's
"works with no network calls and no extra binaries" baseline, so
neither can land without an explicit posture change.

The numbered roadmap (v1.1 → v1.2 → v1.3) is self-contained and
ships without any of this. The v1.3 audit-trail bundle layout
reserves slots for the postponed outputs precisely so promoting
either feature later does not require a depot-format change.

### Postponed: Second-opinion scanner (TruffleHog)

Prior art: pi-share-hf runs **TruffleHog** as a defence-in-depth pass
after its deterministic redactor; non-empty TruffleHog output **gates
upload** of the session. The aha analogue runs the scanner
*post-redaction* on the indexed text and uses its output as a check on
the existing pipeline, not as the redactor itself.

#### Scope

After v1.1 (and v1.2, if enabled) have produced the redacted text, a
configured second-opinion scanner reviews it. Any non-empty finding
aborts ingest of that session with a diagnostic; the bundle remains
in the depot so the user can re-run with `aha reindex --session <id>`
once the cause is understood.

#### Inputs

```jsonc
{
  "redaction_second_opinion": "trufflehog",
  "redaction_second_opinion_path": "/usr/local/bin/trufflehog"
}
```

Recognised values: `"none"` (default), `"trufflehog"`,
`"detect-secrets"`. Custom hooks via `"command:<absolute-path>"` accept
JSON on stdin and emit a non-empty findings array to exit-1 on hit.

#### Schema

```sql
alter table sessions add column second_opinion_status text
  default 'unscanned';
-- 'unscanned' | 'clean' | 'flagged' | 'error'
```

A separate `second_opinion_findings` table mirrors `redactions`:

```sql
create table if not exists second_opinion_findings(
  session_key text,
  entry_id    text,
  detector    text,   -- 'trufflehog' | 'detect-secrets' | 'command:<…>'
  rule_id     text,   -- detector-specific finding id
  count       integer,
  primary key(session_key, entry_id, detector, rule_id),
  foreign key(session_key, entry_id) references entries(session_key, entry_id)
);
```

#### Surfaces

- `aha status --json` adds `second_opinion_status_counts` (clean /
  flagged / unscanned / error) and `second_opinion_findings_total`.
- `aha doctor` reports whether the configured detector is on `$PATH`
  and what version.
- `aha verify` flags sessions stamped `second_opinion_status='flagged'`
  whose entries are still being read.

#### Test strategy

1. **Mock detector**: spec a stdin/stdout contract so the test suite
   can provide a fake scanner without shelling out. Tests assert the
   correct stdin payload reaches the scanner and the result flows
   into the schema.
2. **Live detector** (opt-in, off by default in CI): a verify mode
   that requires TruffleHog on `$PATH` and asserts the integration
   against a fixture with a planted secret.
3. **Idempotency**: re-running the scanner on already-redacted text
   produces the same findings (deterministic per detector version).

#### Open questions

1. Should `second_opinion: "trufflehog"` mandate that ingest fails on
   `error` (binary missing, exec failure), or silently fall back to
   `none`? Defaulting to fail-closed matches the trust model but
   complicates first-run ergonomics.
2. Detector versions drift; should `aha verify` re-run when the
   detector version changes, or stamp the detector version into
   `sessions.second_opinion_status_meta`?

### Postponed: LLM review gate

Prior art: pi-share-hf's final pipeline step asks an LLM three
questions over every redacted session before allowing the upload to
Hugging Face. The aha analogue scopes this strictly to the **sharing
flow** (`aha snapshot --redact`, depot publication) — not normal
local ingest, which must stay LLM-free.

#### Scope

Add `aha review --session <ref> [--model claude-haiku-4-5]` and gate
`aha snapshot --redact` (and equivalent depot-publish surfaces)
behind a passing review. The reviewer reads the *already-redacted*
session entries and answers three yes/no questions:

1. Is this session about the project the depot covers?
2. Is anything in it sensitive that survived redaction?
3. Is the session generally fit to share publicly?

A "no" on (2) or "no" on (3) blocks publication. The review verdict
plus the model's rationale is recorded per session.

#### Inputs

```jsonc
{
  "share_review": {
    "enabled": false,
    "model": "claude-haiku-4-5",
    "system_prompt_path": "~/.config/aha/review-prompt.md",
    "max_tokens": 500
  }
}
```

The review model is a user-installed binary or API call; aha does not
ship LLM credentials. When `enabled: false` (the default), `aha
snapshot --redact` and friends fall through with no review gate.

#### Schema

```sql
create table if not exists session_reviews(
  session_key       text primary key,
  reviewed_at       text,
  model             text,
  verdict           text, -- 'shareable' | 'blocked'
  rationale         text,
  prompt_version    text,
  detector_versions text,
  foreign key(session_key) references sessions(session_key)
);
```

#### Surfaces

- `aha review --session <ref>` runs the review; prints verdict +
  rationale; exits non-zero on `blocked`.
- `aha snapshot --redact --review-required` refuses to publish a
  bundle if any included session lacks a passing review.
- `aha status --json` adds `reviews_by_verdict`.
- Dashboard adds a "Pending review" lane in the share UI (out of scope
  for the review gate itself, listed as a follow-up).

#### Test strategy

1. **Mock model**: the reviewer is invoked via a stdin/stdout contract;
   the test suite plugs a deterministic fake reviewer.
2. **Live model** (opt-in): a verify mode that runs against a real
   Claude / OpenAI / local model and asserts a clearly-shareable
   fixture passes and a planted-secret fixture is blocked.
3. **Verdict round-trip**: after `aha review --session X`, the
   recorded verdict is what `aha snapshot --redact --review-required`
   reads.

#### Open questions

1. Should the reviewer see redacted text only, or also a metadata
   summary (file counts, token totals, tools used)? More context →
   better verdict but more surface for prompt-injection.
2. Should the rationale be redacted itself before storage? It could
   echo back the very secret the reviewer is flagging.
3. Cost budget: a 50-turn session is ~10 KB after redaction; at
   Haiku-4.5 prices that is sub-cent per review. Above some threshold
   (very long sessions), should aha sample / chunk?

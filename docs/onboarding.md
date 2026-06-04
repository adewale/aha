# Onboarding

This page is the shortest verifiable path from “I have agent histories on this machine” to “I can search and read them with `aha`”.

`aha` is local-first. Set up R2 only after local search/read works.

## 0. Privacy check

`aha` v1 does **not** redact secrets. It can archive prompts, code, tool output, paths, images, API responses, and credentials accidentally pasted into chat.

Use a private machine-local corpus first. If you later use R2, use a private bucket.

## 1. Install the CLI

From a clone:

```bash
git clone https://github.com/adewale/aha.git
cd aha
go install ./cmd/aha
```

If `aha` is not found after install, add Go's bin directory to your shell path:

```bash
export PATH="$(go env GOPATH)/bin:$PATH"
```

For zsh, make it persistent:

```bash
echo 'export PATH="$(go env GOPATH)/bin:$PATH"' >> ~/.zshrc
source ~/.zshrc
```

Verify:

```bash
command -v aha
aha --version
```

Passes if `command -v aha` prints a path, commonly `~/go/bin/aha`, and `aha --version` prints a version.

If you only built a local binary, use that path explicitly:

```bash
go build -o /tmp/aha ./cmd/aha
/tmp/aha --version
```

## 2. Inspect local histories

Run:

```bash
aha doctor --json
```

Passes if the command exits successfully and reports source diagnostics. Typical source roots are:

| Source | Root |
|---|---|
| Pi | `~/.pi/agent/sessions` |
| Claude Code | `~/.claude/projects` |
| Codex | `~/.codex/sessions` |
| OpenCode | `$XDG_DATA_HOME/opencode` when set, otherwise `~/.local/share/opencode`; `$OPENCODE_DB` can point at one DB file |

A missing source is not fatal if you do not use that tool. For example, a machine with Pi but no Codex/OpenCode should still onboard successfully.

For a human-readable quick check:

```bash
aha doctor --json > /tmp/aha-doctor.json
python3 - <<'PY'
import json
with open('/tmp/aha-doctor.json') as f:
    data = json.load(f)
for s in data.get('sources', []):
    print(f"{s.get('type')}: ok={s.get('ok')} sessions={s.get('session_files', 0)} root={s.get('expanded_root')}")
PY
```

## 3. Create config and accept the local privacy model

Run once:

```bash
aha init --accept-secrets
```

Passes if it writes a config under:

```text
~/.config/aha/config.jsonc
```

The default local storage is:

| Thing | Default |
|---|---|
| Corpus | `~/.aha` |
| Local depot | `~/.aha/depot` |

## 4. First local archive

Start with a small smoke test if you have lots of history:

```bash
aha refresh --max-sessions 1 --json
```

Passes if JSON is printed and the command exits successfully.

Then ingest all configured local sessions:

```bash
aha refresh --json
```

This snapshots local histories into the local depot, then ingests pending/new bundles into the SQLite + FTS corpus.

## 5. Verify the corpus

Run:

```bash
aha status --json
aha verify --json
```

Passes if `verify` reports no problems. `status` should show non-zero sessions/entries/messages when local histories were found.

If FTS needs repair:

```bash
aha verify --repair-fts --json
aha verify --json
```

## 6. Prove search/read works

Search for something you remember discussing:

```bash
aha search "migration" --refs
```

Passes if it returns lines whose first field is a ref such as `msg:v1:...`, `session:v1:...`, or `artifact:v1:...`. Copy only the ref, not the explanatory text after it.

Read a returned ref:

```bash
REF=$(aha search "migration" --refs | awk 'NF { print $1; exit }')
aha read "$REF" --md
```

Passes if `read` returns surrounding transcript context or artifact text. Search results are leads; `read` is the evidence step.

Agent/script-friendly form:

```bash
aha search "migration" --json --limit 10
REF=$(aha search "migration" --refs | awk 'NF { print $1; exit }')
aha read "$REF" --json
```

## 7. Routine use

After more agent work:

```bash
aha refresh
aha search "what changed in the parser" --refs
```

## 8. Optional R2 depot for backup or multi-machine sync

Do this only after local onboarding works. Local stays the default until you
configure a depot here — this step **opts into R2 as your default depot**, and
you can switch back to local at any time.

> For the full set of depot states and transitions — init, use, snapshot,
> verify, compact — see [`depot-lifecycle.md`](depot-lifecycle.md).

R2 requires two separate things:

1. A private R2 bucket. `aha depot init` creates it for you if it doesn't exist
   (or you can pre-create one — see below).
2. R2 **S3-compatible** credentials: Access Key ID + Secret Access Key.

A Wrangler OAuth login can list/create buckets, but `aha` does not use Wrangler OAuth. `aha` talks to the R2 S3-compatible API.

### Find or create a bucket

If Wrangler is logged in:

```bash
npx wrangler r2 bucket list
npx wrangler r2 bucket create aha-depot
```

Use one private bucket per depot. Do not enable public `r2.dev` or a public custom domain for an `aha` depot.

### Find account ID

```bash
npx wrangler whoami
```

Or copy it from the Cloudflare dashboard URL:

```text
https://dash.cloudflare.com/<ACCOUNT_ID>/...
```

### Create R2 S3 credentials

In Cloudflare dashboard:

```text
R2 Object Storage → Manage R2 API tokens / API tokens → Create token
```

Copy:

- **Access Key ID** → `AHA_R2_ACCESS_KEY_ID`
- **Secret Access Key** → `AHA_R2_SECRET_ACCESS_KEY`

The secret is shown only once. If you did not save it, create a new token.

Recommended token: Object Read & Write, scoped to the depot bucket when bucket scoping is available.

### Configure R2 as the default depot

Export the credentials. The two **secret** keys always stay in the environment,
never in config. The account ID is only needed in the environment for this
first `init` — it gets persisted to config afterward:

```bash
export AHA_R2_ACCESS_KEY_ID="<r2-access-key-id>"
export AHA_R2_SECRET_ACCESS_KEY="<r2-secret-access-key>"
export AHA_R2_ACCOUNT_ID="<account-id>"
```

Now configure the depot. `aha depot init` creates the bucket (if needed), writes
the depot marker, **sets R2 as your default depot**, and persists the non-secret
`depot.r2.account_id` into config:

```bash
aha depot init r2:aha-depot
aha doctor            # default is now r2:aha-depot — reachable and initialized
aha refresh           # snapshots + ingests against R2, no --depot flag needed
```

Because the account ID is now in config, later shells only need the two secret
keys exported. A direnv `.envrc` is a convenient home for them:

```bash
# .envrc
export AHA_R2_ACCESS_KEY_ID="<r2-access-key-id>"
export AHA_R2_SECRET_ACCESS_KEY="<r2-secret-access-key>"
```

### Switch the default depot

Configuring a depot sets the default; switch between configured depots anytime:

```bash
aha depot use local:~/.aha/depot   # back to local
aha depot use r2:aha-depot         # back to R2
```

`aha depot use` only switches to a depot that is already initialized. If an
empty bucket has no depot marker yet, it points you at `aha depot init`; if a
populated depot is missing its marker or has catalog drift, it points you at
`aha depot verify --repair` instead.

### Add another machine

On a second machine, export the same two secret keys (plus `AHA_R2_ACCOUNT_ID`
the first time), then connect to the existing, already-initialized bucket:

```bash
aha depot use r2:aha-depot   # switch the default to the shared R2 depot
aha refresh                  # share history through R2
```

### Deep verification

`aha depot verify` is quick by default. For a full byte-hash check — it downloads
and reads bundle bytes — run it explicitly:

```bash
aha depot verify --deep --json
```

## Troubleshooting

### `zsh: command not found: aha`

Install it and add Go's bin directory to `PATH`:

```bash
cd /path/to/aha
go install ./cmd/aha
export PATH="$(go env GOPATH)/bin:$PATH"
command -v aha
```

### `doctor` shows a missing Codex/Pi/Claude source

That is okay if you do not use that tool. If you do, check whether the tool stores history somewhere non-default and configure that root.

### R2 works in Wrangler but not in `aha`

Wrangler OAuth is not an R2 S3 access key. Create an R2 API token and set:

```bash
AHA_R2_ACCOUNT_ID
AHA_R2_ACCESS_KEY_ID
AHA_R2_SECRET_ACCESS_KEY
```

### I know the bucket but not the access key

Go to Cloudflare dashboard → R2 Object Storage → Manage R2 API tokens / API tokens. If the secret was lost, create a new token and revoke the old one.

### I know the access key but not the bucket

List buckets with Wrangler:

```bash
npx wrangler r2 bucket list
```

Or create a new private depot bucket:

```bash
npx wrangler r2 bucket create aha-depot
```

## Completion checklist

Local onboarding is complete when all are true:

- `command -v aha` prints a binary path.
- `aha doctor --json` runs.
- `aha init --accept-secrets` has written config.
- `aha refresh --json` runs.
- `aha verify --json` reports no corpus problems.
- `aha search "..." --refs` returns refs for a known topic.
- `aha read '<first-field-ref-from-search>' --md` returns full context.

R2 onboarding is complete when all are true:

- The bucket is private.
- `AHA_R2_ACCESS_KEY_ID` and `AHA_R2_SECRET_ACCESS_KEY` are set in the shell or
  secret manager (these two always stay in the environment).
- `aha depot init r2:<bucket>` reported the depot ready and wrote config, so
  `depot.r2.account_id` is now persisted.
- `aha doctor` shows the default depot as `r2:<bucket>` with `ok=true`.
- `aha refresh` (no `--depot` flag) snapshots and ingests against R2.
- `aha depot use local:~/.aha/depot` switches the default back to local, and
  `aha depot use r2:<bucket>` switches it back to R2.

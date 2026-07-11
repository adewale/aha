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

This pushes local histories into the local depot as content-addressed blobs plus a snapshot manifest (unchanged files are never re-uploaded), then pulls new snapshots into the SQLite + FTS corpus.

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
> verify — see [`depot-lifecycle.md`](depot-lifecycle.md).

R2 setup is four steps, in this order:

1. Create a private R2 bucket.
2. Create an R2 API token scoped to that bucket. The token gives you the two
   S3 credentials `aha` reads: Access Key ID and Secret Access Key.
3. Run the read-only `aha depot setup r2:<bucket>` preflight and inspect its
   single next action.
4. Run `aha depot init r2:<bucket>` to write the depot marker and set R2 as
   your default depot.

The bucket comes first because the recommended token is scoped to one bucket:
the dashboard can only scope a token to a bucket that already exists, and a
bucket-scoped token cannot create buckets (bucket creation is an Admin-token
permission in Cloudflare's model).

A Wrangler OAuth login can list and create buckets, but `aha` does not use
Wrangler OAuth. `aha` talks to the R2 S3-compatible API using the token from
step 2.

### Step 1: create the bucket

If Wrangler is logged in:

```bash
npx wrangler r2 bucket create aha-depot
```

Or in the Cloudflare dashboard: **R2 Object Storage → Create bucket**. Either
way: keep the bucket private (no public `r2.dev` access, no public custom
domain), use one bucket per depot, and leave the location on Automatic unless
you have a reason documented in
[`r2-bucket-settings.md`](r2-bucket-settings.md).

While you are in the dashboard, note your account ID — step 3 needs it. It is
the `<ACCOUNT_ID>` in the dashboard URL `https://dash.cloudflare.com/<ACCOUNT_ID>/...`,
or:

```bash
npx wrangler whoami
```

### Step 2: create the S3 credentials

In the Cloudflare dashboard: **R2 Object Storage → Manage R2 API tokens /
API tokens → Create token**, with:

- Permission: **Object Read & Write**.
- Scope: **Apply to specific buckets only**, selecting the bucket from step 1.
- Token type: prefer an **Account API token** over a User API token for a
  depot that outlives any one person — User tokens deactivate when that user
  is removed from the Cloudflare account.

Copy the two generated values:

- **Access Key ID** → `AHA_R2_ACCESS_KEY_ID`
- **Secret Access Key** → `AHA_R2_SECRET_ACCESS_KEY`

The secret is shown only once. If you did not save it, create a new token and
revoke the old one.

### Step 3: configure R2 as the default depot

Load the credentials without putting secrets in shell history. This form works
in both zsh (the macOS default) and bash; do not use `read -p`, whose meaning
differs between those shells. Enter the real values without `<...>` brackets:

```bash
printf 'Cloudflare Account ID: '
read -r AHA_R2_ACCOUNT_ID
export AHA_R2_ACCOUNT_ID

printf 'R2 Access Key ID: '
read -r AHA_R2_ACCESS_KEY_ID
export AHA_R2_ACCESS_KEY_ID

printf 'R2 Secret Access Key: '
read -rs AHA_R2_SECRET_ACCESS_KEY
printf '\n'
export AHA_R2_SECRET_ACCESS_KEY
```

The two **secret** keys always stay in the environment, never in config. First
run the read-only preflight. It validates identifiers locally before any
network request and returns exactly one next command:

```bash
aha depot setup r2:aha-depot --json
```

For a new, reachable bucket its next command is `aha depot init`. Initialization
writes the marker, **sets R2 as the default depot**, and persists only the
non-secret account ID:

```bash
aha depot init r2:aha-depot
aha doctor            # exactly one state-aware next command
aha refresh --max-sessions 1
aha verify --json
```

Run each state-changing command only after the previous command succeeds.
Long phases update on stderr even when final output uses `--json`; redirected
JSON remains quiet unless `--progress=json` is requested.

If the bucket does not exist, `aha depot init` tries to create it. That only
succeeds with an Admin Read & Write token; with the recommended bucket-scoped
token, init reports that the token cannot create buckets and points you back
to step 1.

Because the account ID is now in config, later shells only need the two secret
keys exported. A direnv `.envrc` is a convenient home for them:

```bash
# .envrc — replace the example words with real values; never commit this file.
export AHA_R2_ACCESS_KEY_ID="real-access-key-id"
export AHA_R2_SECRET_ACCESS_KEY="real-secret-access-key"
```

### Optional: live smoke test

From a repo clone, `scripts/r2-smoketest.sh` runs the depot integration test
against a real bucket — real conditional writes and read-after-write, the two
things local fakes cannot vouch for. Create a **separate test bucket** and a
distinct **Object Read & Write** S3 token scoped only to that bucket.

The smoke test never reads production `AHA_R2_*`, `R2_*`, or `AWS_*`
credentials. It defaults to this project's dedicated test target:

- bucket: `aha-depot-test-ebb92642-3301-4021-84b7-31ae4c34e7cd`
- account: `8837d43caf5a2ab3df5143eb3e2f1b96`

Missing test credentials are requested securely from an interactive terminal:

```bash
scripts/r2-smoketest.sh
```

The bucket, account-derived endpoint, and identity nonce are source-pinned;
the script and direct integration test accept no target flags or target
environment variables. Before any mutation, the test reads the pre-existing
`aha-r2-smoketest-target-v1.json` object and requires an exact identity match.
Maintainers can restore that object with the separately pinned
`scripts/r2-smoketest-provision.sh` command.

For non-interactive CI, provide only
`AHA_R2_SMOKETEST_ACCESS_KEY_ID` and
`AHA_R2_SMOKETEST_SECRET_ACCESS_KEY`. The script rejects a test key that
matches ambient production credentials, removes all production credential
names from the child process, and never accepts secrets in argv.

The smoke test includes simultaneous first pushes from multiple machine IDs,
so the real service—not only the local fake—vouches for shared-index
conditional-write contention. Cleanup removes discovery/index registration
before deleting each run namespace; the target attestation and depot metadata
are intentionally persistent. An interrupted run can leave uniquely named
smoke objects behind.

### Switch the default depot

Configuring a depot sets the default; switch between configured depots anytime:

```bash
aha depot use local:~/.aha/depot   # back to local
aha depot use r2:aha-depot         # back to R2
```

`aha depot use` only switches to a depot that is already initialized. If an
empty bucket has no depot marker yet, it points you at `aha depot init`; if a
depot has problems, it points you at `aha depot verify --deep` to diagnose.
Note that `aha depot init` refuses a bucket holding a v1 (`bundles/v1` +
`catalog/v1`) depot — there is no migration; recover a v1 depot by importing
its bundle files with `aha ingest <bundle.tar.zst>` and pushing fresh.

### Add another machine

On a second machine, export the same two secret keys (plus `AHA_R2_ACCOUNT_ID`
the first time), then connect to the existing, already-initialized bucket:

```bash
aha depot use r2:aha-depot   # switch the default to the shared R2 depot
aha refresh                  # share history through R2
```

### Deep verification

`aha depot verify` is quick by default (pointers resolve to well-formed
manifests). For a full byte-hash check — it fetches every referenced blob and
verifies its hash — run it explicitly:

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

### `read: -p: no coprocess`

That is zsh interpreting bash's `read -p` differently. Use the shell-neutral
`printf` + `read -r` / `read -rs` commands in step 3 above.

### `aha depot init` says the token cannot create buckets

The token is fine — bucket creation is simply not an object-token permission.
Create the bucket first (step 1 above: `npx wrangler r2 bucket create
aha-depot` or the dashboard), then rerun `aha depot init r2:aha-depot`.

### `HeadBucket` returns `403 Forbidden`

No depot mutation occurred. `aha` automatically tries a bounded
`ListObjectsV2` read when `HeadBucket` is forbidden. If listing succeeds, it
accepts the bucket and continues; if both supported read checks return 403, the
loaded S3 key pair does not authorize the bucket/account endpoint.

The usual causes are a token scoped to another bucket, an access key and secret
copied from different tokens, or conflicting `AHA_R2_*`/`R2_*` aliases. Alias
conflicts are rejected before networking without printing either value.

Next: export a matching key pair from one **Object Read & Write** R2 S3 token
scoped to the named bucket, then rerun the failed command. `aha` and the live
smoke test classify this case without printing credential values. Set
`--verbose`/`AHA_R2_SMOKETEST_VERBOSE=1` retains the private 0600 Go test log
and reports its path for explicit local inspection. Child logs are never
streamed into normal output because dependencies could echo credentials.

### The endpoint contains `%3Cyour-...%3E` or TLS fails on `<your-account-id>`

A documentation placeholder was exported literally. Current `aha` rejects
placeholder-looking account IDs, bucket names, endpoints, and credentials
before networking. Reload the real values without angle brackets, then run:

```bash
aha depot setup r2:aha-depot --json
```

### The corpus uses the pre-v2 bundle schema

Preserve it and rebuild from the selected depot atomically:

```bash
aha corpus rebuild --backup --json
```

There is deliberately no no-backup mode. The command builds and verifies a
sibling replacement before promotion, leaves the timestamped backup in place,
and reports its path.

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

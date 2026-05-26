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

A missing source is not fatal if you do not use that tool. For example, a machine with Pi but no Codex should still onboard successfully.

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

Passes if it returns refs such as `msg:v1:...`, `session:v1:...`, or `artifact:v1:...`.

Read a returned ref:

```bash
aha read '<paste-ref-here>' --md
```

Passes if `read` returns surrounding transcript context or artifact text. Search results are leads; `read` is the evidence step.

Agent/script-friendly form:

```bash
aha search "migration" --json --limit 10
aha read '<paste-ref-here>' --json
```

## 7. Routine use

After more agent work:

```bash
aha refresh
aha search "what changed in the parser" --refs
```

## 8. Optional R2 setup for backup or multi-machine sync

Do this only after local onboarding works.

R2 requires two separate things:

1. A private R2 bucket.
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

### Configure env and verify

```bash
export AHA_R2_ACCOUNT_ID="<account-id>"
export AHA_R2_ACCESS_KEY_ID="<r2-access-key-id>"
export AHA_R2_SECRET_ACCESS_KEY="<r2-secret-access-key>"

aha doctor --depot r2:aha-depot --json
aha snapshot --depot r2:aha-depot --accept-secrets --json
aha depot verify r2:aha-depot --json
```

Passes if doctor can reach the depot, snapshot uploads a bundle, and verify reports the depot catalog/object checks succeeded.

For a full byte-hash check, run explicitly because it downloads/reads bundle bytes:

```bash
aha depot verify r2:aha-depot --deep --json
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
- `aha read '<ref>' --md` returns full context.

R2 onboarding is complete when all are true:

- The bucket is private.
- `AHA_R2_ACCOUNT_ID`, `AHA_R2_ACCESS_KEY_ID`, and `AHA_R2_SECRET_ACCESS_KEY` are set in the shell or secret manager.
- `aha doctor --depot r2:<bucket> --json` runs without credential/bucket errors.
- `aha snapshot --depot r2:<bucket> --accept-secrets --json` uploads a bundle.
- `aha depot verify r2:<bucket> --json` passes.

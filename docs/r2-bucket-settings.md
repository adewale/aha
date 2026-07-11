# Recommended Cloudflare R2 Settings

`aha` R2 depots contain unredacted agent-history blobs and snapshot manifests. Treat the bucket as private sensitive storage.

## Bucket posture

- Keep the bucket **private**.
- Do **not** enable public `r2.dev` access for an `aha` depot bucket.
- Do **not** attach a public custom domain to the depot bucket.
- Use one bucket per depot, for example `aha-depot`.
- Use jurisdiction-specific buckets only when you also configure the matching jurisdiction endpoint.
- Location: Automatic placement is Cloudflare's default and the right choice
  for a depot shared across machines in different places. A location hint
  (for example `WNAM`, `WEUR`) is worth setting only when every machine that
  pushes and pulls sits in one region — and it only takes effect on the
  bucket's **first** creation; recreating a same-named bucket keeps the
  original placement.

## Credentials and tokens

`aha` uses Cloudflare R2's **S3-compatible API**, so it needs an R2 **Access Key ID** and **Secret Access Key**. A Wrangler login/OAuth token can list or create buckets with `wrangler`, but it is **not** the same thing as an S3 access key and is not read by `aha`.

### Find or create an R2 access key

In the Cloudflare dashboard:

1. Open <https://dash.cloudflare.com> and select the account that owns the bucket.
2. Go to **R2 Object Storage**.
3. Open **Manage R2 API tokens** / **API tokens**.
4. Create an **R2 API token** for S3-compatible access.
5. Copy the generated values:
   - **Access Key ID** → `AHA_R2_ACCESS_KEY_ID`
   - **Secret Access Key** → `AHA_R2_SECRET_ACCESS_KEY`

The **Secret Access Key is shown only once**. If it was not saved, create a new token and revoke the old one. The Access Key ID is an identifier, not the secret, but still avoid committing it.

Recommended token shape:

- Permission: **Object Read & Write** for machines that run `aha snapshot` or `aha refresh`.
- Scope: restrict the token to the specific depot bucket. Create the bucket
  first — the dashboard can only scope a token to an existing bucket, and a
  bucket-scoped token cannot create buckets (`aha depot init` says so
  explicitly when CreateBucket is denied).
- Token type: prefer an **Account API token** over a User API token. User
  tokens deactivate when that user is removed from the Cloudflare account,
  which silently breaks every machine using them; account tokens stay valid
  until revoked.
- Use separate tokens per machine so a lost laptop can be revoked without rotating every client.
- Use **Object Read only** only for hosts that will pull/read depot snapshots but never push.
- Use admin permissions only for one-time bucket creation/administration; do not use broad admin tokens for daily `aha refresh`.
- For CI jobs or borrowed machines, Cloudflare's **temporary access
  credentials** (short-lived keys derived from a parent token via the
  Temporary Credentials API) avoid leaving a long-lived secret on a host you
  do not control.
- Store secrets in environment variables or an OS/secret-manager mechanism, not in repo files or `aha` config.

`aha` reads these variables. For interactive zsh/bash setup, avoid literal
placeholders and keep the secret out of shell history:

```bash
printf 'Cloudflare Account ID: '; read -r AHA_R2_ACCOUNT_ID; export AHA_R2_ACCOUNT_ID
printf 'R2 Access Key ID: '; read -r AHA_R2_ACCESS_KEY_ID; export AHA_R2_ACCESS_KEY_ID
printf 'R2 Secret Access Key: '; read -rs AHA_R2_SECRET_ACCESS_KEY; printf '\n'; export AHA_R2_SECRET_ACCESS_KEY
# Optional only for jurisdiction-specific or non-Cloudflare S3 endpoints:
# export AHA_R2_ENDPOINT=https://real-account-id.eu.r2.cloudflarestorage.com
```

`aha` rejects documentation placeholders before creating an SDK client.
Cloudflare account IDs must be 32 lowercase hexadecimal characters; bucket
names must be 3–63 lowercase letters, numbers, or hyphens.

`R2_*` aliases also work. `aha` intentionally ignores generic `AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY` so it does not accidentally use unrelated AWS credentials.

After loading real values, preflight and follow its single next action:

```bash
aha depot setup r2:aha-depot --json
aha depot init r2:aha-depot
aha snapshot --depot r2:aha-depot --accept-secrets --json
aha depot verify r2:aha-depot --json
```

## Bucket and account discovery

To list buckets with Wrangler, if already logged in:

```bash
npx wrangler r2 bucket list
```

To create a private bucket for `aha`:

```bash
npx wrangler r2 bucket create aha-depot
```

To find the account ID with Wrangler:

```bash
npx wrangler whoami
```

Or use the dashboard URL/account page. The account ID is the `<ACCOUNT_ID>` in:

```text
https://dash.cloudflare.com/<ACCOUNT_ID>/...
https://<ACCOUNT_ID>.r2.cloudflarestorage.com
```

## Endpoint and region

Cloudflare's S3-compatible endpoint is:

```text
https://<ACCOUNT_ID>.r2.cloudflarestorage.com
```

Jurisdiction-specific buckets require jurisdiction-specific endpoints:

```text
https://<ACCOUNT_ID>.eu.r2.cloudflarestorage.com
https://<ACCOUNT_ID>.fedramp.r2.cloudflarestorage.com
```

A bucket's jurisdiction is fixed at creation and cannot be changed afterward.

The S3 region should be `auto`.

### SDK checksums

Nothing to configure: `aha` pins the AWS SDK's request/response checksum
behavior to "when required", per Cloudflare's SDK guidance, because R2
implements the `x-amz-checksum-*` headers only partially. Integrity does not
depend on those headers anyway — every blob and manifest is content-addressed
and re-hashed on read.

## Durability and auditability

Because blobs and manifests are content-addressed, accidental mutation is detectable. Accidental deletion is the bigger risk — `aha` itself never deletes depot objects.

Recommended bucket controls:

- Add a **bucket lock** rule on the `blobs/` prefix (dashboard: bucket →
  Settings → Bucket lock rules; or `npx wrangler r2 bucket lock add`).
  Blobs are write-once session content — the data that cannot be
  reconstructed if lost — so an indefinite or long retention lock turns
  "aha never deletes" from a code-level promise into a service-enforced one.
- Do **not** lock the `machines/` prefix or the whole bucket. Every push
  overwrites `machines/<id>/latest` and `machines/index.json` with
  conditional PUTs; a lock covering those keys breaks all future pushes.
- Use lifecycle rules cautiously; do not expire `blobs/v2/*` or `machines/*` unless you intentionally want to destroy history.
- Enable access logs, audit logs, or event notifications where available so object writes/deletes are observable.
- Consider a second backup/export path for the bucket if the depot is your only durable copy.

## Common mistakes `aha doctor --depot r2:...` checks

`aha doctor --depot r2:aha-depot --json` warns about common R2/S3 mistakes:

- putting an endpoint URL in the depot address instead of using `r2:BUCKET`;
- using public `r2.dev` URLs as the S3 endpoint;
- using non-HTTPS R2 endpoints outside local fake-S3 tests;
- setting a region other than `auto`;
- using malformed bucket names;
- relying on generic `AWS_*` credentials, which `aha` ignores;
- likely wrong account/bucket/endpoint combinations (`404`/not found);
- likely token scope/permission problems (`403`/access denied); a forbidden
  `HeadBucket` is checked with a one-key `ListObjectsV2` fallback before being
  classified, so an object token is not rejected merely for a HEAD-policy quirk;
- signature/access-key mismatches;
- conflicting `AHA_R2_*` and `R2_*` aliases (rejected before networking without
  printing either value).

## End-to-end validation

From a repo clone, `scripts/r2-smoketest.sh` runs the live-bucket integration
tests: push, delta push, unchanged-state reuse, pull, deep verify, and
simultaneous first pushes from multiple machine IDs against the real service.
Use a dedicated bucket and a distinct Object Read & Write token scoped only to
that bucket.

The script defaults to the project's pinned dedicated target—bucket
`aha-depot-test-ebb92642-3301-4021-84b7-31ae4c34e7cd` in account
`8837d43caf5a2ab3df5143eb3e2f1b96`. Credentials are securely prompted on a TTY
or supplied through the dedicated `AHA_R2_SMOKETEST_ACCESS_KEY_ID` and
`AHA_R2_SMOKETEST_SECRET_ACCESS_KEY` variables. Production `AHA_R2_*`, `R2_*`,
and `AWS_*` credentials are never fallback inputs and are removed from the
child environment. A test key matching an ambient production key is rejected
before networking. The target is source-pinned and must present the exact
pre-existing identity attestation before the test can mutate anything.

```bash
scripts/r2-smoketest.sh
```

Target flags and target environment variables are intentionally unsupported.
Maintainers can restore the pinned test bucket's identity attestation with
`scripts/r2-smoketest-provision.sh`; that provisioner is also source-pinned and
accepts no destination arguments.

## References

- Cloudflare R2 Authentication: https://developers.cloudflare.com/r2/api/tokens/
- Cloudflare R2 S3 API compatibility: https://developers.cloudflare.com/r2/api/s3/api/
- Cloudflare R2 AWS SDK for Go example: https://developers.cloudflare.com/r2/examples/aws/aws-sdk-go/
- Cloudflare R2 bucket locks: https://developers.cloudflare.com/r2/buckets/bucket-locks/
- Cloudflare R2 data location (hints and jurisdictions): https://developers.cloudflare.com/r2/reference/data-location/

# Recommended Cloudflare R2 Settings

`aha` R2 depots contain unredacted agent-history bundles. Treat the bucket as private sensitive storage.

## Bucket posture

- Keep the bucket **private**.
- Do **not** enable public `r2.dev` access for an `aha` depot bucket.
- Do **not** attach a public custom domain to the depot bucket.
- Use one bucket per depot, for example `aha-depot`.
- Use jurisdiction-specific buckets only when you also configure the matching jurisdiction endpoint.

## Credentials and tokens

Create R2 S3 API tokens in Cloudflare's **R2 object storage → API Tokens** UI. R2 tokens produce an **Access Key ID** and **Secret Access Key** for S3-compatible SDKs.

Recommended token shape:

- Permission: **Object Read & Write** for machines that run `aha snapshot` or `aha refresh`.
- Scope: restrict the token to the specific depot bucket when Cloudflare offers bucket scoping.
- Use separate tokens per machine so a lost laptop can be revoked without rotating every client.
- Use **Object Read only** only for hosts that will ingest/read depot bundles but never publish snapshots.
- Use admin permissions only for one-time bucket creation/administration; do not use broad admin tokens for daily `aha refresh`.
- Store secrets in environment variables or an OS/secret-manager mechanism, not in repo files or `aha` config.

`aha` reads these variables:

```bash
export AHA_R2_ACCOUNT_ID=...
export AHA_R2_ACCESS_KEY_ID=...
export AHA_R2_SECRET_ACCESS_KEY=...
# Optional, for jurisdiction-specific or fake-S3 endpoints:
export AHA_R2_ENDPOINT=https://<ACCOUNT_ID>.r2.cloudflarestorage.com
export AHA_R2_REGION=auto
```

`R2_*` aliases also work. `aha` intentionally ignores generic `AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY` so it does not accidentally use unrelated AWS credentials.

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

The S3 region should be `auto`.

## Durability and auditability

Because bundles are content-addressed, accidental mutation is detectable. Accidental deletion is the bigger risk.

Recommended bucket controls where available for your account/bucket:

- Enable object versioning or retention/object-lock style controls if Cloudflare exposes them for the bucket.
- Use lifecycle rules cautiously; do not expire `bundles/v1/*` unless you intentionally want to destroy history.
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
- likely token scope/permission problems (`403`/access denied);
- signature/access-key mismatches.

## References

- Cloudflare R2 Authentication: https://developers.cloudflare.com/r2/api/tokens/
- Cloudflare R2 S3 API compatibility: https://developers.cloudflare.com/r2/api/s3/api/
- Cloudflare R2 AWS SDK for Go example: https://developers.cloudflare.com/r2/examples/aws/aws-sdk-go/

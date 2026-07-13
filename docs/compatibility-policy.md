# Compatibility policy

Aha separates **data-format compatibility**, **wire-contract compatibility**, and **behaviour compatibility**. They have different failure modes and must not share one version switch.

## Current construction

| Boundary | Compatibility mechanism |
|---|---|
| Config | `aha.config.v1`; unsupported schemas fail before commands mutate state; namespaced `extensions` survive read/write; JSONC comments survive updates |
| Archive | current `aha-depot/v3` marker plus authoritative `aha-v3/` writer namespace, bounded major/minor versions, and required/optional features; exact unprefixed v2 Archives and `aha-snapshot-manifest/v2` bytes remain read-only; each v3 snapshot declares semantic features |
| Workspace | `workspace.db` (distinct from the pre-0.2 writer filename), SQLite migration number, and a checksummed `aha.workspace.identity.v1` witness refreshed after migration |
| CLI JSON | response-local schemas such as `aha.status.v2` |
| HTTP | `/api/v2/...`, `Aha-Contract-Schema: aha.http.v2`, and `/api/v2/capabilities` |
| MCP | `aha.mcp.v2`, discoverable through `aha_capabilities` |
| TypeScript | generated from the canonical MCP tool registry; HTTP and stdio transports negotiate and reject unsupported required features before tool calls |

An older binary may read additive optional metadata. It must reject an unknown required feature, unsupported format major or minor, config schema, Workspace identity schema, or newer SQLite migration before obtaining a write capability. The Archive writer fence and separate Workspace database filename prevent the immediately preceding binary from becoming a mixed-version writer.

Archive objects are never reinterpreted in place. A new representation gets a new schema or required feature. Workspace databases are rebuildable, but an unsupported database is not opened for mutation.

## Compatibility dates: research

### Cloudflare Workers and Wrangler

Cloudflare continuously updates one Workers runtime. A Worker's pinned `compatibility_date` activates the set of behaviour changes whose activation dates are less than or equal to that date. `compatibility_flags` can opt into a future change early or hold back a change that the selected date would otherwise enable.

Important properties:

- the date is stored with the deployment rather than inferred from the currently installed Wrangler version;
- moving the date is explicit and takes effect on the next deployment;
- newly created projects use a current date, while API uploads that omit it fall back to the oldest date (`2021-11-02`);
- Cloudflare says old dates remain supported, although old behaviour may require historical documentation and newer features can require a newer date;
- flags name individual semantic changes, while the date is an ordered shorthand for a bundle of flags.

Sources: [Compatibility dates](https://developers.cloudflare.com/workers/configuration/compatibility-dates/), [compatibility flags](https://developers.cloudflare.com/workers/configuration/compatibility-flags/), and [Wrangler configuration](https://developers.cloudflare.com/workers/wrangler/configuration/).

### Stripe API releases

Stripe uses date-labelled API releases, now combined with named release trains. A major release may contain breaking behaviour; monthly releases within that train are documented as backwards-compatible. Integrations can test a selected version with the `Stripe-Version` request header before changing the account default, and Stripe offers a limited rollback period after an account upgrade. Explicitly versioned webhook endpoints retain their selected behaviour.

This is not merely calendar ordering: the named release train identifies the breaking compatibility family, while the date identifies a release within it.

Sources: [API versioning](https://docs.stripe.com/api/versioning) and [API upgrades](https://docs.stripe.com/upgrades).

### Rust editions

Rust editions are year-labelled, per-crate opt-ins for otherwise incompatible language changes. Crates on different editions must interoperate because editions compile to a shared internal representation. Cargo selects a current edition for new crates and supplies migration tooling for existing crates.

Source: [Rust Edition Guide: What are editions?](https://doc.rust-lang.org/edition-guide/editions/).

### Android target API levels

Android separates the platform version available on a device from the `targetSdkVersion` selected by an application. The selected target gates behaviour changes while allowing the application to run on older platform versions. Google Play imposes target-age deadlines for new and existing applications, demonstrating that indefinite preservation of old behaviour can conflict with security and ecosystem health.

Source: [Google Play target API requirements](https://developer.android.com/google/play/requirements/target-sdk).

### Kubernetes API and storage versions

Kubernetes versions API groups independently, requires objects to round-trip between served versions without losing fields, separates preferred/served versions from storage versions, and applies explicit deprecation windows. Persisted representations constrain when an API version can disappear.

Source: [Kubernetes deprecation policy](https://kubernetes.io/docs/reference/using-api/deprecation-policy/).

## Lessons for aha

### 1. Dates describe behaviour, not bytes

A date is useful when one implementation must preserve historical semantics such as query ranking, redaction interpretation, or adapter parsing. It is insufficient for an unknown on-disk representation. Persistent formats therefore retain explicit schemas and required capabilities.

Aha should introduce a config-level date only when the first intentional behaviour gate exists:

```jsonc
{
  "schema": "aha.config.v1",
  "compatibility_date": "YYYY-MM-DD",
  "compatibility_flags": ["example-change-v1"]
}
```

Adding an inert date now would be configuration theatre. Until there is a real gated change, schemas and capabilities are the complete mechanism.

### 2. Never derive compatibility from wall-clock time

A stored date changes only through an explicit config edit or future upgrade command. Installing a new aha binary, crossing midnight, or rebuilding a Workspace must not silently advance it.

New configs should select the current supported behaviour date. A missing date in an older config should map to one documented baseline, not to “today”.

### 3. Maintain an immutable change registry

Each behaviour change needs:

- a stable feature name;
- an activation date;
- old and new semantics;
- affected commands and contracts;
- migration/test guidance;
- whether a temporary positive or negative override exists.

The effective profile is constructed from `(date + explicit flags)` and reported by status/capability endpoints. Conflicting flags are rejected during config construction.

### 4. Keep the compatibility axis local

Like Rust editions, a compatibility profile should belong to the unit whose behaviour it controls. Config can govern local parsing/query behaviour. Archive markers govern durable format requirements. HTTP and MCP advertise their own contracts. One global “aha compatibility version” would couple unrelated migrations.

### 5. Prefer a common internal model

Different behaviour profiles should normalise into the same typed internal model whenever possible. That keeps mixed-date Agent histories and newer Workspaces interoperable, following Rust's shared-representation principle.

### 6. Preserve unknown data when converting

Following Kubernetes round-trip rules, a converter must not silently discard fields it does not understand. It must preserve opaque optional data or reject before rewriting. Aha's config `extensions` map and immutable Archive objects provide the initial boundaries for this rule.

### 7. Define support and expiry honestly

Cloudflare's “forever” support is enabled by a centrally operated runtime and carries documentation cost. Aha should not promise that. Before compatibility dates ship, the project must publish:

- the oldest supported behaviour date;
- notice periods for retiring old behaviour;
- whether security fixes can override a pinned date;
- an automated upgrade/test command;
- a rollback or backup window.

Security and integrity requirements may establish a minimum supported date, as Android's target deadlines illustrate.

### 8. Test N/N+1, not only the current release

`scripts/compat-n-minus-one.sh` pins the exact predecessor commit, builds both binaries, materialises a real predecessor snapshot with the current binary, and proves reverse-direction refusal without mutation. Committed fixtures additionally prove:

- an older reader safely accepts additive optional metadata;
- an older writer rejects unknown required features before mutation;
- a newer reader consumes the prior format;
- config updates preserve comments and extension payloads;
- objects round-trip without field loss;
- mixed-version publication remains monotonic;
- an unsupported Workspace can be identified from its external witness without opening it for mutation.

## Decision

Aha uses **schemas and required capabilities for durable data**, **explicit contract identifiers for wire surfaces**, and will use a **compatibility date plus named flags only for intentional behaviour changes**. Dates complement versions; they do not replace them.

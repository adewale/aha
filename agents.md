# Agent Implementation Principles

These are the non-negotiable principles for any change to this repository.
They are enforced by a static test in `internal/testquality/agent_docs_test.go`,
so they cannot silently disappear.

## 1. We always use red-green-refactor TDD

Every change follows the red-green-refactor cycle:

1. **Red** — Write a failing test that pins down the desired behavior. Run it
   and confirm it fails for the right reason before writing any implementation.
2. **Green** — Write the simplest code that makes the test pass.
3. **Refactor** — Clean up the implementation and tests while keeping the suite
   green.

No production code is written without a failing test demanding it. Use
`make verify-quick` (or `scripts/verify.sh quick`) to run the suite locally; CI
runs `scripts/verify.sh ci`.

## 2. We always use correctness-by-construction

We prefer to make invalid states unrepresentable rather than to detect them at
runtime. Concretely (see `docs/correctness-by-construction-spec.md`):

- the invalid value cannot be constructed (opaque types, smart constructors);
- the invalid row cannot be inserted (DB constraints, repository encapsulation);
- the invalid operation is not exposed by the interface (closed variants);
- the invalid sequence is rejected by a model/state-machine test;
- where Go/SQLite cannot make it impossible, the residual risk is explicitly
  tested (property-based tests, fuzzing) and documented.

When a choice exists between a runtime check and a construction-time guarantee,
choose the construction-time guarantee.

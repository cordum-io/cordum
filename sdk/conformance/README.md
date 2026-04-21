# Cordum SDK Conformance Suite

Language-agnostic acceptance tests that prove Cordum's Go, Python, and
TypeScript SDKs behave **identically** against identical gateway traffic.
If the Python SDK returns a typed `ValidationError` where the TypeScript
SDK returns a plain `Error`, the suite catches it. If a retry policy
swallows a `429 Retry-After` in one SDK but surfaces it in another, the
suite catches that too.

## Why this exists

SDK parity is the contract Cordum ships to platform teams. A user who
writes code against the Python SDK and later switches to TypeScript
cannot be punished for that choice — the method names, error shapes,
retry semantics, pagination, and stream consumption MUST be the same.

The suite is the executable version of that promise.

## What ships here

| Path | Purpose |
|------|---------|
| `schema/fixture.schema.json` | JSON Schema contract for every fixture. Validated in CI. |
| `schema/error_envelope.schema.json` | Gateway error envelope shape — locked so drift surfaces as a fixture failure, not a per-SDK bug. |
| `SPEC.md` | Fixture format reference — what a step looks like, what `$any$`/`$timestamp$`/`$uuid$` mean, how `extract` flows data between steps. |
| `fixtures/**/*.json` | The actual test scenarios, grouped by DoD domain (agents/jobs/workflows/policies/audit/auth/errors/idempotency). |
| `simulator/` | Shared Go gateway simulator binary. Serves the operationIds referenced by fixtures from an in-memory store; scenarios program deterministic responses (e.g. "fail twice, then succeed" for retry fixtures). |
| `harness/{go,python,typescript}/` | Per-language test runners. Each spawns the simulator, walks fixtures, instantiates the real SDK package for that language, dispatches through its shipped auth/retry/error layers, diffs responses, emits JUnit XML. |
| `parity/` | Cross-harness diff-parity tests. Invokes each harness's `diff-cli` with identical inputs and asserts byte-identical verdicts — catches the #1 conformance bug class (diff implementations silently disagreeing). |
| `docs/` | `grading.md`, `wildcards.md`, `authoring-fixtures.md`, `known-divergences.md`. |
| `reports/` | Per-run JUnit XML + an aggregated `summary.md` matrix. `reports/*.xml` is `.gitignore`d; `summary.md` is committed so regressions show up in PR diffs. |

## Quickstart

```sh
# Build the simulator binary (once per change).
make -C cordum/sdk/conformance sim

# Run one harness.
make -C cordum/sdk/conformance conformance-go
make -C cordum/sdk/conformance conformance-python
make -C cordum/sdk/conformance conformance-typescript

# Run all three + aggregate.
make -C cordum/sdk/conformance conformance
# → writes reports/summary.md with the fixture × SDK matrix.
```

CI (`.github/workflows/sdk-conformance.yml`) is the canonical grader.
Local runs are advisory — a PR can merge only if CI posts a
green matrix.

## How to add a new fixture

See `docs/authoring-fixtures.md` for the full walk-through. In short:

1. Pick a domain (`fixtures/<domain>/`) or create one.
2. Write a JSON fixture that matches `schema/fixture.schema.json`.
3. Run `ci/validate_fixtures.mjs` locally to catch shape errors.
4. Run `make conformance` to grade every SDK.
5. Only open the PR once every cell of the matrix is `PASS`.

## Phasing notes

This suite landed incrementally, but the final state is stricter:
every language job now **requires** its SDK workspace and fails fast if
the Go/Python/TypeScript package is absent. A green matrix only counts
when all three harnesses exercised their real SDKs.

## Epic rail

> All three SDKs pass 100% of fixtures.

A fixture that fails on any SDK blocks merge. Exceptions require either
(a) a scoped SDK fix, or (b) explicit human sign-off and a
`known-divergence` tag documented in `docs/known-divergences.md`. See
the task plan for rationale.

# Authoring a Conformance Fixture

## When to add one

Add a fixture when you're teaching the SDKs a new contract — a new
endpoint, a new error class, a new retry rule. A fixture is the
executable version of "all three SDKs must behave this way."

Don't add a fixture for:

* Internal SDK refactors (those are covered by SDK unit tests).
* Platform-specific branches (e.g. Node vs browser — handle with
  `runtime:` tags, not duplicate fixtures).
* Gateway bugs — file a separate task first, add the fixture once
  the gateway is fixed.

## Step-by-step

1. Pick or create a domain directory under `fixtures/` — e.g.
   `fixtures/agents/`, `fixtures/errors/`.
2. Name the file with the scenario id: `fixtures/agents/create_and_get.json`.
   The id inside the JSON must match the filename pattern
   `<domain>.<scenario>` (e.g. `"agents.create_and_get"`).
3. Open `SPEC.md` and copy the structure of an existing similar
   fixture as a starting point.
4. Fill in `schemaVersion: 1`, `name`, `description`, `tags`,
   `setup`, `steps`.
5. For opaque values (ids, timestamps, request ids) use the wildcard
   tokens documented in `docs/wildcards.md`.
6. Use `extract` to capture a value from one step into the `$vars.*`
   bag so later steps can reference it (`$vars.agentId` etc).

## Validate locally

```sh
# Shape + operationId-drift check (fast, no network).
node ci/validate_fixtures.mjs \
  --fixtures fixtures \
  --operation-spec ../../docs/api/openapi/cordum-api.yaml

# Full matrix run — every SDK must return PASS.
make sim
make conformance
cat reports/summary.md
```

The row for your new fixture in `reports/summary.md` must read
`PASS` under Go, Python, and TypeScript. If any cell is `FAIL`,
debug the SDK (or the simulator if the fixture's expected behavior
hasn't landed there yet).

## Retry-sensitive fixtures

If your fixture exercises a retry path (rate limiting, idempotency,
retry-exhausted), program the simulator with `X-Conformance-Script`:

| Script name | Effect |
|-------------|--------|
| `rate-limit-once` | simulator returns one 429 with Retry-After, then succeeds |
| `server-500-once` | one 500, then success |
| `server-500-one-shot` | same as above (alternative spelling) |
| `server-500-three-times` | three 500s, then success — used for RetryExhaustedError |

Example:

```json
{
  "kind": "request",
  "operationId": "submitJob",
  "headers": {
    "Idempotency-Key": "conformance-idem-01",
    "X-Conformance-Script": "server-500-once"
  },
  "body": { "topic": "job.echo" },
  "expect": { "status": 202, "bodyMatches": { "$.job_id": "$any$" } }
}
```

## Error-class fixtures

Use `kind: "assert_error"` with an `errorClass` from the taxonomy in
`SPEC.md`. The harness checks the status code matches the taxonomy's
canonical mapping (ValidationError→400, NotFoundError→404, etc).

```json
{
  "kind": "assert_error",
  "operationId": "getAgent",
  "pathParams": { "id": "does-not-exist" },
  "expect": { "status": 404, "errorClass": "NotFoundError" }
}
```

## Stream fixtures

Set `kind: "stream"` and an `eventCount` lower bound. The simulator
emits 3 deterministic SSE frames; if your fixture expects fewer
events, request them with `eventCount`.

## Tagging

Use tags (`"tags": ["agents", "happy-path"]`) to let harnesses skip
incompatible runtimes. `runtime:` tags (`"runtime:go,python,ts-node"`)
opt out of specific harness environments — e.g. streaming fixtures
skip under the TS browser harness where jsdom can't drive
`fetch`-stream responses.

## Opening the PR

Once `make conformance` is green locally, open a PR. CI will:

1. Run `validate-fixtures` (schema + operationId drift).
2. Build the simulator binary.
3. Run every harness in parallel.
4. Aggregate the matrix and post it as a sticky PR comment.
5. Fail the required status check if any SDK failed any fixture.

If CI fails where local passed, check for a simulator binary mismatch
or a stale workspace link to the Python / TypeScript SDK.

# Conformance Fixture Specification

A fixture is a single JSON file that declares a test scenario. Each
SDK harness loads the file, walks its `steps`, and grades every
response against the declared `expect` block. Fixtures are
self-contained and deterministic — they MUST NOT depend on wall
clocks, random IDs, or prior test state.

This document is the normative reference. The companion
`schema/fixture.schema.json` is the machine-checkable version.

## Top-level shape

```jsonc
{
  "schemaVersion": 1,
  "name": "agents.create_and_get",
  "description": "Create an agent identity, then GET it back by id.",
  "tags": ["agents", "happy-path"],
  "setup": {
    "auth": { "kind": "apiKey", "value": "$vars.apiKey" }
  },
  "steps": [
    { "kind": "request", "operationId": "createAgentIdentity", "...": "..." },
    { "kind": "request", "operationId": "getAgentIdentity", "...": "..." }
  ]
}
```

### `schemaVersion` (required, integer)

The fixture-format version. Always `1` for this release. A future v2
schema MUST refuse to load v1 fixtures and vice versa so migrations
stage cleanly.

### `name` (required, string)

A dot-separated stable identifier. Harnesses use it as the JUnit
test-case name and humans use it in error messages. Collisions
across fixtures are a lint failure.

### `description` (optional, string)

One or two sentences. Shown in the CI matrix comment so reviewers
can understand what broke without opening the JSON.

### `tags` (optional, array of strings)

Free-form. A few reserved tags carry runtime behaviour:

- `async:true` — the Python harness drives this fixture via
  `AsyncCordumClient` instead of the sync facade.
- `runtime: ["go", "python", "ts-node"]` — restrict which harnesses
  execute the fixture. Streaming fixtures typically exclude
  `ts-browser` because jsdom cannot drive fetch streams.
- `known-divergence` — a fixture intentionally failing on one SDK
  pending a tracked fix. Requires a row in
  `docs/known-divergences.md` with a linked tracking task.
- `sim-divergence` — the gateway simulator does not yet model the
  real gateway's behaviour for this operation; fixture is skipped
  in CI with explicit human sign-off.

### `setup` (optional, object)

Runs once before the first step. Carries default auth + headers
that every subsequent step inherits unless overridden.

```jsonc
"setup": {
  "auth": { "kind": "apiKey" | "bearer" | "none", "value": "$vars.key" },
  "headers": { "X-Tenant-Id": "$vars.tenant" }
}
```

### `steps` (required, array, non-empty)

Each step is one interaction with the simulator.

## Step shapes

Every step has a `kind` discriminator. Five kinds ship in v1:
`request`, `assert_error`, `sleep`, `stream`, `paginate`.

### `kind: "request"`

The canonical HTTP request. Fails the fixture if the response does
not match `expect`.

```jsonc
{
  "kind": "request",
  "operationId": "createAgentIdentity",
  "auth": { "kind": "apiKey", "value": "$vars.apiKey" },
  "headers": { "Idempotency-Key": "abc-123" },
  "pathParams": { "id": "$vars.agentId" },
  "query": { "limit": 50 },
  "body": { "name": "alpha", "tier": "standard" },
  "expect": {
    "status": 201,
    "headers": { "Location": "$any$" },
    "body": { "id": "$any$", "name": "alpha" },
    "bodyMatches": {
      "$.created_at": "$timestamp$",
      "$.id": "$uuid$"
    }
  },
  "extract": {
    "agentId": "$.id"
  }
}
```

### `kind: "assert_error"`

Identical shape to `request` but the `expect` block carries typed
error assertions instead of `status` + `body`.

```jsonc
{
  "kind": "assert_error",
  "operationId": "getAgentIdentity",
  "pathParams": { "id": "missing-agent" },
  "expect": {
    "errorClass": "NotFoundError",
    "status": 404,
    "fields": {
      "error.code": "not_found",
      "error.details.resource": "agent"
    }
  }
}
```

`errorClass` maps to each SDK's public error type per the parity
table in `docs/wildcards.md`. The class name MUST be identical
across SDKs (e.g. `ValidationError`, `AuthenticationError`,
`RateLimitError`, `RetryExhaustedError`, `NotFoundError`,
`ConflictError`, `ServerError`).

### `kind: "sleep"`

Pauses the driver for `durationMs` milliseconds. Used to exercise
retry backoff windows. Harnesses MUST honour this as monotonic
sleep, not wall-clock.

```jsonc
{ "kind": "sleep", "durationMs": 250 }
```

### `kind: "stream"`

Consumes an SSE stream. The harness reads up to `maxEvents` or
`maxDurationMs` (whichever comes first) and asserts each event
matches the corresponding entry in `expect.events[]`.

```jsonc
{
  "kind": "stream",
  "operationId": "streamWorkflowRun",
  "pathParams": { "runId": "$vars.runId" },
  "maxEvents": 3,
  "maxDurationMs": 5000,
  "expect": {
    "events": [
      { "type": "run.started", "data": { "id": "$any$" } },
      { "type": "step.started", "data": { "name": "step-1" } },
      { "type": "run.completed", "data": { "status": "ok" } }
    ]
  }
}
```

### `kind: "paginate"`

Follows cursor pagination until the response returns no `nextCursor`
or `maxPages` is reached.

```jsonc
{
  "kind": "paginate",
  "operationId": "listAuditEvents",
  "query": { "limit": 10 },
  "maxPages": 3,
  "expect": {
    "pageCount": ">=2",
    "totalItems": ">=15",
    "allItemsMatch": { "$.tenant": "acme" }
  }
}
```

## Wildcards

Grading tokens that match any value conforming to a named shape.
Full semantics in `docs/wildcards.md`.

| Token | Matches |
|-------|---------|
| `$any$` | Any non-null value. |
| `$timestamp$` | ISO-8601 string within ±2s of the step's dispatch time. |
| `$uuid$` | RFC 4122 v4 UUID string. |
| `$request_id$` | Opaque request correlation id; matches `^[A-Za-z0-9_\-]{8,}$`. |
| `$int$` | Any JSON number with no fractional part. |

## Variable bag

Every step shares a scenario-scoped variable bag. `extract` writes
JSONPath matches into the bag under the given key; later steps
reference those values via `$vars.<key>` in `pathParams`, `query`,
`body`, `headers`, or nested `auth.value`.

The bag is initialised with one implicit variable:

- `$vars.tenant` — resolves to the active tenant passed via
  `setup.headers["X-Tenant-Id"]` or the default tenant configured
  in the simulator's scenario script.

## operationId — NOT URL paths

Fixtures reference gateway operationIds, not URL paths. Rationale:

- URL paths drift as deprecated aliases are renamed. OperationIds
  don't.
- The SDKs already expose an operationId → method lookup (Python
  via the `_generated/api` tree walk, TypeScript via the `paths`
  type, Go via a code-generated `operation_map.go`) — fixtures
  consume that lookup directly.
- CI's `validate-fixtures` job asserts every `operationId` appears
  in `docs/api/openapi/cordum-api.yaml` so a rename in the spec
  immediately flags the fixture as broken.

## Error envelope

Every gateway error response MUST match
`schema/error_envelope.schema.json`:

```jsonc
{
  "error": {
    "code": "validation_failed",
    "message": "name is required",
    "details": { "field_errors": [ /* optional */ ] }
  }
}
```

A fixture that accepts a non-conforming error shape is rejected by
`validate-fixtures` before grading begins.

## Determinism

Fixtures MUST be deterministic. Things that are NOT allowed:

- `Date.now()` / `time.Now()` / `datetime.utcnow()` in expected values.
  Use `$timestamp$` instead.
- Randomly generated IDs in expected values. Use `$any$` or `$uuid$`.
- Host-specific paths. Use simulator-relative paths only.
- External network calls. The sim is the only endpoint allowed.

Violations are caught by CI's deterministic-run gate: every fixture
runs twice in the same pipeline and the outputs must be byte-identical
once wildcards are masked.

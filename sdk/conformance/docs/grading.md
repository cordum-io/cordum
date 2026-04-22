# Conformance Grading Semantics

This doc is the language-agnostic reference for the wildcard tokens
and diff semantics every harness implements. The Go, Python, and
TypeScript harnesses each carry their own copy of the diff engine
(Go `harness/go/diff.go`, Python `harness/python/conformance/_diff.py`,
TypeScript `harness/typescript/src/diff.mjs`) — step 9's parity test
cross-runs a shared scenario set through all three and fails if any
harness's verdict differs from the others.

## Wildcard tokens

| Token | Matches |
|-------|---------|
| `$any$` | any value (including null, objects, arrays) |
| `$timestamp$` | string parsable as RFC3339 / ISO-8601 (trailing `Z` ok) |
| `$uuid$` | string matching the UUID v4 shape `^[0-9a-fA-F]{8}-…` |
| `$int$` | integer value; JSON numbers must round-trip to int; booleans rejected |
| `$request_id$` | opaque request id; currently aliased to `$any$` |

A value is a wildcard only when the JSON-decoded *expected* value is
a string that is exactly one of the tokens above. Partial matches
(`"prefix-$any$"`) are literal strings.

## Object comparison

* Expected keys MUST be present in actual with a matching value.
* Actual MAY carry additional keys the expected object doesn't
  mention — that keeps fixtures robust to additive response changes.
* Key iteration is sorted before comparison so error messages are
  deterministic across runs.

## Array comparison

* Order-sensitive; elements compared index by index.
* Length mismatch is a failure.
* Unordered comparison is a planned v2 extension (fixture-level
  `unordered: true` flag). Not implemented in v1.

## Null and primitives

* `null` only matches `null`.
* Strings, numbers, and booleans must equal exactly.
* `true`/`false` are NEVER accepted as `$int$` even though Python
  treats `bool` as an int subclass (explicit guard).

## JSONPath selector

`bodyMatches` keys use a minimal JSONPath subset:

* `$` — root
* `$.field` — dotted property lookup
* `$.items[0]` — array index
* `$.items[0].id` — array index + nested field

Leading `$` is required. Any other syntax raises a path-qualified
error in every harness.

## `$vars.*` placeholder resolution

Strings in step inputs (body / pathParams / query / headers / auth.value)
that start with `$vars.` are substituted from the per-fixture
variable bag. The bag starts each fixture with
`{apiKey, tenant}` and grows via `extract` directives on prior
steps. Missing vars resolve to an empty string so fixtures surface
as auth failures (401) rather than template errors.

## Error-class → HTTP status

`assert_error` fixtures carry an `errorClass` name that maps to the
canonical HTTP status every harness checks:

| Error class | Status |
|-------------|--------|
| AuthenticationError | 401 |
| AuthorizationError  | 403 |
| NotFoundError       | 404 |
| ValidationError     | 400 |
| ConflictError       | 409 |
| RateLimitError      | 429 |
| ServerError         | 500 |
| RetryExhaustedError | — (SDK-shaped; see below) |
| NetworkError        | — (transport-level) |
| TimeoutError        | — (transport-level) |

`RetryExhaustedError`, `NetworkError`, and `TimeoutError` don't map
to a single status; the raw-HTTP harnesses simulate retry semantics
in the driver so a 3×500 sequence surfaces as `RetryExhaustedError`
after the retry budget is exhausted.

## Retry contract

All three harnesses apply the same retry rules, so retry-sensitive
fixtures (`errors/rate_limit_retry_after`, `errors/server_retry_exhausted`,
`idempotency/post_with_key_retries`) grade identically:

* Max 3 attempts.
* Retry on 429, 500, 503, 504.
* Honor `Retry-After` (integer seconds, scaled to ms/10 for fast
  fixtures).
* GET/HEAD/PUT/DELETE are always retryable. POST retries ONLY when
  `Idempotency-Key` is present, mirroring the documented SDK rule.
* `assert_error` steps never retry — they want to observe the first
  failure directly.

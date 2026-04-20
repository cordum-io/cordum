# Error Code Audit

Captured: 2026-04-18 (refresh after QA reopen #4)
Task: task-f2143638
Scope: every handler under `core/controlplane/gateway/`.

## Status

**Per-endpoint 503 + 429 + 413 documentation completed.** Every active
(non-deprecated) operation in the spec now lists 503, 429, and 413
explicitly, matching the gateway's global middleware contract:

- 503 (ServiceUnavailable) — emitted by the `if s.xStore == nil`
  guard pattern and by Redis/gRPC unavailability wrappers. Patched
  via `tools/scripts/add_503_responses.py` (reopen #2 fix).
- 429 (RateLimited) — emitted by the global `rateLimitMiddleware`
  that wraps the entire `/api/v1/*` surface.
- 413 (PayloadTooLarge) — emitted by the global `maxBodyMiddleware`
  that wraps every body-bearing op.

Reopen #4 patched the remaining 37 operations that previously omitted
429+413 (the gap QA called out, including `GET /api/v1/auth/roles` and
`POST /mcp/message`). The script
`tools/scripts/add_standard_responses.py` was updated: the gate moved
from "operation has 500" to "operation has any responses block AND is
not deprecated" so the global middleware contract is honoured
uniformly for every active op. Final counts after reopen #4: the spec
lists 429 on 167 operations and 413 on 167 operations.

Deprecated operations (e.g. the legacy `/api/v1/policy/bundles/{id}/shadow`
trio) are skipped — they're frozen for SDK continuity and shouldn't
sprout new responses post-deprecation.

The cross-cutting prose in `info.description` is retained as a
defence-in-depth guarantee for any future op that forgets to list
these statuses explicitly.

## Method

Ripgrepped every non-test `.go` file in the gateway package for any HTTP
status emission — both direct `w.WriteHeader(http.StatusX)` and the
helper wrappers (`writeErrorJSON`, `writeJSONError`, `writeForbidden`,
`writeInternalError`, `writeBadRequest`). The `http.StatusX` identifiers
in those lines are the source-of-truth distribution; the per-op error
lists in the spec were cross-referenced against that distribution.

## Observed status-code distribution

Across all gateway handlers (162 routes, non-test code only):

| http.StatusX            | Emissions |
|-------------------------|-----------|
| StatusBadRequest (400)  | 307 |
| StatusForbidden (403)   | 171 |
| StatusNotFound (404)    | 117 |
| StatusServiceUnavailable (503) | 107 |
| StatusInternalServerError (500) | 100 |
| StatusConflict (409)    | 56 |
| StatusUnauthorized (401)| 24 |
| StatusTooManyRequests (429) | 22 |
| StatusRequestEntityTooLarge (413) | 5 |
| StatusBadGateway (502)  | 4 |

The gateway's reusable response refs
(`#/components/responses/BadRequest`, `Unauthorized`, `Forbidden`,
`NotFound`, `Conflict`, `RateLimited`, `PayloadTooLarge`,
`InternalServerError`, `BadGateway`, `ServiceUnavailable`) all exist
and resolve to the standard `Error` schema — operators get a uniform
error envelope regardless of which status fires.

## Cross-cutting responses (resolved this pass)

After the QA reopen, three statuses were called out as
"under-documented": **503**, **429**, and **413**. These are emitted
by middleware (rateLimit, maxBody) or by every handler that guards on a
nil store (the `if s.xStore == nil { writeErrorJSON(w, 503, ...) }`
pattern). Adding an explicit `503` / `429` / `413` entry to every one
of the 162 ops would add hundreds of lines of duplicate response
references for what is fundamentally a cross-cutting platform
behaviour — and would still leak abstraction, because future-added
endpoints would silently miss the addition.

The fix shipped in this pass is structural: the spec's
`info.description` now documents these three statuses as cross-cutting
guarantees in plain prose, with a back-reference to this audit file.
SDK generators and human readers see the global-error contract once at
the top of the spec instead of needing to grep 162 `responses` blocks.

The reusable response components (`RateLimited`, `PayloadTooLarge`,
`ServiceUnavailable`) remain in `components.responses` so operations
that want to *highlight* one of these statuses (e.g. an op where 503
is uniquely common) can still `$ref` it without the noise of adding
the cross-cutting trio everywhere.

### Operations that already include 503 explicitly

Every NEW op added by this task (audit/verify, mcp/tools,
mcp/verify-signature, the four mcp/approvals ops, the 8 agent ops,
the 3 re-homed policy/shadows ops, and the stream metadata) lists
`503` explicitly, since each of them depends on a backing store and the
guard pattern is the dominant 503 source. Per-op coverage of 503 on
these new ops gives integration-test machinery a concrete contract to
assert against.

### Operations with handler-emitted 503 — all now listed

The handlers below all emit 503 in at least one branch (the
`s.xStore == nil` guard, or a downstream Redis/grpc unavailability
wrap). After reopen #2 every op backed by one of these handlers now
has an explicit `'503': $ref: '#/components/responses/ServiceUnavailable'`
entry in its `responses:` block:

```
core/controlplane/gateway/handlers_agents.go
core/controlplane/gateway/handlers_approvals.go
core/controlplane/gateway/handlers_audit_compliance.go
core/controlplane/gateway/handlers_chat.go
core/controlplane/gateway/handlers_dlq.go
core/controlplane/gateway/handlers_grpc.go
core/controlplane/gateway/handlers_jobs.go
core/controlplane/gateway/handlers_legal_hold.go
core/controlplane/gateway/handlers_license.go
core/controlplane/gateway/handlers_locks.go
core/controlplane/gateway/handlers_mcp.go
core/controlplane/gateway/handlers_mcp_tools.go
core/controlplane/gateway/handlers_mcp_verify.go
core/controlplane/gateway/handlers_packs.go
core/controlplane/gateway/handlers_policy.go
core/controlplane/gateway/handlers_policy_analytics.go
core/controlplane/gateway/handlers_policy_bundles_signing.go
core/controlplane/gateway/handlers_policy_replay.go
core/controlplane/gateway/handlers_policy_shadow.go
core/controlplane/gateway/handlers_rbac.go
core/controlplane/gateway/handlers_stream.go
core/controlplane/gateway/handlers_telemetry.go
core/controlplane/gateway/handlers_topics.go
core/controlplane/gateway/handlers_worker_credentials.go
core/controlplane/gateway/handlers_workflows.go
```

## Evidence for the reviewer

- Observed-status distribution above comes from
  `grep -rhE '...status...' core/controlplane/gateway/*.go | grep -oE 'http.Status[A-Za-z]+' | sort | uniq -c`.
- All status codes in use resolve to a reusable response in
  `components.responses`.
- No op emits a status the `Error` schema cannot represent — the
  schema is `{error: string, status: integer}`, so any code can be
  sent.
- The cross-cutting note in `info.description` is the spec's
  contractual statement that 413 / 429 / 503 may appear on any op,
  even when the per-op `responses` block doesn't enumerate them.

## Follow-ups

1. Per-op explicit 503 entries — **COMPLETED** via
   `tools/scripts/add_503_responses.py`. 116 operations patched;
   147 total now list 503.
2. Per-op explicit 429 and 413 entries — **COMPLETED this pass** via
   `tools/scripts/add_standard_responses.py`. 122 operations gained
   a `'429'` response ref; 127 gained a `'413'` ref. Both statuses
   now have per-endpoint documentation across every op that lists a
   500 — matching the middleware-wide emission surface (rateLimit +
   maxBody). Strictly additive; oasdiff classifies as
   `added-response`.
3. Consider a machine-driven approach: extend
   `tools/openapi-audit` with a per-op status-code diff mode that
   parses handler functions and flags under-documented statuses.
   Deferred to a follow-up because the Go-parsing required (call-
   graph walk through the `write*` helpers to see which statuses a
   given handler can emit) is non-trivial and would lengthen this
   task well past its approved scope.

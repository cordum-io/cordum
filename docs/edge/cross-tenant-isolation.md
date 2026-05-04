# Edge cross-tenant isolation contract (EDGE-067)

This document records the cross-tenant negative-test sweep that proves
every Edge REST endpoint and the shared WebSocket surface enforce
tenant isolation.

## Audited surfaces

| #   | Method | Path                                                       | Mutating | Tenant-scoped resource              |
|-----|--------|------------------------------------------------------------|----------|-------------------------------------|
| 1   | POST   | `/api/v1/edge/sessions`                                    | yes      | EdgeSession                          |
| 2   | GET    | `/api/v1/edge/sessions`                                    | no       | EdgeSession list                     |
| 3   | GET    | `/api/v1/edge/sessions/{session_id}`                       | no       | EdgeSession                          |
| 4   | POST   | `/api/v1/edge/sessions/{session_id}/heartbeat`             | yes      | EdgeSession                          |
| 5   | POST   | `/api/v1/edge/sessions/{session_id}/end`                   | yes      | EdgeSession                          |
| 6   | POST   | `/api/v1/edge/executions`                                  | yes      | AgentExecution + parent EdgeSession  |
| 7   | GET    | `/api/v1/edge/executions`                                  | no       | AgentExecution list                  |
| 8   | GET    | `/api/v1/edge/executions/{execution_id}`                   | no       | AgentExecution                       |
| 9   | POST   | `/api/v1/edge/executions/{execution_id}/end`               | yes      | AgentExecution                       |
| 10  | GET    | `/api/v1/edge/approvals`                                   | no       | EdgeApproval list                    |
| 11  | GET    | `/api/v1/edge/approvals/{approval_ref}`                    | no       | EdgeApproval                         |
| 12  | POST   | `/api/v1/edge/approvals/{approval_ref}/approve`            | yes      | EdgeApproval state                   |
| 13  | POST   | `/api/v1/edge/approvals/{approval_ref}/reject`             | yes      | EdgeApproval state                   |
| 14  | POST   | `/api/v1/edge/approvals/{approval_ref}/wait`               | no       | EdgeApproval (long-poll)             |
| 15  | POST   | `/api/v1/edge/evaluate`                                    | yes      | session+execution+event+approval     |
| 16  | POST   | `/api/v1/edge/events`                                      | yes      | AgentActionEvent                     |
| 17  | POST   | `/api/v1/edge/events/batch`                                | yes      | AgentActionEvent batch               |
| 18  | GET    | `/api/v1/edge/sessions/{session_id}/events`                | no       | AgentActionEvent list (by session)   |
| 19  | GET    | `/api/v1/edge/executions/{execution_id}/events`            | no       | AgentActionEvent list (by execution) |
| 20  | POST   | `/api/v1/edge/sessions/{session_id}/export`                | yes (artifact) | EdgeSessionExport             |
| 21  | GET (Upgrade) | `/api/v1/stream`                                    | n/a      | Shared WebSocket (post-filter tenant scope) |

## Asserted properties

For every endpoint above, the test sweep asserts at least one of the
following negative properties. Every assertion is enforced through the
production handler chain via `newCrossTenantFixture` (see
`core/controlplane/gateway/cross_tenant_fixture_test.go`), which runs
every request through the same `apiKeyMiddleware → tenantMiddleware →
maxBodyMiddleware` stack used in production.

1. **ID-guess attack.** Tenant A authenticates with its own API key and
   X-Tenant-ID, but targets a resource ID belonging to tenant B. The
   handler must not surface the resource. Preferred outcome is **404
   not_found** (no info leak); 403 is acceptable when an authorization
   check fires before the lookup. Test helper `assertCrossTenantBlocked`
   enforces this.

2. **Header injection.** Tenant A authenticates with its own API key
   but sets `X-Tenant-ID` to tenant B. The gateway derives tenant from
   the auth context (`requireTenantAccess`), not the header, and must
   reject with **403 tenant_access_denied** before any resource lookup.

3. **Body tenant injection.** Body advertises `tenant_id=B` while the
   header is A. The handler chain via `edgeTenantFromRequest` returns
   **403 tenant_mismatch** before touching the store.

4. **Missing X-Tenant-ID.** When the header is absent, the gateway
   fails closed with **400 tenant_required**. There is no implicit
   "anonymous tenant" or wildcard fallback.

5. **List endpoints stay scoped.** When tenant A lists sessions /
   executions / approvals, only tenant A's resources appear in the
   response body. Tenant B's IDs never leak, even via incidental
   substring match.

6. **Batch tenant uniformity.** `/edge/events/batch` rejects the entire
   request when any contained event references a session or execution
   not owned by the request's tenant.

7. **No identifier leak.** Even on a 404 or 403 response, the body
   must not echo the queried tenant B identifier. Test helper
   `(fix).assertNoTenantBLeak` enforces this for every probe.

## WebSocket surface (`/api/v1/stream`)

Today the gateway uses a **shared in-memory dispatch channel**
(`server.eventsCh`) and applies a **per-connection tenant post-filter**
during fan-out (`handlers_stream.go`). Each enqueued event carries a
tenant tag (`enqueueWSEvent` at `core/controlplane/gateway/handlers_stream.go`)
and `enqueueEdgeEvent` (at `core/controlplane/gateway/edge_stream.go`)
fail-closes when an event has a blank or missing tenant.

The post-filter design satisfies the no-leak invariant: tenant A's
connection never receives an event tagged `tenant=B`. This is verified
end-to-end by:

- Existing `TestEdgeEventStreamTenantFilteringAndBusPacketRegression`
  in `edge_stream_test.go`, which exercises the in-memory dispatcher
  with three connections (tenantA, tenantB, cross-tenant viewer) and
  asserts strict per-tenant routing.
- New `TestEdgeCrossTenantWSDoesNotLeakForeignTenantEvents`
  (`handlers_edge_ws_isolation_test.go`), which routes a tenant-B
  edge event through the same dispatch path while a tenant-A
  client is connected and asserts the event is never delivered.
- New `TestEdgeCrossTenantWSEnqueueValidatesTenantTagBeforeDispatch`,
  which asserts the gateway fail-closes on missing/blank tenant
  before dispatch can ever fire.

### Future work (not required for EDGE-067)

The post-filter approach is correct but observably less defensive than
**subscribe-time tenant scoping** (where tenant A's connection would
subscribe only to a tenant-scoped channel and tenant B's events would
never even traverse the same code path). A separate task can move to
per-tenant fan-out lists if the post-filter approach ever proves
contention-bound. The post-filter is preserved for now because it
works within the existing single `eventsCh` shape and keeps the
per-connection client-side machinery simple. No DoS or leakage path
was uncovered during this sweep; the architectural preference is
documented here for awareness.

## Test sweep entry points

A single command runs the full sweep:

```sh
go test -run "TestEdgeCrossTenant" ./core/controlplane/gateway/ -count=1
```

The 29 tests in scope (as of EDGE-067):

- `cross_tenant_fixture_test.go` — fixture smoke (1 test).
- `handlers_edge_sessions_isolation_test.go` — 8 tests (sessions + executions REST).
- `handlers_edge_evaluate_isolation_test.go` — 3 tests (evaluate).
- `handlers_edge_lifecycle_isolation_test.go` — 5 tests (heartbeat, end-*, list-*).
- `handlers_edge_approvals_isolation_test.go` — 5 tests (approval CRUD).
- `handlers_edge_events_isolation_test.go` — 6 tests (events, batch, list, export).
- `handlers_edge_ws_isolation_test.go` — 2 tests (WebSocket).

## Test infrastructure

- `crossTenantFixture` (`core/controlplane/gateway/cross_tenant_fixture_test.go`)
  layers on top of `newEdgeRouteTestServer` and reuses the existing
  `*AsTenant` helpers per the epic rail "reuse-before-build / no parallel
  subsystems".
- Tenant A and tenant B each own a session and execution pre-created
  through the production handler chain.
- `(fix).asAttacker(...)` is the canonical attack pattern: auth as A,
  header A, target B's resource ID. Asserts 404/403 with no body leak.
- `(fix).asAttackerWithHeader(...)` exercises explicit X-Tenant-ID
  header injection.

## Cross-references

- `core/controlplane/gateway/cross_tenant_fixture_test.go` — fixture + smoke test
- `core/controlplane/gateway/handlers_edge_sessions_isolation_test.go`
- `core/controlplane/gateway/handlers_edge_evaluate_isolation_test.go`
- `core/controlplane/gateway/handlers_edge_lifecycle_isolation_test.go`
- `core/controlplane/gateway/handlers_edge_approvals_isolation_test.go`
- `core/controlplane/gateway/handlers_edge_events_isolation_test.go`
- `core/controlplane/gateway/handlers_edge_ws_isolation_test.go`
- `core/controlplane/gateway/handlers_edge_sessions.go` `edgeTenantFromRequest`
- `core/controlplane/gateway/edge_stream.go` `enqueueEdgeEvent`
- `core/controlplane/gateway/handlers_stream.go` `enqueueWSEvent`
- `core/controlplane/gateway/edge_stream_test.go` `TestEdgeEventStreamTenantFilteringAndBusPacketRegression`

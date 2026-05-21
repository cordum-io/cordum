# API Overview

Cordum exposes a REST API and a gRPC service.

For the full gateway REST reference (all registered HTTP routes, request/response
schemas, auth requirements, and error behaviors), see `docs/api-reference.md`.

## REST API

Base URL (local compose): `http://localhost:8081`

Authentication:
- HTTP header: `X-API-Key: <key>` (or JWT via `Authorization: Bearer <token>`)
- Session token: `Authorization: Bearer <session-token>` (from `/api/v1/auth/login`)
- Tenant header: `X-Tenant-ID: <tenant>`
- WebSocket stream: `Sec-WebSocket-Protocol: cordum-api-key, <base64url>` + `?tenant_id=<tenant>`
All endpoints require auth and a tenant header (including `/api/v1/status`).

Auth endpoints (public):
- `GET /api/v1/auth/config` - Get authentication configuration
- `POST /api/v1/auth/login` - Login with user/password or API key
- `GET /api/v1/auth/session` - Validate current session
- `POST /api/v1/auth/logout` - Logout (no-op for stateless auth)
- `POST /api/v1/auth/password` - Change password (authenticated)

User management (admin only):
- `POST /api/v1/users` - Create a new user

Common endpoints:
- Status/stream: `GET /api/v1/status`, WebSocket `GET /api/v1/stream` (CAP BusPacket protojson plus `edge.event` envelopes)
- Jobs: `GET /api/v1/jobs`, `GET /api/v1/jobs/{id}`, `GET /api/v1/jobs/{id}/stream` (WebSocket), `GET /api/v1/jobs/{id}/decisions`, `POST /api/v1/jobs`, `POST /api/v1/jobs/{id}/cancel`, `POST /api/v1/jobs/{id}/remediate`
- Traces: `GET /api/v1/traces/{id}`
- Workflows: `GET/POST /api/v1/workflows`, `GET/DELETE /api/v1/workflows/{id}`
- Runs: `POST /api/v1/workflows/{id}/runs`, `GET /api/v1/workflows/{id}/runs`, `GET /api/v1/workflow-runs`, `GET /api/v1/workflow-runs/{id}`, `GET /api/v1/workflow-runs/{id}/timeline`, `GET /api/v1/workflow-runs/{id}/chat`, `POST /api/v1/workflow-runs/{id}/chat`, `POST /api/v1/workflow-runs/{id}/rerun`, `DELETE /api/v1/workflow-runs/{id}`
- Approvals: `POST /api/v1/approvals/{job_id}/approve`, `POST /api/v1/approvals/{job_id}/reject`
- Policy: `POST /api/v1/policy/evaluate`, `POST /api/v1/policy/simulate`, `POST /api/v1/policy/explain`, `GET /api/v1/policy/snapshots`, `GET/PUT /api/v1/policy/bundles/{id}`, `POST /api/v1/policy/bundles/{id}/simulate`, `POST /api/v1/policy/publish`, `POST /api/v1/policy/rollback`, `GET /api/v1/policy/audit`
- Config: `GET /api/v1/config?scope=...&scope_id=...`, `POST /api/v1/config`, `GET /api/v1/config/effective`
- Schemas: `GET/POST /api/v1/schemas`, `GET /api/v1/schemas/{id}`, `DELETE /api/v1/schemas/{id}`
- Packs: `GET /api/v1/packs`, `GET /api/v1/packs/{id}`, `POST /api/v1/packs/install`, `POST /api/v1/packs/{id}/verify`, `POST /api/v1/packs/{id}/uninstall`
- Marketplace: `GET /api/v1/marketplace/packs`, `POST /api/v1/marketplace/install`
- Artifacts: `POST /api/v1/artifacts`, `GET /api/v1/artifacts/{ptr}`
- Locks: `GET /api/v1/locks`, `POST /api/v1/locks/acquire`, `POST /api/v1/locks/release`, `POST /api/v1/locks/renew`
- DLQ: `GET /api/v1/dlq`, `GET /api/v1/dlq/page`, `DELETE /api/v1/dlq/{job_id}`, `POST /api/v1/dlq/{job_id}/retry`
- Memory pointers: `GET /api/v1/memory?ptr=...`

### Edge agent governance APIs
Tags: edge, agent-governance, compliance-firewall

Cordum Edge P0 routes live under `/api/v1/edge/*` and use the standard Gateway
auth headers plus `X-Tenant-ID`. They provide tenant-scoped agent sessions,
executions, action events, policy evaluation, approvals, and evidence export.
Edge actions are not Cordum Jobs unless linked to a real production
`job_id`/workflow run.

Route families:
- Sessions: `POST/GET /api/v1/edge/sessions`, `GET /api/v1/edge/sessions/{session_id}`, `POST /api/v1/edge/sessions/{session_id}/heartbeat`, `POST /api/v1/edge/sessions/{session_id}/end`
- Executions: `POST /api/v1/edge/executions`, `GET /api/v1/edge/executions/{execution_id}`, `POST /api/v1/edge/executions/{execution_id}/end`
- Evaluate: `POST /api/v1/edge/evaluate`
- Approvals: `GET /api/v1/edge/approvals`, `GET /api/v1/edge/approvals/{approval_ref}`, `POST /api/v1/edge/approvals/{approval_ref}/approve`, `POST /api/v1/edge/approvals/{approval_ref}/wait`, `POST /api/v1/edge/approvals/{approval_ref}/reject`
- Events: `POST /api/v1/edge/events`, `POST /api/v1/edge/events/batch`, `GET /api/v1/edge/sessions/{session_id}/events`, `GET /api/v1/edge/executions/{execution_id}/events`
- Export: `POST /api/v1/edge/sessions/{session_id}/export`

Destructive action gates require resolved approval provenance in addition to
the approval store: `EventEdgeApprovalResolved` / `edge.approval_resolved` with
approved decision and exact tenant, `approval_ref`, and `action_hash` matches.
Requested-only approval events are not sufficient. See
[Edge API reference](edge/api.md) for request/response shapes, stable error
codes, idempotency, approval visibility, and artifact export behavior. The
canonical schema remains [OpenAPI](api/openapi/cordum-api.yaml).

### Job event streaming (WebSocket)
Tags: websocket, jobs, streaming

Use `GET /api/v1/jobs/{id}/stream` to receive job events for a single job. The
connection uses the same API key WebSocket protocol as `/api/v1/stream` and
requires `X-Tenant-ID` (or `?tenant_id=`) for tenant scoping.

Use `GET /api/v1/stream` for the global dashboard stream. It emits the existing
CAP BusPacket protojson messages and dedicated Edge action envelopes with
`type: "edge.event"`, `tenant_id`, `session_id`, `execution_id`, and `event`
payloads matching `core/edge/schema/agent_action_event.schema.json`. Generic
Edge events are not delivered to `/api/v1/jobs/{id}/stream`.

## gRPC API

The gateway exposes `CordumApi` (see `core/protocol/proto/v1/api.proto`).
Use the generated SDK under `sdk/` or the protobufs in `core/protocol/pb/v1`.

For message types and envelopes, see `docs/AGENT_PROTOCOL.md` and the CAP repo
(`github.com/cordum-io/cap/v2`).

## OpenAPI (canonical)

Validate the canonical HTTP OpenAPI spec:

```bash
make openapi
```

The source of truth is `docs/api/openapi/cordum-api.yaml`. When docs are
published, the Swagger UI lives at `docs/api/openapi/` and loads that single
canonical spec.

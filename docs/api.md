# API Overview

Cordum exposes a REST API and a gRPC service.

## REST API

Base URL (local compose): `http://localhost:8081`

Authentication:
- HTTP header: `X-API-Key: <key>`
- WebSocket stream: `Sec-WebSocket-Protocol: cordum-api-key, <base64url>`

Common endpoints:
- Status/stream: `GET /api/v1/status`, WebSocket `GET /api/v1/stream`
- Jobs: `GET /api/v1/jobs`, `GET /api/v1/jobs/{id}`, `GET /api/v1/jobs/{id}/decisions`, `POST /api/v1/jobs`, `POST /api/v1/jobs/{id}/cancel`, `POST /api/v1/jobs/{id}/remediate`
- Traces: `GET /api/v1/traces/{id}`
- Workflows: `GET/POST /api/v1/workflows`, `GET/DELETE /api/v1/workflows/{id}`
- Runs: `POST /api/v1/workflows/{id}/runs`, `GET /api/v1/workflows/{id}/runs`, `GET /api/v1/workflow-runs`, `GET /api/v1/workflow-runs/{id}`, `GET /api/v1/workflow-runs/{id}/timeline`, `POST /api/v1/workflow-runs/{id}/rerun`, `DELETE /api/v1/workflow-runs/{id}`
- Approvals: `POST /api/v1/workflows/{id}/runs/{run_id}/steps/{step_id}/approve`, `POST /api/v1/approvals/{job_id}/approve`, `POST /api/v1/approvals/{job_id}/reject`
- Policy: `POST /api/v1/policy/evaluate`, `POST /api/v1/policy/simulate`, `POST /api/v1/policy/explain`, `GET /api/v1/policy/snapshots`, `GET/PUT /api/v1/policy/bundles/{id}`, `POST /api/v1/policy/bundles/{id}/simulate`, `POST /api/v1/policy/publish`, `POST /api/v1/policy/rollback`, `GET /api/v1/policy/audit`
- Config: `GET /api/v1/config?scope=...&scope_id=...`, `POST /api/v1/config`, `GET /api/v1/config/effective`
- Schemas: `GET/POST /api/v1/schemas`, `GET /api/v1/schemas/{id}`, `DELETE /api/v1/schemas/{id}`
- Packs: `GET /api/v1/packs`, `GET /api/v1/packs/{id}`, `POST /api/v1/packs/install`, `POST /api/v1/packs/{id}/verify`, `POST /api/v1/packs/{id}/uninstall`
- Marketplace: `GET /api/v1/marketplace/packs`, `POST /api/v1/marketplace/install`
- Artifacts: `POST /api/v1/artifacts`, `GET /api/v1/artifacts/{ptr}`
- Locks: `GET /api/v1/locks`, `POST /api/v1/locks/acquire`, `POST /api/v1/locks/release`, `POST /api/v1/locks/renew`
- DLQ: `GET /api/v1/dlq`, `GET /api/v1/dlq/page`, `DELETE /api/v1/dlq/{job_id}`, `POST /api/v1/dlq/{job_id}/retry`
- Memory pointers: `GET /api/v1/memory?ptr=...`

## gRPC API

The gateway exposes `CordumApi` (see `core/protocol/proto/v1/api.proto`).
Use the generated SDK under `sdk/` or the protobufs in `core/protocol/pb/v1`.

For message types and envelopes, see `docs/AGENT_PROTOCOL.md` and the CAP repo
(`github.com/cordum-io/cap/v2`).

## OpenAPI (generated)

Generate OpenAPI specs from the protobufs:

```bash
make openapi
```

Output is written to `docs/api/openapi/` (merged as `cordum.swagger.json`).
When docs are published, the Swagger UI lives at `docs/api/openapi/` and the
raw spec at `docs/api/openapi/cordum.swagger.json`.

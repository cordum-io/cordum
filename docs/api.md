# API Overview

Cordum exposes a REST API and a gRPC service.

## REST API

Base URL (local compose): `http://localhost:8081`

Authentication:
- HTTP header: `X-API-Key: <key>`
- WebSocket stream: `Sec-WebSocket-Protocol: cordum-api-key, <base64url>`

Common endpoints:
- Workflows: `GET/POST /api/v1/workflows`, `GET/DELETE /api/v1/workflows/{id}`
- Workflow runs: `POST /api/v1/workflows/{id}/runs`, `GET /api/v1/workflow-runs/{run_id}`, `POST /api/v1/workflow-runs/{run_id}/rerun`
- Jobs: `GET /api/v1/jobs`, `GET /api/v1/jobs/{id}`, `POST /api/v1/jobs`, `POST /api/v1/jobs/{id}/cancel`, `POST /api/v1/jobs/{id}/remediate`
- Approvals: `POST /api/v1/workflows/{id}/runs/{run_id}/steps/{step}/approve`, `POST /api/v1/approvals/{job_id}/approve`, `POST /api/v1/approvals/{job_id}/reject`
- Policy: `POST /api/v1/policy/evaluate`, `POST /api/v1/policy/simulate`, `GET /api/v1/policy/snapshots`
- Config: `GET/POST /api/v1/config/{scope}/{id}`
- Schemas: `GET/POST /api/v1/schemas`, `GET /api/v1/schemas/{id}`
- Packs: `GET/POST /api/v1/packs`, `GET /api/v1/packs/{id}`
- Marketplace: `GET /api/v1/marketplace/packs`, `POST /api/v1/marketplace/install`
- Artifacts: `GET/POST /api/v1/artifacts`
- Locks: `POST /api/v1/locks/acquire`, `POST /api/v1/locks/release`
- DLQ: `GET /api/v1/dlq`, `POST /api/v1/dlq/replay`

## gRPC API

The gateway exposes `CordumApi` (see `core/protocol/proto/v1/api.proto`).
Use the generated SDK under `sdk/` or the protobufs in `core/protocol/pb/v1`.

For message types and envelopes, see `docs/AGENT_PROTOCOL.md` and the CAP repo
(`github.com/cordum-io/cap/v2`).

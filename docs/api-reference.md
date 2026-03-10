# REST API Reference (Gateway)

Source of truth: `core/controlplane/gateway/gateway_core.go` route registration and handler implementations in `core/controlplane/gateway/*`.

## Related Specs and Protocol Docs

- OpenAPI (generated output, current protobuf-driven subset): `docs/api/openapi/README.md`
- OpenAPI merged spec artifact: `docs/api/openapi/cordum.swagger.json`
- REST + gRPC overview: `docs/api.md`
- gRPC service definition (`CordumApi`): `core/protocol/proto/v1/api.proto`

This file is the comprehensive REST route reference; OpenAPI output currently covers only the generated subset.

## gRPC Tenant Resolution

gRPC methods resolve the caller's tenant using these rules (in order):

1. **Scoped key** (auth context has tenant): request `org_id` must match auth tenant, or `PermissionDenied` is returned. Empty `org_id` defaults to auth tenant.
2. **Cross-tenant key** (`AllowCrossTenant=true`): any `org_id` is accepted.
3. **Unscoped key** (auth context has no tenant): `org_id` must be empty or match the server default tenant. Arbitrary tenant selection returns `PermissionDenied`.
4. **No auth context**: falls back to server default tenant.

SDK/CLI callers using unscoped API keys should omit `org_id` or set it to the server default. Scoped keys should match the configured tenant.

## Base URL and Servers

- Gateway HTTP (default): `http://localhost:8081`
- Metrics server (default): `http://localhost:9092/metrics`

## Authentication and Tenant Rules

### Public endpoints

These paths are public (no API key required), controlled by middleware + auth provider:

- `GET /api/v1/auth/config`
- `POST /api/v1/auth/login`

Also public:

- `GET /health`
- Non-`/api/*` paths (for example `/mcp/*`, subject to MCP-specific auth wrappers)

### Auth for protected `/api/*` routes

Use one of:

- `X-API-Key: <key>`
- `Authorization: Bearer <jwt-or-session-token>`

Tenant header is required on protected `/api/*` routes:

- `X-Tenant-ID: <tenant>`

Tenant access is enforced by middleware and per-handler checks.

### WebSocket auth

For websocket upgrades, API key can be provided as subprotocol:

- `Sec-WebSocket-Protocol: cordum-api-key, <base64url(api_key)>`

## Common Error Format

Most handlers return:

```json
{
  "error": "human-readable message",
  "status": 400
}
```

Common statuses:

- `400` invalid request/body/params
- `401` unauthorized
- `403` forbidden / tenant access denied
- `404` not found
- `409` state conflict/idempotency conflict
- `413` payload too large
- `429` rate limited
- `500` internal error
- `502` upstream service error
- `503` dependent service unavailable

## Rate Limiting

- Public routes use a dedicated public limiter.
- Protected API routes use tenant/IP-based limiter.
- On limit breach: `429` with `{ "error": "rate limited", "status": 429 }`.

## Idempotency

Used by job/workflow submission flows.

Accepted keys:

- `Idempotency-Key`
- `X-Idempotency-Key`
- query: `idempotency_key` or `idempotency-key`

---

## 1. Auth and Session

### GET `/api/v1/auth/config`

- Auth: public
- Request body: none
- Response: auth capability object

```json
{
  "password_enabled": true,
  "user_auth_enabled": true,
  "saml_enabled": false,
  "saml_enterprise": false,
  "saml_login_url": "",
  "saml_metadata_url": "",
  "session_ttl": "24h",
  "require_rbac": true,
  "require_principal": false,
  "default_tenant": "default",
  "oidc_enabled": false,
  "oidc_issuer": ""
}
```

### POST `/api/v1/auth/login`

- Auth: public
- Request schema (`AuthLoginRequest`):

```json
{
  "username": "admin",
  "password": "<password-or-api-key>",
  "tenant": "default"
}
```

- Response schema (`AuthLoginResponse`):

```json
{
  "token": "session-... or masked-api-key",
  "expires_at": "2026-02-13T10:00:00Z",
  "user": {
    "id": "user-id",
    "username": "admin",
    "email": "admin@example.com",
    "display_name": "Admin",
    "tenant": "default",
    "roles": ["admin"],
    "source": "user|api_key",
    "created_at": "...",
    "updated_at": "...",
    "last_login_at": "..."
  }
}
```

- Errors: `400`, `401`, `403`, `429`, `500`

### GET `/api/v1/auth/session`

- Auth: required
- Request body: none
- Response: same shape as login response
- Errors: `401`

### POST `/api/v1/auth/logout`

- Auth: required
- Request body: none
- Response: `204 No Content`

### POST `/api/v1/auth/password`

- Auth: required (user auth mode)
- Request schema (`ChangePasswordRequest`):

```json
{
  "current_password": "old",
  "new_password": "new"
}
```

- Response: `204 No Content`
- Errors: `400`, `401`, `404`

### Example: login + session check

```bash
curl -sS -X POST http://localhost:8081/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"YOUR_API_KEY","tenant":"default"}'

curl -sS http://localhost:8081/api/v1/auth/session \
  -H 'X-API-Key: YOUR_API_KEY' \
  -H 'X-Tenant-ID: default'
```

---

## 2. User Management

All endpoints below require admin role and tenant-scoped access.

### POST `/api/v1/users`

- Auth: required + admin
- Request schema (`CreateUserRequest`):

```json
{
  "username": "alice",
  "email": "alice@example.com",
  "password": "strong-password",
  "tenant": "default",
  "role": "user"
}
```

- Response: `201` + `AuthUser`
- Errors: `400`, `401`, `403`, `409`

### GET `/api/v1/users`

- Auth: required + admin
- Request body: none
- Response:

```json
{
  "items": [
    {
      "id": "...",
      "username": "alice",
      "email": "alice@example.com",
      "display_name": "",
      "tenant": "default",
      "roles": ["user"],
      "created_at": "...",
      "updated_at": "..."
    }
  ]
}
```

- Errors: `400`, `401`, `403`, `500`

### PUT `/api/v1/users/{id}`

- Auth: required + admin
- Request schema:

```json
{
  "email": "alice@new.example.com",
  "display_name": "Alice",
  "roles": ["admin"]
}
```

- Response: updated `AuthUser`
- Errors: `400`, `401`, `403`, `404`, `500`

### DELETE `/api/v1/users/{id}`

- Auth: required + admin
- Request body: none
- Response: `204`
- Errors: `400`, `401`, `403`, `404`, `500`

### POST `/api/v1/users/{id}/password`

- Auth: required + admin
- Request schema:

```json
{
  "password": "new-strong-password"
}
```

- Response: `204`
- Errors: `400`, `401`, `403`, `404`, `500`

### Example: create user

```bash
curl -sS -X POST http://localhost:8081/api/v1/users \
  -H 'X-API-Key: YOUR_API_KEY' \
  -H 'X-Tenant-ID: default' \
  -H 'Content-Type: application/json' \
  -d '{"username":"alice","password":"Secret123!","role":"user"}'
```

---

## 3. API Key Management

All endpoints require admin role.

### GET `/api/v1/auth/keys`

- Auth: required + admin
- Response:

```json
{
  "items": [
    {
      "id": "key-id",
      "name": "ci-key",
      "prefix": "abc123",
      "scopes": ["jobs:write"],
      "createdAt": "...",
      "lastUsed": "...",
      "usageCount": 12,
      "expiresAt": "..."
    }
  ]
}
```

- Errors: `403`, `500`, `503`

### POST `/api/v1/auth/keys`

- Auth: required + admin
- Request schema:

```json
{
  "name": "ci-key",
  "scopes": ["jobs:write", "workflows:read"],
  "expiresAt": "2026-12-31T23:59:59Z"
}
```

- Response: `201`

```json
{
  "key": {
    "id": "...",
    "name": "ci-key",
    "prefix": "...",
    "scopes": ["jobs:write"],
    "createdAt": "...",
    "usageCount": 0
  },
  "secret": "raw-api-key-once"
}
```

- Errors: `400`, `403`, `500`, `503`

### DELETE `/api/v1/auth/keys/{id}`

- Auth: required + admin
- Response: `204`
- Errors: `400`, `403`, `404`, `500`, `503`

### Example: create API key

```bash
curl -sS -X POST http://localhost:8081/api/v1/auth/keys \
  -H 'X-API-Key: YOUR_API_KEY' \
  -H 'X-Tenant-ID: default' \
  -H 'Content-Type: application/json' \
  -d '{"name":"ci-key","scopes":["jobs:write"]}'
```

---

## 4. Jobs and Traces

### POST `/api/v1/jobs`

- Auth: required
- Request schema (`submitJobRequest`):

```json
{
  "prompt": "Generate plan",
  "topic": "job.default",
  "adapter_id": "openai:gpt-4.1",
  "priority": "interactive",
  "context": {},
  "memory_id": "run:abc",
  "context_mode": "balanced",
  "tenant_id": "default",
  "org_id": "default",
  "team_id": "team-a",
  "project_id": "proj-a",
  "principal_id": "user-123",
  "actor_id": "user-123",
  "actor_type": "human",
  "idempotency_key": "job-abc-001",
  "pack_id": "pack.example",
  "capability": "summarize",
  "risk_tags": ["pii"],
  "requires": ["approval"],
  "labels": {"k":"v"},
  "max_input_tokens": 8000,
  "allow_summarization": true,
  "allow_retrieval": true,
  "tags": ["demo"],
  "max_output_tokens": 1024,
  "max_total_tokens": 4096,
  "deadline_ms": 30000
}
```

- Response:

```json
{
  "job_id": "uuid",
  "trace_id": "uuid"
}
```

- Errors: `400`, `403`, `409`, `429`, `503`

### GET `/api/v1/jobs`

- Auth: required
- Query:
  - `limit`, `cursor`, `state`, `topic`, `tenant`, `team`, `trace_id`, `updated_after`, `updated_before`
- Response:

```json
{
  "items": [
    {
      "id": "job-id",
      "state": "PENDING",
      "topic": "job.default",
      "tenant": "default",
      "updated_at": 1739400000000000
    }
  ],
  "next_cursor": 1739399999999999
}
```

- Errors: `403`, `500`, `503`

### GET `/api/v1/jobs/{id}`

- Auth: required + tenant access
- Response (abridged):

```json
{
  "id": "job-id",
  "state": "RUNNING",
  "trace_id": "trace-id",
  "context_ptr": "redis://ctx:job-id",
  "result_ptr": "redis://res:job-id",
  "result": {},
  "topic": "job.default",
  "tenant": "default",
  "capability": "summarize",
  "risk_tags": ["pii"],
  "requires": ["approval"],
  "output_safety": {},
  "labels": {},
  "workflow_id": "...",
  "run_id": "...",
  "step_id": "..."
}
```

- Errors: `400`, `403`, `404`

### GET `/api/v1/jobs/{id}/decisions`

- Auth: required + tenant access
- Query: `limit`
- Response: array of safety decision records for the job
- Errors: `400`, `403`, `500`, `503`

### POST `/api/v1/jobs/{id}/cancel`

- Auth: required + admin + tenant access
- Request body: none
- Response:

```json
{
  "id": "job-id",
  "state": "CANCELLED"
}
```

- Errors: `400`, `403`, `404`, `500`

### POST `/api/v1/jobs/{id}/remediate`

- Auth: required + admin + tenant access
- Request schema:

```json
{
  "remediation_id": "optional-if-single-choice"
}
```

- Response:

```json
{
  "job_id": "new-job-id",
  "trace_id": "trace-id"
}
```

- Errors: `400`, `403`, `404`, `409`, `502`, `503`

### GET `/api/v1/traces/{id}`

- Auth: required
- Response: list of trace job records visible to caller tenant
- Errors: `400`, `500`

### Example: submit + fetch job

```bash
curl -sS -X POST http://localhost:8081/api/v1/jobs \
  -H 'X-API-Key: YOUR_API_KEY' \
  -H 'X-Tenant-ID: default' \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: demo-job-001' \
  -d '{"prompt":"hello","topic":"job.default"}'

curl -sS http://localhost:8081/api/v1/jobs/JOB_ID \
  -H 'X-API-Key: YOUR_API_KEY' \
  -H 'X-Tenant-ID: default'
```

---

## 5. DLQ (Dead Letter Queue)

All DLQ endpoints require admin role.

### GET `/api/v1/dlq`

- Auth: required + admin
- Query: `limit`
- Response: array of DLQ entries
- Errors: `403`, `500`, `503`

### GET `/api/v1/dlq/page`

- Auth: required + admin
- Query: `limit`, `cursor`
- Response:

```json
{
  "items": [
    {
      "job_id": "job-id",
      "topic": "job.default",
      "status": "JOB_STATUS_FAILED",
      "reason": "error text",
      "created_at": "..."
    }
  ],
  "next_cursor": 1739400000
}
```

- Errors: `403`, `500`, `503`

### DELETE `/api/v1/dlq/{job_id}`

- Auth: required + admin + tenant access
- Response: `204`
- Errors: `400`, `403`, `500`, `503`

### POST `/api/v1/dlq/{job_id}/retry`

- Auth: required + admin + tenant access
- Request body: none
- Response:

```json
{
  "job_id": "new-retry-job-id"
}
```

- Errors: `400`, `403`, `404`, `500`, `503`

### Example: retry DLQ entry

```bash
curl -sS -X POST http://localhost:8081/api/v1/dlq/JOB_ID/retry \
  -H 'X-API-Key: YOUR_API_KEY' \
  -H 'X-Tenant-ID: default'
```

---

## Remaining Endpoint Groups

The remaining groups (workflows, policy bundles, config, packs/marketplace, locks, schemas, artifacts, websocket stream, MCP) are documented below in this same file.

## 6. Workflows and Runs

### Workflow definitions

### GET `/api/v1/workflows`

- Auth: required
- Query: `org_id`
- Response: array of workflow definitions
- Errors: `403`, `500`, `503`

### POST `/api/v1/workflows`

- Auth: required + admin
- Request schema (`createWorkflowRequest`):

```json
{
  "id": "optional-uuid",
  "org_id": "default",
  "team_id": "team-a",
  "name": "Deploy workflow",
  "description": "...",
  "version": "1.0.0",
  "timeout_sec": 600,
  "created_by": "user-1",
  "input_schema": {},
  "parameters": [],
  "steps": {},
  "config": {}
}
```

- Response: `201` + `{ "id": "workflow-id" }`
- Errors: `400`, `403`, `500`, `503`

### GET `/api/v1/workflows/{id}`

- Auth: required + tenant access
- Response: workflow definition JSON
- Errors: `400`, `403`, `404`, `503`

### DELETE `/api/v1/workflows/{id}`

- Auth: required + admin + tenant access
- Response: `204`
- Errors: `400`, `403`, `404`, `500`, `503`

### Run management

### POST `/api/v1/workflows/{id}/runs`

- Auth: required + admin
- Query: `org_id`, `team_id`, `dry_run`
- Body: arbitrary workflow input object
- Idempotency key supported
- Response: `{ "run_id": "run-uuid" }`
- Errors: `400`, `403`, `404`, `409`, `429`, `500`, `503`

### GET `/api/v1/workflows/{id}/runs`

- Auth: required + tenant access
- Response: array of run objects
- Errors: `400`, `403`, `500`, `503`

### GET `/api/v1/workflow-runs`

- Auth: required
- Query: `limit`, `cursor`, `status`, `workflow_id`, `org_id`, `team_id`, `updated_after`, `updated_before`
- Response:

```json
{
  "items": [ { "id": "run-id", "workflow_id": "wf-id", "status": "RUNNING" } ],
  "next_cursor": 1739400000
}
```

### GET `/api/v1/workflow-runs/{id}`

- Auth: required + tenant access
- Response: run object enriched with active delay timers (if any)

```json
{
  "id": "run-id",
  "workflow_id": "wf-id",
  "status": "RUNNING",
  "timers": [
    {
      "workflow_id": "wf-id",
      "run_id": "run-id",
      "fires_at": "2026-02-20T12:30:00Z",
      "remaining_ms": 28500
    }
  ]
}
```

The `timers` array is omitted when no active delay timers exist for the run. Timer lookup is best-effort — failures are silently ignored to avoid degrading the endpoint.

- Errors: `400`, `403`, `404`, `503`

### GET `/api/v1/workflow-runs/{id}/timeline`

- Auth: required + tenant access
- Query: `limit`
- Response: array of timeline events
- Errors: `400`, `403`, `500`, `503`

### DELETE `/api/v1/workflow-runs/{id}`

- Auth: required + admin + tenant access
- Response: `204`
- Errors: `400`, `403`, `404`, `500`, `503`

### POST `/api/v1/workflow-runs/{id}/rerun`

- Auth: required + admin + tenant access
- Request schema:

```json
{
  "from_step": "optional-step-id",
  "dry_run": false
}
```

- Response: `{ "run_id": "new-run-id" }`
- Errors: `400`, `403`, `404`, `429`, `503`

### POST `/api/v1/workflows/{id}/dry-run`

- Auth: required + admin + tenant access
- Request schema:

```json
{
  "input": {},
  "environment": "staging"
}
```

- Response schema (`dryRunResponse`):

```json
{
  "workflow_id": "wf-id",
  "steps": [
    {
      "step_id": "step-a",
      "step_name": "Step A",
      "step_type": "worker",
      "decision": "ALLOW",
      "reason": "...",
      "rule_id": "rule-1"
    }
  ]
}
```

- Errors: `400`, `403`, `404`, `503`

### Chat on runs

### GET `/api/v1/workflow-runs/{id}/chat`

- Auth: required + tenant access
- Query: `limit`, `cursor`
- Response (`chatResponse`):

```json
{
  "items": [
    {
      "id": "msg-id",
      "run_id": "run-id",
      "role": "user|agent|system",
      "content": "text",
      "step_id": "...",
      "job_id": "...",
      "agent_id": "...",
      "agent_name": "...",
      "created_at": "2026-02-13T09:00:00Z",
      "metadata": {}
    }
  ],
  "next_cursor": 10
}
```

### POST `/api/v1/workflow-runs/{id}/chat`

- Auth: **admin** or **operator** role required + tenant access
- Request:

```json
{
  "content": "Please continue",
  "role": "user",
  "step_id": "optional",
  "job_id": "optional",
  "agent_id": "optional",
  "agent_name": "optional",
  "metadata": {}
}
```

- **Role field rules:**
  - Allowed values: `user`, `agent`, `assistant` (alias for agent), `system`. Unrecognized values return `400`.
  - Omitting `role` defaults to `user`.
  - Only **admin** callers may set `role` to `agent` or `system`. Operator callers are forced to `user`.
- Response: created chat message (same shape as `items[]`)
- Errors: `400`, `403`, `404`, `500`, `503`

### POST `/api/v1/workflows/{id}/runs/{run_id}/cancel`

- Auth: required + admin + tenant access
- Request body: none
- Response: `204`
- Errors: `400`, `403`, `409`, `503`

### Example: start workflow run

```bash
curl -sS -X POST http://localhost:8081/api/v1/workflows/WF_ID/runs \
  -H 'X-API-Key: YOUR_API_KEY' \
  -H 'X-Tenant-ID: default' \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: run-001' \
  -d '{"input":"hello"}'
```

---

## 7. Approvals (Job Approval Queue)

### GET `/api/v1/approvals`

- Auth: required + admin
- Query: `limit`, `cursor`
- Response:

```json
{
  "items": [
    {
      "job": { "id": "job-id", "state": "APPROVAL" },
      "decision": "REQUIRE_APPROVAL",
      "policy_snapshot": "cfg:...",
      "policy_rule_id": "rule-1",
      "policy_reason": "...",
      "constraints": {},
      "job_hash": "...",
      "approval_required": true,
      "approval_ref": "job-id"
    }
  ],
  "next_cursor": 1739400000000000
}
```

### POST `/api/v1/approvals/{job_id}/approve`

- Auth: required + admin + tenant access
- Request:

```json
{
  "reason": "approved by on-call",
  "note": "ticket INC-123"
}
```

- Response:

```json
{
  "job_id": "job-id",
  "trace_id": "trace-id"
}
```

- Errors: `400`, `403`, `404`, `409`, `502`, `503`

### POST `/api/v1/approvals/{job_id}/reject`

- Auth: required + admin + tenant access
- Request:

```json
{
  "reason": "policy violation",
  "note": "missing human approval"
}
```

- Response:

```json
{ "job_id": "job-id" }
```

- Errors: `400`, `403`, `404`, `409`, `503`

Note: There are no `GET /api/v1/approvals/{id}` or `PUT /api/v1/approvals/{id}` routes in current gateway route registration.

---

## 8. Policy Evaluation and Bundles

### Policy evaluation endpoints

### POST `/api/v1/policy/evaluate`
### POST `/api/v1/policy/simulate`
### POST `/api/v1/policy/explain`

- Auth: required
- Request schema (`policyCheckRequest`):

```json
{
  "job_id": "job-id",
  "topic": "job.default",
  "tenant": "default",
  "org_id": "default",
  "team_id": "team-a",
  "workflow_id": "wf-id",
  "step_id": "step-id",
  "principal_id": "user-1",
  "priority": "interactive",
  "estimated_cost": 0.1,
  "budget": {
    "max_input_tokens": 8000,
    "max_output_tokens": 1024,
    "max_total_tokens": 4096,
    "deadline_ms": 30000
  },
  "labels": {"k":"v"},
  "memory_id": "run:abc",
  "effective_config": {},
  "meta": {
    "tenant_id": "default",
    "actor_id": "user-1",
    "actor_type": "human",
    "idempotency_key": "abc",
    "capability": "summarize",
    "risk_tags": ["pii"],
    "requires": ["approval"],
    "pack_id": "pack.example",
    "labels": {}
  }
}
```

- Response: protobuf-JSON `PolicyCheckResponse` from safety kernel
- Errors: `400`, `403`, `502`, `503`

### GET `/api/v1/policy/snapshots`

- Auth: required
- Response: protobuf-JSON `ListSnapshotsResponse`
- Errors: `502`, `503`

### Policy bundle management (admin)

### GET `/api/v1/policy/rules`

- Query: `include_disabled=true|false`
- Response:

```json
{
  "items": [ { "id": "rule-id", "source": {"fragment_id":"secops/demo"} } ],
  "errors": [ { "fragment_id": "...", "error": "..." } ]
}
```

### GET `/api/v1/policy/bundles`

- Response:

```json
{
  "bundles": {},
  "items": [
    {
      "id": "secops/demo",
      "enabled": true,
      "source": "studio",
      "author": "...",
      "message": "...",
      "created_at": "...",
      "updated_at": "...",
      "version": "...",
      "installed_at": "...",
      "sha256": "...",
      "rule_count": 4
    }
  ],
  "updated_at": "..."
}
```

### GET `/api/v1/policy/bundles/{id}`

- Response:

```json
{
  "id": "secops/demo",
  "content": "yaml...",
  "enabled": true,
  "author": "...",
  "message": "...",
  "created_at": "...",
  "updated_at": "..."
}
```

### PUT `/api/v1/policy/bundles/{id}`

- Constraint: id must start with `secops/`
- Request schema (`policyBundleUpsertRequest`):

```json
{
  "content": "yaml-policy",
  "enabled": true,
  "author": "secops",
  "message": "update rule"
}
```

- Response:

```json
{
  "id": "secops/demo",
  "updated_at": "..."
}
```

### POST `/api/v1/policy/bundles/{id}/simulate`

- Request schema (`policyBundleSimulateRequest`):

```json
{
  "request": { "topic": "job.default", "tenant": "default" },
  "content": "optional-overridden-policy-content"
}
```

- Response: protobuf-JSON `PolicyCheckResponse`

### GET `/api/v1/policy/bundles/snapshots`

- Response:

```json
{
  "items": [
    {
      "id": "2026-02-13T09:00:00Z-abcd1234",
      "created_at": "2026-02-13T09:00:00Z",
      "note": "before publish"
    }
  ]
}
```

### POST `/api/v1/policy/bundles/snapshots`

- Request:

```json
{ "note": "checkpoint" }
```

- Response: full snapshot object (`id`, `created_at`, `note`, `bundles`)

### GET `/api/v1/policy/bundles/snapshots/{id}`

- Response: full snapshot object
- Errors: `400`, `404`

### POST `/api/v1/policy/publish`

- Request schema (`policyPublishRequest`):

```json
{
  "bundle_ids": ["secops/a", "secops/b"],
  "author": "secops",
  "message": "publish",
  "note": "release"
}
```

- Response:

```json
{
  "snapshot_before": "...",
  "snapshot_after": "...",
  "published": ["secops/a", "secops/b"]
}
```

### POST `/api/v1/policy/rollback`

- Request schema (`policyRollbackRequest`):

```json
{
  "snapshot_id": "snapshot-id",
  "author": "secops",
  "message": "rollback",
  "note": "incident response"
}
```

- Response:

```json
{
  "snapshot_before": "...",
  "snapshot_after": "...",
  "rollback_to": "snapshot-id"
}
```

### GET `/api/v1/policy/audit`

- Response:

```json
{ "items": [ { "id": "...", "action": "publish", "created_at": "..." } ] }
```

### Example: policy evaluate

```bash
curl -sS -X POST http://localhost:8081/api/v1/policy/evaluate \
  -H 'X-API-Key: YOUR_API_KEY' \
  -H 'X-Tenant-ID: default' \
  -H 'Content-Type: application/json' \
  -d '{"topic":"job.default","tenant":"default"}'
```

---

## 9. Config and Schemas

### Config

### GET `/api/v1/config`

- Auth: required (admin for `system` scope; admin or operator for all other scopes)
- Query: `scope`, `scope_id`, `envelope=true|false`
- Valid scopes: `system`, `org`, `team`, `workflow`, `step`. Unknown scope values return `400`.
- Response:
  - default: flat `data` object
  - with `envelope=true`: full config document (`scope`, `scope_id`, `data`, `meta`)
- Errors: `400` (invalid scope), `403` (insufficient role or tenant mismatch)

### GET `/api/v1/config/effective`

- Auth: required + admin or operator
- Query: `org_id`, `team_id`, `workflow_id`, `step_id`
- Response: merged effective config snapshot
- Errors: `403` (viewer role denied)

### POST `/api/v1/config`

- Auth: required + admin
- Accepts:
  - wrapped document payload (`scope`, `scope_id`, `data`, `meta`)
  - flat payload (auto-wrapped to `system/default`)
- Response: `204`

### Schemas

### POST `/api/v1/schemas`

- Auth: required + admin
- Request:

```json
{
  "id": "schema-id",
  "schema": { "type": "object" }
}
```

- Response: `204`

### GET `/api/v1/schemas`

- Query: `limit`
- Response:

```json
{ "schemas": ["schema-a", "schema-b"] }
```

### GET `/api/v1/schemas/{id}`

- Response:

```json
{
  "id": "schema-id",
  "schema": { "type": "object" }
}
```

### DELETE `/api/v1/schemas/{id}`

- Auth: required + admin
- Response: `204`

---

## 10. Packs and Marketplace

All endpoints in this section require admin role.

### Packs

### GET `/api/v1/packs`

- Response:

```json
{ "items": [ { "id": "pack.id", "version": "1.0.0", "status": "ACTIVE" } ] }
```

### GET `/api/v1/packs/{id}`

- Response: full `packRecord`

### POST `/api/v1/packs/install`

- Content-Type: `multipart/form-data`
- Form fields:
  - `bundle` file (`.tgz`)
  - `force` boolean
  - `upgrade` boolean
  - `inactive` boolean
- Response: installed `packRecord`
- Errors: `400`, `403`, `409`, `500`, `503`

### POST `/api/v1/packs/{id}/uninstall`

- Request body (optional):

```json
{ "purge": true }
```

- Response: updated `packRecord` with `status: DISABLED`

### POST `/api/v1/packs/{id}/verify`

- Response:

```json
{
  "pack_id": "pack.id",
  "results": [
    {
      "name": "test-name",
      "expected": "ALLOW",
      "got": "ALLOW",
      "reason": "...",
      "ok": true
    }
  ]
}
```

### Marketplace

### GET `/api/v1/marketplace/packs`

- Response:

```json
{
  "catalogs": [
    {
      "id": "official",
      "title": "Cordum Official",
      "url": "https://packs.cordum.io/catalog.json",
      "enabled": true,
      "updated_at": "...",
      "error": ""
    }
  ],
  "items": [
    {
      "id": "pack.id",
      "version": "1.2.3",
      "title": "...",
      "description": "...",
      "url": "https://.../pack.tgz",
      "sha256": "...",
      "catalog_id": "official",
      "catalog_title": "Cordum Official",
      "capabilities": [],
      "requires": [],
      "risk_tags": [],
      "installed_version": "1.0.0",
      "installed_status": "ACTIVE",
      "installed_at": "..."
    }
  ],
  "fetched_at": "...",
  "cached": true
}
```

### POST `/api/v1/marketplace/install`

- Request schema (`marketplaceInstallRequest`):

```json
{
  "catalog_id": "official",
  "pack_id": "pack.id",
  "version": "1.2.3",
  "url": "https://.../pack.tgz",
  "sha256": "...",
  "force": false,
  "upgrade": true,
  "inactive": false
}
```

- Response: installed `packRecord`
- Errors: `400`, `403`, `404`, `500`, `503`

### Example: list marketplace packs

```bash
curl -sS http://localhost:8081/api/v1/marketplace/packs \
  -H 'X-API-Key: YOUR_API_KEY' \
  -H 'X-Tenant-ID: default'
```

---

## 11. Resource Locks

### GET `/api/v1/locks`

- Auth: required
- Query: `resource`
- Response schema (`lock`):

```json
{
  "resource": "packs:global",
  "mode": "exclusive",
  "owners": {
    "api-gateway:123": 1
  },
  "updated_at": "2026-02-13T09:37:17Z",
  "expires_at": "2026-02-13T09:38:17Z"
}
```

- Errors: `400`, `404`, `500`, `503`

### POST `/api/v1/locks/acquire`

- Auth: required + admin
- Request (`lockRequest`):

```json
{
  "resource": "packs:global",
  "owner": "api-gateway:123",
  "mode": "exclusive",
  "ttl_ms": 60000
}
```

- Response schema (`lock`): same as `GET /api/v1/locks`
- Errors: `400`, `403`, `409`, `503`

### POST `/api/v1/locks/release`

- Auth: required + admin
- Request: same `lockRequest`
- Response schema:

```json
{
  "lock": {
    "resource": "packs:global",
    "mode": "exclusive",
    "owners": {},
    "updated_at": "2026-02-13T09:38:24Z",
    "expires_at": "2026-02-13T09:39:24Z"
  },
  "released": true
}
```

- Errors: `400`, `403`, `409`, `503`

### POST `/api/v1/locks/renew`

- Auth: required + admin
- Request: same `lockRequest`
- Response schema (`lock`): same as `GET /api/v1/locks`
- Errors: `400`, `403`, `404`, `503`

---

## 12. Health, Status, Workers, Metrics

### GET `/health`

- Auth: public
- Response: plain text `ok`

### GET `/api/v1/status`

- Auth: required
- Response schema:

```json
{
  "time": "2026-02-13T09:38:23Z",
  "uptime_seconds": 4512,
  "build": {
    "version": "dev",
    "commit": "unknown",
    "date": "unknown"
  },
  "nats": {
    "connected": true,
    "status": "CONNECTED",
    "url": "nats://nats:4222"
  },
  "redis": {
    "ok": true,
    "error": "dial tcp ... (admin-only when present)"
  },
  "workers": {
    "count": 1
  },
  "license": {
    "plan": "enterprise"
  },
  "instance_id": "gw-abc123",
  "rate_limiter": {
    "mode": "redis"
  },
  "circuit_breakers": {
    "input": {
      "state": "CLOSED",
      "failures": 0,
      "fail_threshold": 3,
      "cooldown_remaining_ms": 0
    },
    "output": {
      "state": "CLOSED",
      "failures": 0,
      "fail_threshold": 3,
      "cooldown_remaining_ms": 0
    }
  },
  "replicas": {
    "api-gateway": [
      {"id": "gw-abc123", "service": "api-gateway", "version": "0.2.0", "commit": "abc123", "started_at": "2026-02-20T12:00:00Z"}
    ],
    "scheduler": [
      {"id": "sched-def456", "service": "scheduler", "version": "0.2.0", "commit": "def456", "started_at": "2026-02-20T12:00:01Z"}
    ]
  }
}
```

- Notes:
  - **Admin-only fields**: The following fields are only included when the caller has the `admin` role: `nats.url`, `redis.error`, `instance_id`, `rate_limiter`, `circuit_breakers`, `input_fail_open_total`, `ha_env`, `snapshot_meta`, `replicas`. Non-admin callers receive a reduced response with only `time`, `uptime_seconds`, `build`, `nats` (connected/status only), `redis` (ok only), `workers`, `pipeline`, and `license`.
  - `license` is optional and only present when auth provider exposes license info.
  - `circuit_breakers` shows input (pre-dispatch safety) and output (post-execution safety) circuit breaker state. Possible states: `CLOSED` (healthy), `OPEN` (failures exceeded threshold), `UNKNOWN` (Redis unavailable).
  - `replicas` is a map of service name to array of registered instance info. Only present when the instance registry is active (HA mode with Redis).
- Errors: auth/tenant middleware errors (`401`, `403`).

### GET `/api/v1/workers`

- Auth: required + admin
- Response schema: array of `Heartbeat` objects

```json
[
  {
    "worker_id": "demo-guardrails-worker",
    "region": "local",
    "type": "cpu",
    "cpu_load": 0,
    "gpu_utilization": 0,
    "active_jobs": 0,
    "capabilities": ["demo"],
    "pool": "demo-guardrails",
    "max_parallel_jobs": 4,
    "labels": {
      "tenant": "default"
    },
    "memory_load": 0,
    "progress_pct": 0,
    "last_memo": "ready"
  }
]
```

- Errors: `403` (non-admin), plus auth/tenant middleware errors (`401`, `403`).

### GET `/metrics`

- Server: metrics listener (`:9092` by default), not on main API mux
- Response: Prometheus metrics text format

Note: There are no `/healthz`, `/readyz`, or `/api/v1/system/health` routes in current `gateway_core.go` registration.

---

## 13. Memory and Artifacts

### GET `/api/v1/memory`

- Auth: required + admin
- Query: `ptr` or `key`
- Supports `ctx:*`, `res:*`, and `mem:*` keys
- Tenant isolation: all key prefixes enforce tenant checks. `ctx:`/`res:` keys check the job's tenant. `mem:run:{id}:*` keys check the workflow run's org. `mem:{id}:*` keys attempt job tenant lookup. Cross-tenant reads return `403`.
- Response includes pointer/key metadata and payload views (`base64`, and optional parsed `json`)

### POST `/api/v1/artifacts`

- Auth: required
- Request schema (`artifactPutRequest`):

```json
{
  "content_base64": "...",
  "content": "...",
  "content_type": "application/json",
  "retention": "24h",
  "labels": {"tenant_id":"default"}
}
```

- Response:

```json
{
  "artifact_ptr": "redis://artifact:...",
  "size_bytes": 123
}
```

### GET `/api/v1/artifacts/{ptr}`

- Auth: required + tenant constraints
- Response:

```json
{
  "artifact_ptr": "redis://artifact:...",
  "content_base64": "...",
  "metadata": {}
}
```

---

## 14. WebSocket Streaming

### GET `/api/v1/stream`

- Auth: required + admin role
- Upgrade: websocket
- API key via `X-API-Key` or websocket subprotocol (`cordum-api-key, <base64url(key)>`)
- Streams bus events (`sys.job.*`, `sys.audit.*`) filtered by tenant permissions.

### GET `/api/v1/jobs/{id}/stream`

- Auth: required + tenant access to that job
- Upgrade: websocket
- Streams only events for the specified job id.

---

## 15. MCP Endpoints (HTTP/SSE)

MCP routes are only registered when `mcp.enabled=true` and `mcp.transport=http` in config.

### GET `/mcp/sse`

- Auth: MCP auth wrapper (`AuthenticateHTTP` + tenant checks)
- Opens SSE stream; returns `X-MCP-Session-ID`.

### POST `/mcp/message`

- Auth: MCP auth wrapper (`AuthenticateHTTP` + tenant checks)
- Body: JSON-RPC 2.0 message

Example request:

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "initialize",
  "params": { "protocolVersion": "2024-11-05" }
}
```

- Response: JSON-RPC 2.0 response (or `202` for notifications)

### GET `/mcp/status`

- Auth: MCP auth wrapper
- Response:

```json
{
  "running": true,
  "connected_clients": 1,
  "uptime_seconds": 42,
  "transport": "http",
  "enabled_tools": 0,
  "enabled_resources": 0
}
```

### Example: MCP message call

```bash
curl -sS -X POST http://localhost:8081/mcp/message \
  -H 'Content-Type: application/json' \
  -H 'X-API-Key: YOUR_API_KEY' \
  -H 'X-Tenant-ID: default' \
  -d '{"jsonrpc":"2.0","id":1,"method":"ping"}'
```

---

## 16. Admin Endpoints

### `GET /api/v1/admin/locks` — List Active Distributed Locks

Returns all active distributed locks held in Redis. This is a read-only diagnostic endpoint for operators to inspect lock state across scheduler, workflow engine, and gateway services.

**Auth:** Admin role required.

**Response:**

```json
{
  "locks": [
    {
      "key": "cordum:scheduler:job:job-abc123",
      "holder": "scheduler-1a2b3c",
      "ttl_remaining_ms": 24500,
      "type": "job"
    },
    {
      "key": "cordum:reconciler:default",
      "holder": "scheduler-1a2b3c",
      "ttl_remaining_ms": 28000,
      "type": "reconciler"
    }
  ]
}
```

**Lock types:**

| Type | Key prefix | Description |
|------|-----------|-------------|
| `reconciler` | `cordum:reconciler:` | Scheduler reconciliation loop leader lock |
| `replayer` | `cordum:replayer:` | Replayer leader lock |
| `job` | `cordum:scheduler:job:` | Per-job dispatch lock |
| `snapshot` | `cordum:scheduler:snapshot:` | Snapshot writer lock |
| `dlq_cleanup` | `cordum:dlq:cleanup` | DLQ cleanup leader lock |
| `workflow_run` | `cordum:wf:run:lock:` | Per-workflow-run execution lock |
| `delay_poller` | `cordum:wf:delay:poller` | Delay timer poller leader lock |
| `workflow_reconciler` | `cordum:workflow-engine:reconciler:` | Workflow engine reconciler leader lock |
| `rate_limit` | `cordum:rl:` | Rate limiter window keys |
| `jwks_cache` | `cordum:auth:jwks:` | OIDC/JWT key set cache entries |
| `circuit_breaker` | `cordum:cb:` | Circuit breaker state keys |
| `marketplace_cache` | `cordum:cache:marketplace` | Marketplace pack list cache |

**Notes:**
- Results are capped at 500 entries to prevent oversized responses.
- Uses Redis `SCAN` (never `KEYS`) so it is safe to call in production.
- Keys that expire between the scan and the value read are silently skipped.
- A `ttl_remaining_ms` of `0` means the key has no expiry or expiry could not be read.

---

## Endpoint Index (Registered Routes)

The following routes are registered in gateway route setup.

| Method | Path |
|---|---|
| GET | `/health` |
| GET | `/api/v1/auth/config` |
| POST | `/api/v1/auth/login` |
| GET | `/api/v1/auth/session` |
| POST | `/api/v1/auth/logout` |
| POST | `/api/v1/auth/password` |
| POST | `/api/v1/users` |
| GET | `/api/v1/users` |
| PUT | `/api/v1/users/{id}` |
| DELETE | `/api/v1/users/{id}` |
| POST | `/api/v1/users/{id}/password` |
| GET | `/api/v1/auth/keys` |
| POST | `/api/v1/auth/keys` |
| DELETE | `/api/v1/auth/keys/{id}` |
| GET | `/api/v1/workers` |
| GET | `/api/v1/status` |
| GET | `/api/v1/jobs` |
| POST | `/api/v1/jobs` |
| GET | `/api/v1/jobs/{id}` |
| GET | `/api/v1/jobs/{id}/stream` |
| GET | `/api/v1/jobs/{id}/decisions` |
| POST | `/api/v1/jobs/{id}/cancel` |
| POST | `/api/v1/jobs/{id}/remediate` |
| GET | `/api/v1/memory` |
| POST | `/api/v1/artifacts` |
| GET | `/api/v1/artifacts/{ptr}` |
| GET | `/api/v1/traces/{id}` |
| GET | `/api/v1/workflows` |
| POST | `/api/v1/workflows` |
| GET | `/api/v1/workflows/{id}` |
| DELETE | `/api/v1/workflows/{id}` |
| POST | `/api/v1/workflows/{id}/runs` |
| GET | `/api/v1/workflows/{id}/runs` |
| POST | `/api/v1/workflows/{id}/dry-run` |
| GET | `/api/v1/workflow-runs` |
| GET | `/api/v1/workflow-runs/{id}` |
| GET | `/api/v1/workflow-runs/{id}/timeline` |
| GET | `/api/v1/workflow-runs/{id}/chat` |
| POST | `/api/v1/workflow-runs/{id}/chat` |
| DELETE | `/api/v1/workflow-runs/{id}` |
| POST | `/api/v1/workflow-runs/{id}/rerun` |
| GET | `/api/v1/config` |
| GET | `/api/v1/config/effective` |
| POST | `/api/v1/config` |
| GET | `/api/v1/packs` |
| GET | `/api/v1/packs/{id}` |
| POST | `/api/v1/packs/install` |
| POST | `/api/v1/packs/{id}/uninstall` |
| POST | `/api/v1/packs/{id}/verify` |
| GET | `/api/v1/marketplace/packs` |
| POST | `/api/v1/marketplace/install` |
| POST | `/api/v1/schemas` |
| GET | `/api/v1/schemas` |
| GET | `/api/v1/schemas/{id}` |
| DELETE | `/api/v1/schemas/{id}` |
| GET | `/api/v1/locks` |
| POST | `/api/v1/locks/acquire` |
| POST | `/api/v1/locks/release` |
| POST | `/api/v1/locks/renew` |
| GET | `/api/v1/dlq` |
| GET | `/api/v1/dlq/page` |
| DELETE | `/api/v1/dlq/{job_id}` |
| POST | `/api/v1/dlq/{job_id}/retry` |
| POST | `/api/v1/workflows/{id}/runs/{run_id}/cancel` |
| GET | `/api/v1/approvals` |
| POST | `/api/v1/approvals/{job_id}/approve` |
| POST | `/api/v1/approvals/{job_id}/reject` |
| POST | `/api/v1/policy/evaluate` |
| POST | `/api/v1/policy/simulate` |
| POST | `/api/v1/policy/explain` |
| GET | `/api/v1/policy/snapshots` |
| GET | `/api/v1/policy/rules` |
| GET | `/api/v1/policy/bundles` |
| GET | `/api/v1/policy/bundles/{id}` |
| PUT | `/api/v1/policy/bundles/{id}` |
| POST | `/api/v1/policy/bundles/{id}/simulate` |
| GET | `/api/v1/policy/bundles/snapshots` |
| POST | `/api/v1/policy/bundles/snapshots` |
| GET | `/api/v1/policy/bundles/snapshots/{id}` |
| POST | `/api/v1/policy/publish` |
| POST | `/api/v1/policy/rollback` |
| GET | `/api/v1/policy/audit` |
| GET | `/api/v1/admin/locks` |
| GET | `/api/v1/stream` (websocket upgrade) |
| GET | `/mcp/sse` |
| POST | `/mcp/message` |
| GET | `/mcp/status` |

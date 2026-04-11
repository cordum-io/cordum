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
- Query: `limit`, `cursor`, `include_resolved` (`false` to show pending approvals only)
- Response:

```json
{
  "items": [
    {
      "job": { "id": "job-id", "state": "APPROVAL" },
      "decision": "REQUIRE_APPROVAL",
      "policy_snapshot": "cfg:...",
      "policy_rule_id": "rule-1",
      "policy_reason": "finance approval required",
      "constraints": {},
      "job_hash": "...",
      "approval_required": true,
      "approval_ref": "job-id",
      "approval_status": "approved",
      "approval_actionability": "resolved",
      "approval_revision": 2,
      "approval_decision": "approve",
      "workflow_id": "wf-1",
      "workflow_run_id": "run-1",
      "workflow_step_id": "approve",
      "step_name": "Manager Approval",
      "gate_type": "workflow_approval",
      "context_ptr": "ctx:mem:job-workflow-context",
      "job_input": {
        "kind": "workflow_approval_context",
        "version": 1,
        "workflow": {
          "workflow_id": "wf-1",
          "run_id": "run-1",
          "step_id": "approve",
          "step_name": "Manager Approval"
        },
        "decision": {
          "amount": 1250,
          "currency": "USD",
          "vendor": "Acme Travel",
          "approval_reason": "manager threshold exceeded",
          "next_effect": "Approve to continue Manager Approval."
        }
      },
      "decision_summary": {
        "source": "workflow_payload",
        "completeness": "rich",
        "context_status": "available",
        "title": "Review Acme Travel spending request",
        "why": "manager threshold exceeded",
        "next_effect": "Approve to continue Manager Approval.",
        "amount": 1250,
        "currency": "USD",
        "vendor": "Acme Travel"
      },
      "resolved_by": "manager-2",
      "resolved_comment": "ticket INC-123",
      "resolution": "approved",
      "resolved_at": 1709000002000000
    }
  ],
  "next_cursor": 1739400000000000
}
```

Approval lifecycle endpoints may return structured conflict payloads on `409`:

```json
{
  "error": "approval in progress; retry",
  "status": 409,
  "code": "approval_retryable_lock",
  "retryable": true
}
```

Notes:

- Workflow approval gates and policy approvals share the same list endpoint.
- `decision_summary` is the primary decision-first contract for the dashboard. It
  highlights what is being approved, why it matters, and what happens next.
- `context_ptr` and `job_input` are only present when a workflow payload was persisted
  and successfully hydrated. Older non-workflow approvals may omit them.
- `decision_summary.context_status` can be `available`, `missing`, `malformed`,
  `unavailable`, or `absent` so degraded workflow data fails informatively instead of
  appearing as an empty approval shell.
- `decision_summary.source` distinguishes rich workflow payloads (`workflow_payload`),
  workflow-label fallback (`workflow_labels`), and legacy/policy-only approvals
  (`policy_only`).
- `approval_status` is the explicit lifecycle state. Values: `pending`,
  `approved`, `rejected`, `expired`, `invalidated`, `repaired`.
- `approval_actionability` tells the dashboard whether the approval can still be
  acted on. Values: `actionable`, `resolved`, `expired`, `invalidated`,
  `repaired`.
- `approval_revision` increments on each lifecycle mutation and helps clients
  detect stale views or concurrent operator repair work.
- `approval_decision` records the lifecycle mutation that resolved or repaired
  the approval (`approve`, `reject`, `expire`, `invalidate`, `repair`).
- Resolved approvals continue to expose `decision_summary`, `policy_snapshot`,
  `job_hash`, `resolved_by`, `resolved_comment`, and `resolution` for audit/history
  views.

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

`409 Conflict` uses machine-readable `code` values:

- `approval_retryable_lock` — another decision or repair is in progress; safe to retry
- `approval_terminal_run` — the workflow run already moved past this approval
- `approval_stale_snapshot` — the governing policy snapshot changed
- `approval_stale_request` — the underlying job request changed
- `approval_not_actionable` — the approval is no longer actionable
- `approval_already_resolved` — a decision is already recorded; refresh for audit detail

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

Reject conflicts use the same structured `409` payload and `code` values as the
approve endpoint.

### POST `/api/v1/approvals/{job_id}/repair`

- Auth: required + admin + tenant access
- Request:

```json
{
  "apply": false,
  "note": "optional operator note"
}
```

- Response (dry-run):

```json
{
  "job_id": "job-id",
  "apply": false,
  "applied": false,
  "repairable": true,
  "state": "APPROVAL_REQUIRED",
  "trace_id": "trace-id",
  "approval": {
    "status": "approved",
    "actionability": "resolved"
  },
  "plan": {
    "kind": "invalidate_stale_snapshot",
    "repairable": true,
    "reason": "policy snapshot changed after approval request creation"
  }
}
```

- Response (apply): same envelope, but `applied=true`; the returned `approval`
  object reflects the repaired lifecycle record and may include
  `publish_deferred=true` / `publish_error` when state repair succeeded but the
  follow-up publish must be replayed by recovery logic.
- Errors: `400`, `403`, `404`, `409`, `500`

Notes:

- `apply=false` is the dry-run inspection path used by operators and `cordumctl`.
- `plan.kind` classifies terminal workflow runs, stale request drift, stale
  policy snapshots, legacy partial approvals, and publish-intent recovery.
- Repair requests also use `approval_retryable_lock` when another decision or
  repair currently holds the approval lock.

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

### DELETE `/api/v1/policy/bundles/{id}`

- Auth: required + admin
- Deletes a policy bundle by ID.
- Response: `204 No Content`
- Errors: `400` (invalid id), `404` (not found)

### GET `/api/v1/policy/output/rules`

- Auth: required
- Response: list of output policy rules.

```json
{ "items": [{ "id": "rule-1", "name": "PII filter", "enabled": true, "action": "redact", "pattern": "..." }] }
```

- Errors: auth errors.

### GET `/api/v1/policy/output/stats`

- Auth: required
- Response: aggregate output policy statistics (quarantine counts, redaction counts, denial counts).

```json
{ "total_checked": 1024, "quarantined": 12, "redacted": 45, "denied": 3 }
```

- Errors: auth errors.

### PUT `/api/v1/policy/output/rules/{id}`

- Auth: required + admin
- Updates an existing output policy rule.
- Request:

```json
{ "name": "PII filter", "enabled": true, "action": "redact", "pattern": "..." }
```

- Response: updated rule object.
- Errors: `400` (invalid rule), `404` (not found)

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

## 10. Topics and Worker Credentials

### Topics

### GET `/api/v1/topics`

- Auth: required + `admin`, `operator`, or `viewer`
- Response:

```json
{
  "items": [
    {
      "name": "job.sre-investigator.collect.k8s",
      "pool": "sre-investigator",
      "input_schema_id": "sre-investigator/IncidentContext",
      "output_schema_id": "sre-investigator/IncidentResult",
      "pack_id": "sre-investigator",
      "requires": ["kubectl", "network:egress"],
      "risk_tags": ["network"],
      "status": "active",
      "active_worker_count": 2
    }
  ],
  "registry_empty": false
}
```

Notes:

- `registry_empty=true` means the canonical topic registry has not been populated yet, so legacy routing may still be the only authority.
- `active_worker_count` is derived from the latest runtime worker snapshot for the topic's configured pool. A value of `0` means the topic is still valid but currently degraded.

- Errors: `403`, `500`, `503`

### POST `/api/v1/topics`

- Auth: required + `admin`
- Request schema:

```json
{
  "name": "job.sre-investigator.collect.k8s",
  "pool": "sre-investigator",
  "input_schema_id": "sre-investigator/IncidentContext",
  "output_schema_id": "sre-investigator/IncidentResult",
  "pack_id": "sre-investigator",
  "requires": ["kubectl", "network:egress"],
  "risk_tags": ["network"],
  "status": "active"
}
```

Rules:

- `name` must be a valid topic name.
- `pool` must reference an existing pool unless `status` is `disabled`.
- `status` may be `active`, `deprecated`, or `disabled`. Omitted status defaults to `active`.

- Response: `201 Created` for a new topic, `200 OK` when updating an existing registration.

```json
{
  "name": "job.sre-investigator.collect.k8s",
  "pool": "sre-investigator",
  "input_schema_id": "sre-investigator/IncidentContext",
  "output_schema_id": "sre-investigator/IncidentResult",
  "pack_id": "sre-investigator",
  "requires": ["kubectl", "network:egress"],
  "risk_tags": ["network"],
  "status": "active"
}
```

- Errors: `400`, `403`, `404`, `503`

### DELETE `/api/v1/topics/{name}`

- Auth: required + `admin`
- Response: `204`
- Errors: `400`, `403`, `404`, `500`, `503`

### Example: register and list topics

```bash
curl -sS -X POST http://localhost:8081/api/v1/topics \
  -H 'X-API-Key: YOUR_API_KEY' \
  -H 'X-Tenant-ID: default' \
  -H 'Content-Type: application/json' \
  -d '{
    "name":"job.sre-investigator.collect.k8s",
    "pool":"sre-investigator",
    "input_schema_id":"sre-investigator/IncidentContext",
    "output_schema_id":"sre-investigator/IncidentResult",
    "pack_id":"sre-investigator",
    "requires":["kubectl","network:egress"],
    "risk_tags":["network"],
    "status":"active"
  }'

curl -sS http://localhost:8081/api/v1/topics \
  -H 'X-API-Key: YOUR_API_KEY' \
  -H 'X-Tenant-ID: default'
```

### Worker credentials

### GET `/api/v1/workers/credentials`

- Auth: required + `admin`
- Response:

```json
{
  "items": [
    {
      "worker_id": "external-worker-01",
      "allowed_pools": ["sre-investigator"],
      "allowed_topics": ["job.sre-investigator.collect.k8s"],
      "pack_id": "",
      "created_by": "admin",
      "created_at": "2026-04-07T12:00:00Z"
    }
  ]
}
```

Notes:

- Revoked credentials remain listable and include `revoked_at`.
- Pack-managed credentials may include `pack_id`; operator-issued credentials usually leave it empty.

- Errors: `403`, `500`, `503`

### POST `/api/v1/workers/credentials`

- Auth: required + `admin`
- Request schema:

```json
{
  "worker_id": "external-worker-01",
  "allowed_pools": ["sre-investigator"],
  "allowed_topics": ["job.sre-investigator.collect.k8s"]
}
```

Rules:

- `worker_id` must be non-empty and must not contain whitespace.
- `allowed_pools` must reference existing pools.
- `allowed_topics` must reference existing registered topics when the topic registry is populated.
- Creating a credential for an existing `worker_id` rotates it and returns a fresh token.

- Response: `201 Created` for a new credential, `200 OK` when rotating an existing credential.

```json
{
  "worker_id": "external-worker-01",
  "allowed_pools": ["sre-investigator"],
  "allowed_topics": ["job.sre-investigator.collect.k8s"],
  "pack_id": "",
  "created_by": "admin",
  "created_at": "2026-04-07T12:00:00Z",
  "token": "9f8d4c..."
}
```

Important:

- The plaintext `token` is returned only once at creation/rotation time. Store it immediately.

- Errors: `400`, `403`, `404`, `500`, `503`

### DELETE `/api/v1/workers/credentials/{worker_id}`

- Auth: required + `admin`
- Response: `204`
- Errors: `400`, `403`, `404`, `500`, `503`

### Example: issue and revoke a worker credential

```bash
curl -sS -X POST http://localhost:8081/api/v1/workers/credentials \
  -H 'X-API-Key: YOUR_API_KEY' \
  -H 'X-Tenant-ID: default' \
  -H 'Content-Type: application/json' \
  -d '{
    "worker_id":"external-worker-01",
    "allowed_pools":["sre-investigator"],
    "allowed_topics":["job.sre-investigator.collect.k8s"]
  }'

curl -sS -X DELETE http://localhost:8081/api/v1/workers/credentials/external-worker-01 \
  -H 'X-API-Key: YOUR_API_KEY' \
  -H 'X-Tenant-ID: default'
```

---

## 11. Packs and Marketplace

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

## 12. Resource Locks

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

## 13. Health, Status, Workers, Metrics

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

### GET `/api/v1/telemetry/status`

- Auth: required + admin
- Response schema:

```json
{
  "mode": "anonymous",
  "endpoint": "https://telemetry.cordum.io/v1/report",
  "last_collected_at": "2026-04-08T22:00:00Z",
  "last_reported_at": "2026-04-08T22:00:05Z"
}
```

- Notes:
  - `mode` is controlled by `CORDUM_TELEMETRY_MODE` (`off`, `local_only`, `anonymous`).
  - `endpoint` is only populated when remote reporting is enabled.
- Errors: auth/tenant middleware errors (`401`, `403`), `500`.

### GET `/api/v1/telemetry/inspect`

- Auth: required + admin
- Response: the exact last locally stored telemetry payload as JSON, or `null` if nothing has been collected yet.
- Errors: auth/tenant middleware errors (`401`, `403`), `500`.

### GET `/api/v1/telemetry/export`

- Auth: required + admin
- Response: downloadable JSON attachment (`Content-Disposition: attachment; filename="cordum-telemetry.json"`) containing the exact last locally stored telemetry payload, or `null` if nothing has been collected yet.
- Errors: auth/tenant middleware errors (`401`, `403`), `500`.

### GET `/api/v1/telemetry/usage`

- Auth: required + admin
- Response schema:

```json
{
  "workers": {
    "registered": 2,
    "connected": 1
  },
  "usage": {
    "active_jobs": 1,
    "active_workflow_runs": 1,
    "jobs_last_24h": 12,
    "workflow_runs_last_24h": 4,
    "schemas": 3,
    "policy_bundles": 2
  },
  "features_enabled": {
    "user_auth": true,
    "oidc": false,
    "output_policy": true
  },
  "engagement": {
    "topics_configured": 5,
    "workflows_configured": 3,
    "packs_installed": 1,
    "user_auth_enabled": true,
    "oidc_enabled": false,
    "output_policy_enabled": true
  },
  "limits_hit": {}
}
```

- Errors: auth/tenant middleware errors (`401`, `403`), `500`.

### POST `/api/v1/telemetry/consent`

- Auth: required + admin
- Body: `{"mode": "off"}` — set telemetry mode (`off`, `local_only`, `anonymous`). Persisted in Redis and applied immediately.
- Errors: `400` (invalid mode), auth errors.

### GET `/api/v1/license`

- Auth: required
- Response schema:

```json
{
  "plan": "community",
  "license": {
    "mode": "community",
    "status": "active",
    "plan": "Community",
    "features": ["audit", "break_glass_admin"],
    "limits": {
      "max_workers": 3,
      "max_concurrent_jobs": 3,
      "requests_per_second": 500,
      "audit_retention_days": 7
    }
  },
  "entitlements": { ... },
  "rights": null,
  "expiry_status": "active"
}
```

- Notes: Returns current license plan, entitlements, rights, and expiry status. No license = Community tier.
- Errors: auth errors.

### POST `/api/v1/license/reload`

- Auth: required + admin
- Re-reads the license from environment/disk and revalidates. Use after installing a new license file.
- Errors: `500` if license cannot be read/parsed.

### GET `/api/v1/license/usage`

- Auth: required + admin
- Response: current usage vs entitlement limits (workers, jobs, workflows, schemas, policies, rate limits).
- Errors: auth errors.

### GET `/api/v1/topics`

- Auth: required
- Response: `{"items": [{"name": "job.my-pack.process", "pool": "my-pack", "status": "active", "pack_id": "my-pack", "input_schema_id": "", "output_schema_id": "", "requires": [], "risk_tags": []}]}`
- Notes: Returns all registered topics from the topic registry. Topics are registered automatically when packs are installed.

### DELETE `/api/v1/topics/{name}`

- Auth: required + admin
- Removes a topic registration.
- Errors: `404` (not found), auth errors.

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

### GET `/api/v1/workers/{id}`

- Auth: required + admin
- Response: single `Heartbeat` object for the specified worker.
- Errors: `404` (worker not found), `403` (non-admin).

### GET `/api/v1/workers/{id}/jobs`

- Auth: required + admin
- Response: list of jobs currently assigned to or recently completed by the worker.
- Errors: `404` (worker not found), `403` (non-admin).

### GET `/metrics`

- Server: metrics listener (`:9092` by default), not on main API mux
- Response: Prometheus metrics text format

Note: There are no `/healthz`, `/readyz`, or `/api/v1/system/health` routes in current `gateway_core.go` registration.

---

## 14. Memory and Artifacts

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

## 15. WebSocket Streaming

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

## 16. MCP Endpoints (HTTP/SSE)

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

## 17. Admin Endpoints

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

## 18. Pool Management

All pool management endpoints require **admin** role.

### GET `/api/v1/pools`

List all worker pools.

**Response (200):**
```json
{ "items": [{ "name": "gpu-pool", "status": "active", "requires": ["gpu"], "description": "GPU-enabled pool" }] }
```

### GET `/api/v1/pools/{name}`

Get details for a specific pool.

**Response (200):**
```json
{ "name": "gpu-pool", "status": "active", "requires": ["gpu"], "description": "GPU-enabled pool", "topics": ["job.ml.train"] }
```

**Errors:** 404 (not found)

### PUT `/api/v1/pools/{name}`

Create a new worker pool.

**Request body:**
```json
{ "requires": ["docker", "gpu"], "description": "GPU-enabled pool" }
```

**Response (201):**
```json
{ "name": "gpu-pool", "status": "active", "requires": ["docker", "gpu"], "description": "GPU-enabled pool" }
```

**Errors:** 400 (invalid name), 409 (already exists)

### PATCH `/api/v1/pools/{name}`

Update pool configuration. Only provided fields are changed.

**Request body:**
```json
{ "requires": ["docker"], "description": "updated description" }
```

**Response (200):** Updated pool object.

**Errors:** 400 (invalid config), 404 (not found)

### DELETE `/api/v1/pools/{name}`

Delete a pool. Fails if the pool has active topic mappings unless `?force=true`.

**Query params:** `force=true` — remove topic mappings and delete.

**Response:** 204 No Content

**Errors:** 400 (active topic mappings), 404 (not found)

### POST `/api/v1/pools/{name}/drain`

Start draining a pool. Stops new job routing; in-flight jobs complete normally.
The pool auto-transitions to `inactive` when all jobs finish or timeout expires.

**Request body:**
```json
{ "timeout_seconds": 300 }
```

**Response (200):**
```json
{ "name": "my-pool", "status": "draining", "drain_started_at": "2026-03-26T10:00:00Z", "drain_timeout_seconds": 300 }
```

**Errors:** 400 (not active / already draining), 404 (not found)

### PUT `/api/v1/pools/{name}/topics/{topic}`

Add a topic-to-pool mapping. Idempotent — succeeds if already mapped.

**Response:** 204 No Content

**Errors:** 400 (invalid topic format), 404 (pool not found)

### DELETE `/api/v1/pools/{name}/topics/{topic}`

Remove a topic-to-pool mapping. If the topic has no remaining pools, the topic
entry is removed entirely.

**Response:** 204 No Content

**Errors:** 404 (pool or topic not found)

### Example: create pool and assign topic

```bash
# Create pool
curl -X PUT https://gateway:8081/api/v1/pools/gpu-batch \
  -H "Authorization: Bearer $API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"requires": ["gpu"], "description": "GPU batch processing"}'

# Assign topic
curl -X PUT https://gateway:8081/api/v1/pools/gpu-batch/topics/job.ml.train \
  -H "Authorization: Bearer $API_KEY"

# Drain pool
curl -X POST https://gateway:8081/api/v1/pools/gpu-batch/drain \
  -H "Authorization: Bearer $API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"timeout_seconds": 600}'
```

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
| GET | `/api/v1/workers/{id}` |
| GET | `/api/v1/workers/{id}/jobs` |
| GET | `/api/v1/workers/credentials` |
| POST | `/api/v1/workers/credentials` |
| DELETE | `/api/v1/workers/credentials/{worker_id}` |
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
| GET | `/api/v1/topics` |
| POST | `/api/v1/topics` |
| DELETE | `/api/v1/topics/{name}` |
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
| POST | `/api/v1/approvals/{job_id}/repair` |
| POST | `/api/v1/policy/evaluate` |
| POST | `/api/v1/policy/simulate` |
| POST | `/api/v1/policy/explain` |
| GET | `/api/v1/policy/snapshots` |
| GET | `/api/v1/policy/rules` |
| GET | `/api/v1/policy/bundles` |
| GET | `/api/v1/policy/bundles/{id}` |
| PUT | `/api/v1/policy/bundles/{id}` |
| DELETE | `/api/v1/policy/bundles/{id}` |
| POST | `/api/v1/policy/bundles/{id}/simulate` |
| GET | `/api/v1/policy/bundles/snapshots` |
| POST | `/api/v1/policy/bundles/snapshots` |
| GET | `/api/v1/policy/bundles/snapshots/{id}` |
| POST | `/api/v1/policy/publish` |
| POST | `/api/v1/policy/rollback` |
| GET | `/api/v1/policy/audit` |
| GET | `/api/v1/policy/output/rules` |
| GET | `/api/v1/policy/output/stats` |
| PUT | `/api/v1/policy/output/rules/{id}` |
| GET | `/api/v1/pools` |
| GET | `/api/v1/pools/{name}` |
| PUT | `/api/v1/pools/{name}` |
| PATCH | `/api/v1/pools/{name}` |
| DELETE | `/api/v1/pools/{name}` |
| POST | `/api/v1/pools/{name}/drain` |
| PUT | `/api/v1/pools/{name}/topics/{topic}` |
| DELETE | `/api/v1/pools/{name}/topics/{topic}` |
| GET | `/api/v1/admin/locks` |
| GET | `/api/v1/stream` (websocket upgrade) |
| GET | `/mcp/sse` |
| POST | `/mcp/message` |
| GET | `/mcp/status` |

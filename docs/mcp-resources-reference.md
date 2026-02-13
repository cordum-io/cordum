# MCP Resources Reference

Cordum MCP resources expose read-only operational context for agents and clients.
All `resources/read` calls return MCP content with:
- `contents[0].uri`
- `contents[0].mimeType` (`application/json`)
- `contents[0].text` (a JSON-encoded payload string)

## Resource Catalog

### `cordum://jobs/{id}`

- Description: Job detail by ID.
- Response format:
  - `id`, `state`, `topic`, `tenant`
  - `submitted_at`, `completed_at`
  - `result_ptr`, optional `result`
  - `safety_decision`, `safety_reason`, `safety_rule_id`
  - optional approval metadata
- Example payload (`contents[0].text` JSON):
```json
{
  "id": "b25c8808-17ad-4114-b013-15552bbf5359",
  "state": "succeeded",
  "topic": "job.demo-guardrails.safe",
  "tenant": "default",
  "submitted_at": "2026-02-13T15:20:10Z",
  "completed_at": "2026-02-13T15:20:11Z",
  "result_ptr": "redis://res:b25c8808-17ad-4114-b013-15552bbf5359",
  "safety_decision": "ALLOW",
  "safety_reason": "matched allow rule",
  "safety_rule_id": "allow-safe-topic"
}
```

### `cordum://jobs?status={status}&limit={limit}&cursor={cursor}`

- Description: Job list with filters and pagination.
- Query params:
  - `status` (optional): pending/running/succeeded/failed/cancelled
  - `limit` (optional): default `20`, max `100`
  - `cursor` (optional): pagination cursor
- Response:
  - `items`: array of jobs
  - `next_cursor`: cursor for next page (when available)
- Example payload (`contents[0].text` JSON):
```json
{
  "items": [
    {
      "id": "b25c8808-17ad-4114-b013-15552bbf5359",
      "state": "succeeded",
      "topic": "job.demo-guardrails.safe",
      "submitted_at": "2026-02-13T15:20:10Z"
    },
    {
      "id": "db396e4d-0db6-440a-a0e0-3f2df885c559",
      "state": "running",
      "topic": "job.hello-pack.echo",
      "submitted_at": "2026-02-13T15:19:55Z"
    }
  ],
  "next_cursor": 1739458200000000
}
```

### `cordum://workflows/{id}/runs?limit={limit}`

- Description: Recent workflow runs for a workflow ID.
- Query params:
  - `limit` (optional): default `10`, max `100`
- Response:
  - `workflow_id`
  - `items`: array of runs
- Example payload (`contents[0].text` JSON):
```json
{
  "workflow_id": "demo-guardrails",
  "items": [
    {
      "id": "run-20260213-001",
      "status": "succeeded",
      "started_at": "2026-02-13T15:10:00Z",
      "completed_at": "2026-02-13T15:10:04Z"
    },
    {
      "id": "run-20260213-002",
      "status": "approval_required",
      "started_at": "2026-02-13T15:12:00Z"
    }
  ]
}
```

### `cordum://workflows/{id}/runs/{runId}`

- Description: Workflow run detail by run ID (scoped by workflow ID).
- Response format:
  - `id`, `workflow_id`, `status`
  - `steps` array with per-step status and timing
  - optional `output`, `error`, and metadata
- Example payload (`contents[0].text` JSON):
```json
{
  "id": "run-20260213-001",
  "workflow_id": "demo-guardrails",
  "status": "succeeded",
  "steps": [
    {
      "id": "write@1",
      "status": "succeeded",
      "started_at": "2026-02-13T15:10:00Z",
      "completed_at": "2026-02-13T15:10:02Z"
    },
    {
      "id": "safe@1",
      "status": "succeeded",
      "started_at": "2026-02-13T15:10:02Z",
      "completed_at": "2026-02-13T15:10:04Z"
    }
  ]
}
```

### `cordum://audit?limit={limit}`

- Description: Recent policy audit entries.
- Query params:
  - `limit` (optional): default `50`, max `200`
- Response:
  - `items`: audit entries (timestamp/action/resource/actor metadata)
- Example payload (`contents[0].text` JSON):
```json
{
  "items": [
    {
      "timestamp": "2026-02-13T15:18:23Z",
      "action": "policy.evaluate",
      "resource": "job:b25c8808-17ad-4114-b013-15552bbf5359",
      "actor": "scheduler",
      "decision": "ALLOW"
    },
    {
      "timestamp": "2026-02-13T15:18:31Z",
      "action": "policy.evaluate",
      "resource": "job:dangerous-001",
      "actor": "scheduler",
      "decision": "DENY"
    }
  ]
}
```

### `cordum://health`

- Description: System health snapshot.
- Response format:
  - `uptime_seconds`
  - `workers` (counts/active)
  - `redis`, `nats`, `safety_kernel` status objects
  - optional build/version metadata
- Example payload (`contents[0].text` JSON):
```json
{
  "uptime_seconds": 8342,
  "workers": {
    "total": 7,
    "active": 4
  },
  "redis": {
    "status": "ok"
  },
  "nats": {
    "status": "ok"
  },
  "safety_kernel": {
    "status": "ok"
  },
  "version": "dev"
}
```

### `cordum://policies`

- Description: Active policy bundle summary.
- Response:
  - `active_bundles`: current bundle summaries
  - `current_snapshot_id`
  - `safety_stance` (`permissive|balanced|strict`)
- Example payload (`contents[0].text` JSON):
```json
{
  "active_bundles": [
    {
      "id": "demo-guardrails@0.1.0",
      "name": "demo-guardrails",
      "rule_count": 12,
      "last_updated": "2026-02-13T15:00:00Z"
    }
  ],
  "current_snapshot_id": "snapshot-20260213-150000",
  "safety_stance": "balanced"
}
```

## JSON-RPC Envelope Example

```json
{
  "jsonrpc": "2.0",
  "id": 11,
  "method": "resources/read",
  "params": {
    "uri": "cordum://jobs?status=running&limit=5"
  }
}
```

```json
{
  "jsonrpc": "2.0",
  "id": 11,
  "result": {
    "contents": [
      {
        "uri": "cordum://jobs?status=running&limit=5",
        "mimeType": "application/json",
        "text": "{\"items\":[...],\"next_cursor\":1739433600000000}"
      }
    ]
  }
}
```

## Pagination Pattern

- List resources return `next_cursor` when additional pages exist.
- Pass returned `next_cursor` into the next `resources/read` URI query.
- Keep `limit` stable across pages for predictable traversal.

## Agent Usage Notes

- Fetch `cordum://policies` and `cordum://health` early to build operational context.
- Use `cordum://jobs?...` before `cordum://jobs/{id}` to avoid unnecessary deep reads.
- Prefer bounded limits (`5-20`) for iterative context gathering workflows.

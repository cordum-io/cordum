# MCP Tools Reference

This document describes the core Cordum MCP tools exposed by the MCP server.
All tool calls are JSON-RPC `tools/call` requests and require gateway auth.

## Tool Catalog

### `cordum_submit_job`

- Purpose: Submit a new job into Cordum's scheduler pipeline.
- Input:
  - `prompt` (string, required)
  - `topic` (string, default `job.default`)
  - `priority` (`low|normal|high|critical`, default `normal`)
  - `capability` (string, optional)
  - `risk_tags` (string[], optional)
  - `labels` (object<string,string>, optional)
  - `memory_id` (string, optional)
  - `pack_id` (string, optional)
- Output:
  - `job_id` (string)
  - `trace_id` (string)
  - `status` (`pending`)
- Error codes:
  - `idempotency_conflict`
  - `system_at_capacity`
  - `submit_failed`

### `cordum_cancel_job`

- Purpose: Cancel a pending/running job.
- Input:
  - `job_id` (string, required)
  - `reason` (string, optional)
- Output:
  - `cancelled` (boolean)
  - `job_id` (string)
- Error codes:
  - `job_not_found`
  - `job_already_completed`
  - `cancel_failed`

### `cordum_trigger_workflow`

- Purpose: Start a workflow run.
- Input:
  - `workflow_id` (string, required)
  - `input` (object, optional)
  - `dry_run` (boolean, default `false`)
  - `idempotency_key` (string, optional)
- Output:
  - `run_id` (string)
  - `workflow_id` (string)
  - `status` (`pending`)
- Error codes:
  - `workflow_not_found`
  - `input_validation_failed`
  - `trigger_failed`

### `cordum_approve_job`

- Purpose: Approve a job waiting in approval state.
- Input:
  - `job_id` (string, required)
  - `note` (string, optional)
- Output:
  - `approved` (boolean)
  - `job_id` (string)
- Error codes:
  - `job_not_found`
  - `job_not_in_approval_state`
  - `policy_changed_since_request`
  - `approve_failed`

### `cordum_reject_job`

- Purpose: Reject a job waiting in approval state.
- Input:
  - `job_id` (string, required)
  - `reason` (string, required)
- Output:
  - `rejected` (boolean)
  - `job_id` (string)
- Error codes:
  - `job_not_found`
  - `job_not_in_approval_state`
  - `policy_changed_since_request`
  - `reject_failed`

### `cordum_query_policy`

- Purpose: Simulate policy decision before submitting a job.
- Input:
  - `topic` (string, required)
  - `priority` (`low|normal|high|critical`, default `normal`)
  - `capability` (string, optional)
  - `risk_tags` (string[], optional)
  - `labels` (object<string,string>, optional)
- Output:
  - `decision` (`allow|deny|require_approval|throttle`)
  - `reason` (string)
  - `rule_id` (string)
  - `constraints` (object)
  - `remediations` (array<object>)
- Error codes:
  - `policy_query_failed`

## JSON-RPC Examples

### Submit Job

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "tools/call",
  "params": {
    "name": "cordum_submit_job",
    "arguments": {
      "prompt": "Summarize latest deployment audit logs",
      "topic": "job.ops.summary",
      "priority": "normal",
      "risk_tags": ["ops"]
    }
  }
}
```

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "content": [{ "type": "text", "text": "job submitted" }],
    "structuredContent": {
      "job_id": "d0a4f177-84c9-4d39-aac0-3b8f0e45a779",
      "trace_id": "0f2a9589-c946-4bd4-8d6d-ef7d26565876",
      "status": "pending"
    }
  }
}
```

### Trigger Workflow

```json
{
  "jsonrpc": "2.0",
  "id": 2,
  "method": "tools/call",
  "params": {
    "name": "cordum_trigger_workflow",
    "arguments": {
      "workflow_id": "ops.daily.report",
      "input": { "date": "2026-02-13" },
      "dry_run": false
    }
  }
}
```

### Approve Job

```json
{
  "jsonrpc": "2.0",
  "id": 3,
  "method": "tools/call",
  "params": {
    "name": "cordum_approve_job",
    "arguments": {
      "job_id": "b42d50f8-d2df-4ae1-a3d1-c3cead8ea4cc",
      "note": "approved by on-call engineer"
    }
  }
}
```

### Reject Job

```json
{
  "jsonrpc": "2.0",
  "id": 4,
  "method": "tools/call",
  "params": {
    "name": "cordum_reject_job",
    "arguments": {
      "job_id": "b42d50f8-d2df-4ae1-a3d1-c3cead8ea4cc",
      "reason": "missing required change ticket reference"
    }
  }
}
```

### Query Policy

```json
{
  "jsonrpc": "2.0",
  "id": 5,
  "method": "tools/call",
  "params": {
    "name": "cordum_query_policy",
    "arguments": {
      "topic": "job.finance.export",
      "priority": "high",
      "capability": "finance_ops",
      "risk_tags": ["pii", "export"]
    }
  }
}
```

### Cancel Job

```json
{
  "jsonrpc": "2.0",
  "id": 6,
  "method": "tools/call",
  "params": {
    "name": "cordum_cancel_job",
    "arguments": {
      "job_id": "d0a4f177-84c9-4d39-aac0-3b8f0e45a779",
      "reason": "operator cancelled stale run"
    }
  }
}
```

## Best Practices

- Call `cordum_query_policy` before high-risk submissions to check likely decision and constraints.
- Use `idempotency_key` for workflow-trigger retries to avoid duplicate runs.
- Include clear `reason` and `note` fields on approval decisions for auditability.
- Keep `labels`, `capability`, and `risk_tags` consistent across policy query and submit calls to avoid decision drift.

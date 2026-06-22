# MCP Tools Reference

This document describes the core Cordum MCP tools exposed by the MCP server.
All tool calls are JSON-RPC `tools/call` requests and require gateway auth.

## Tool Catalog

<!-- BEGIN:mcp-tools -->

_Generated from `core/mcp` via `RegisterAllTools` — do not edit by hand; run `make docs-tables`. 27 tools._

| Tool | Approval | Scope | Description |
|------|----------|-------|-------------|
| `cordum_approve_job` | — | `—` | Approve a job that requires human approval before execution. |
| `cordum_audit_query` | — | `—` | Search the audit chain for SIEMEvents matching filters like tenant, event_type, and time window. Use this when the operator asks 'who changed policy X?', 'what happened around time T?', or 'did tool Y get called?'. Returns chain-verified events (seq + event_hash + prev_hash) so callers can prove integrity downstream. |
| `cordum_audit_verify` | — | `—` | Walk the tenant's audit Merkle chain and report integrity: ok / compromised / partial. Use this when the operator asks 'is our audit log clean?' or before handing a compliance auditor evidence. Response includes any gaps with sequence numbers. |
| `cordum_cancel_job` | — | `—` | Cancel a running or pending job. |
| `cordum_create_workflow` | required | `mcp_write` | Create a new workflow definition from a spec. Use this when: the operator describes a multi-step automation and wants it registered so it can be triggered by `cordum_trigger_workflow` later. Returns the new workflow_id. This tool requires human approval by default. On first call, expect a JSON-RPC -32099 error with an approval_id in the data — surface the dashboard link (/approvals?mcp=<id>) to the operator and retry the call after they approve. |
| `cordum_get_job` | — | `—` | Fetch the full record for a single job by id, including prompt, topic, policy decision, retry history, and final state. Use this when the operator says 'show me job X' or 'why did job X fail?' — the response includes the safety decision and any denial reason. |
| `cordum_get_run` | — | `—` | Fetch a workflow run by id with its graph state, pending steps, and outputs. Use this when the operator says 'what is run X doing now?' or 'did run X finish?'. |
| `cordum_install_pack` | required | `mcp_write` | Install a marketplace pack so its capabilities become available to agents. Use this when: the operator asks to 'install X' or 'add the X integration'. Returns {pack_id, version, installed}. This tool requires human approval by default. On first call, expect a JSON-RPC -32099 error with an approval_id in the data — surface the dashboard link (/approvals?mcp=<id>) to the operator and retry the call after they approve. |
| `cordum_list_agents` | — | `—` | List agent identities configured in Cordum — their allowed tools, risk tier, and data classifications. Use this when the operator asks 'which agents can call tool X?' or is reviewing an agent before granting a new scope. |
| `cordum_list_jobs` | — | `—` | List jobs the caller's tenant has submitted to Cordum, newest first. Returns job id, topic, state, submitter, and timestamps. Use this when the operator asks 'what jobs ran today?', 'any failures in the last hour?', or needs to find a job id before cancelling or inspecting it. |
| `cordum_list_packs` | — | `—` | List installed integration packs (Slack, GitHub, AWS, etc.) and their status. Use this when the operator asks 'what integrations are live?' or 'is the Slack pack installed?'. Returns pack id, version, enabled, and install timestamp. |
| `cordum_list_pending_approvals` | — | `—` | List approval requests currently waiting for a human decision across both job approvals and MCP tool-call approvals. Use this when the operator says 'what needs my approval?' or before batch-approving with cordumctl. |
| `cordum_list_runs` | — | `—` | List workflow runs the tenant has initiated, newest first. Includes run id, workflow id, state, start/end timestamps. Use this when the operator asks 'which workflows are running now?' or wants to page through recent runs before opening one. |
| `cordum_list_topics` | — | `—` | List job topics registered on the platform — the allow-listed channels jobs can be published to. Use this when the operator asks 'what topics can I submit to?' or is authoring a policy and needs the topic catalogue. |
| `cordum_list_workers` | — | `—` | List workers currently registered (both in-cluster and external). Each entry has worker id, pool, capabilities, last-seen, status. Use this when the operator asks 'which agents are online?' or 'is worker X reachable?'. |
| `cordum_list_workflows` | — | `—` | List workflow definitions available to the tenant. Each entry has workflow id, version, human title, and step count. Use this when the operator asks 'what workflows do I have?' or needs a workflow id before triggering a run. |
| `cordum_query_policy` | — | `—` | Simulate policy evaluation without submitting a job. |
| `cordum_register_agent` | required | `mcp_write_admin` | Register a new AI agent identity so it can authenticate against the MCP gateway. Use this when: the operator wants to bring a new CI bot, agent framework, or LLM client onto the platform. Returns the stable agent ID. This tool requires human approval by default. On first call, expect a JSON-RPC -32099 error with an approval_id in the data — surface the dashboard link (/approvals?mcp=<id>) to the operator and retry the call after they approve. |
| `cordum_reject_job` | — | `—` | Reject a job that requires human approval before execution. |
| `cordum_revoke_worker_session` | required | `mcp_write_admin` | Revoke a worker's active session credential, forcing it to re-authenticate. Use this when: a credential has been compromised, rotated, or the worker is being decommissioned. This tool requires human approval by default. On first call, expect a JSON-RPC -32099 error with an approval_id in the data — surface the dashboard link (/approvals?mcp=<id>) to the operator and retry the call after they approve. |
| `cordum_run_timeline` | — | `—` | Return the ordered timeline of state transitions and step events for a workflow run. Use this when debugging — the operator asks 'what happened in run X?' or 'where did run X get stuck?'. Output is a list of {timestamp, event_type, step_id, details}. |
| `cordum_set_agent_scope` | required | `mcp_write_admin` | Update an agent's authorized tool list and mutating-tool preapproval allowlist. Use this when: the operator wants to grant / revoke capabilities for an existing identity. The `preapproved_mutating_tools` field is high-privilege — agents on that list bypass human approval for the listed mutating calls. Reserve for CI bots. This tool requires human approval by default. On first call, expect a JSON-RPC -32099 error with an approval_id in the data — surface the dashboard link (/approvals?mcp=<id>) to the operator and retry the call after they approve. |
| `cordum_status` | — | `—` | Report platform health at a glance: queue depth, per-component readiness, last policy snapshot, active worker count. Use this when the operator asks 'is Cordum healthy?' or 'how far behind is the scheduler?'. |
| `cordum_submit_job` | — | `—` | Submit a new job to Cordum for agent execution. |
| `cordum_trigger_workflow` | — | `—` | Start a workflow run with input parameters. |
| `cordum_uninstall_pack` | required | `mcp_write_admin` | Uninstall a previously installed pack, revoking its capabilities. Use this when: the operator asks to 'remove' or 'uninstall' a pack or flags one as compromised. This tool requires human approval by default. On first call, expect a JSON-RPC -32099 error with an approval_id in the data — surface the dashboard link (/approvals?mcp=<id>) to the operator and retry the call after they approve. |
| `cordum_update_policy_bundle` | required | `mcp_write_admin` | Save a new version of a policy bundle. The gateway signs the content with the tenant's policy-signing key before persisting — the MCP client never holds the private key. Use this when: the operator wants to tighten / loosen a rule set or deploy a drafted bundle. This tool requires human approval by default. On first call, expect a JSON-RPC -32099 error with an approval_id in the data — surface the dashboard link (/approvals?mcp=<id>) to the operator and retry the call after they approve. |

<!-- END:mcp-tools -->

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

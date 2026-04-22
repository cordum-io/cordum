# MCP Tools — Read-Only Discovery Surface

Cordum's MCP server exposes ~20 tools any conforming MCP client (Claude
Code, Cursor, VS Code, custom LLM apps) can call to operate the
platform in natural language. This page catalogs every **read-only
tool** shipped in task-466b6a6a: 14 new discovery tools that let the
LLM inspect jobs, runs, workflows, packs, topics, workers, agents,
pending approvals, and the audit chain.

Write-path tools (`cordum_submit_job`, `cordum_approve_job`, etc.) are
covered in [per-tool-approval.md on GitHub](https://github.com/cordum-io/cordum/blob/main/docs/mcp/per-tool-approval.md) and
gated through the approval workflow.

All tools are tenant-scoped by the gateway middleware. An unauthenticated
call receives `-32603 internal_error`; a cross-tenant call is silently
filtered (empty list, 404 on resource dereference).

---

## Pagination envelope

Every list tool returns:

```json
{
  "items": [ /* tool-specific records */ ],
  "next_cursor": "opaque-base64-string",
  "total": 123
}
```

* `page_size` default 50, maximum 500.
* `cursor` is opaque — pass back verbatim to resume.
* `next_cursor` is empty on the final page.

See [docs/mcp/resources.md §pagination](./resources.md#pagination) for
details on the cursor shape.

---

## Tool catalogue

### `cordum_list_jobs`
**Purpose:** List jobs the caller's tenant has submitted.
**When to use:** operator asks "what jobs ran today?" or "any failures
in the last hour?".
**Input:** `{cursor?, page_size?, filter?: {state, topic, tenant}}`.
**Output:** `items: [{id, topic, state, submitter, created_at, updated_at}]`.
**Example prompt:** "list jobs in state=failed from the last hour"
→ `cordum_list_jobs({filter: {state: "failed"}})`.
**Failure modes:** `-32602 invalid_params` if `filter` has unknown keys;
pagination errors if `cursor` is malformed.

### `cordum_get_job`
**Purpose:** Fetch the full record for a single job by id.
**When to use:** operator asks "why did job X fail?" or "show me job X".
**Input:** `{id}`.
**Output:** full job record including prompt, policy decision, retry
history, final state.
**Failure modes:** `-32602` if `id` is empty; 404 BridgeError wrapped
when the job does not exist.

### `cordum_list_runs`
**Purpose:** List workflow runs, newest first.
**Input:** `{cursor?, page_size?, filter?: {workflow_id, state}}`.
**Output:** `items: [{id, workflow_id, state, started_at, ended_at}]`.
**Example prompt:** "what workflow runs are active?" →
`cordum_list_runs({filter: {state: "running"}})`.

### `cordum_get_run`
**Purpose:** Fetch a workflow run by id with graph state.
**Input:** `{id}`.
**Output:** `{id, workflow_id, state, steps, inputs, outputs}`.

### `cordum_run_timeline`
**Purpose:** Ordered event timeline for a run.
**Input:** `{id}`.
**Output:** `{events: [{timestamp, event_type, step_id, details}]}`.
**When to use:** "what happened in run X?" / "where did run X get stuck?".

### `cordum_list_workflows`
**Purpose:** List available workflow definitions.
**Input:** `{cursor?, page_size?, filter?: {id}}`.
**Output:** `items: [{id, version, title, step_count}]`.

### `cordum_list_packs`
**Purpose:** List installed integration packs.
**Input:** `{cursor?, page_size?, filter?: {id, enabled}}`.
**Output:** `items: [{id, version, enabled, installed_at}]`.

### `cordum_list_topics`
**Purpose:** List registered job topics.
**Input:** `{cursor?, page_size?, filter?: {name, pool}}`.
**Output:** `items: [{name, pool, input_schema_id, output_schema_id}]`.

### `cordum_list_workers`
**Purpose:** List currently registered workers.
**Input:** `{cursor?, page_size?, filter?: {pool, status}}`.
**Output:** `items: [{id, pool, capabilities, last_seen, status}]`.

### `cordum_list_agents`
**Purpose:** List configured MCP agent identities.
**Input:** `{cursor?, page_size?, filter?: {status, risk_tier}}`.
**Output:** `items: [{id, allowed_tools, risk_tier, data_classifications, status}]`.

### `cordum_list_pending_approvals`
**Purpose:** List approvals waiting for human decision (jobs + MCP).
**Input:** `{cursor?, page_size?, filter?: {status, tool_name}}`.
**Output:** `items: [{id, owner_kind, tool_name|job_id, requester, created_at, expires_at}]`.
**Defaults** `status=pending` when unspecified.

### `cordum_audit_query`
**Purpose:** Search the audit chain for SIEMEvents.
**Input:** `{cursor?, page_size?, filter?, tenant?, event_type?, since?, until?}`.
**Output:** `items: [{seq, timestamp, event_type, tenant_id, agent_id, extra, event_hash, prev_hash}]`.
**Since/until** accept RFC3339 timestamps (`2026-04-17T00:00:00Z`).
**Admin-only** for cross-tenant queries.

### `cordum_audit_verify`
**Purpose:** Walk the tenant's Merkle audit chain and report integrity.
**Input:** `{tenant?}`.
**Output:** `{status: "ok"|"compromised"|"partial", total_events, verified_events, gaps}`.
**Admin-only.**

### `cordum_status`
**Purpose:** Platform-wide health snapshot.
**Input:** `{}` (no args).
**Output:** `{components: {nats, redis, safety_kernel, scheduler, gateway}, queue_depth, active_workers, last_policy_snapshot}`.

---

## Audit

Every tools/call emits a `mcp.tool_called` SIEMEvent with
`Extra = {tool_name, agent_id, tenant, duration_ms, result_size}` —
landing in the per-tenant Merkle audit chain via the existing
`core/audit` pipeline. Denied calls emit `mcp.tool_denied` instead; see
[scope-filtering.md on GitHub](https://github.com/cordum-io/cordum/blob/main/docs/mcp/scope-filtering.md).

## Error codes

| JSON-RPC code | Meaning                                       |
| ------------- | --------------------------------------------- |
| `-32602`      | Invalid params (missing required fields, etc.) |
| `-32097`      | Approval gate misconfigured (middleware bug)  |
| `-32098`      | Not authorized (scope filter denied)          |
| `-32099`      | Approval required (see `error.data.approval_id`) |
| `-32603`      | Internal error (gateway reachability, etc.)   |

## Related

* [docs/mcp/resources.md](./resources.md) — the `cordum://` URI scheme.
* [docs/mcp/quickstart-claude-code.md](./quickstart-claude-code.md)
* [docs/mcp/quickstart-cursor.md](./quickstart-cursor.md)
* [docs/mcp/quickstart-vscode.md](./quickstart-vscode.md)

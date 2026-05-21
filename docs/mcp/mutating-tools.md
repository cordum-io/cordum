# MCP mutating tools

Cordum's MCP surface exposes seven mutating tools that let an operator fully provision
and administer the platform over natural language. Every one of them is gated by a
per-call human approval by default — the LLM client cannot execute the call until a
human in the loop signs off in the dashboard or via `cordumctl mcp approve`.

This document is the canonical reference for each tool: purpose, input schema,
approval flow, failure modes, idempotency semantics, and the audit trail fields an
operator can expect to see.

Scope-based preapproval (for CI / release bots) is described in a companion document:
[scope-preapproval.md](./scope-preapproval.md).

## Contents

- [How the approval flow works](#how-the-approval-flow-works)
- [Catalogue](#catalogue)
  - [cordum_create_workflow](#cordum_create_workflow)
  - [cordum_install_pack](#cordum_install_pack)
  - [cordum_uninstall_pack](#cordum_uninstall_pack)
  - [cordum_register_agent](#cordum_register_agent)
  - [cordum_update_policy_bundle](#cordum_update_policy_bundle)
  - [cordum_revoke_worker_session](#cordum_revoke_worker_session)
  - [cordum_set_agent_scope](#cordum_set_agent_scope)
- [Audit trail fields](#audit-trail-fields)
- [Worked example: end-to-end create_workflow](#worked-example-end-to-end-create_workflow)

## How the approval flow works

When an LLM client calls a mutating tool for the first time, the server refuses the
call with a distinctive JSON-RPC error and returns an `approval_id` the client can
surface to the operator.

```
LLM → MCP server: tools/call { name: "cordum_install_pack", args: {...} }
MCP server → LLM: JSON-RPC error -32099
                  { code: -32099,
                    message: "tool requires human approval",
                    data: { approval_id: "apr-7f3e…", approve_url: "/approvals?mcp=apr-7f3e…" } }
```

The LLM's job is to surface `approve_url` to the operator (via chat, a banner, or
whatever UX the agent framework provides). The operator clicks through, reviews the
canonical args in the dashboard's approval modal, and clicks **Approve** (or rejects).

The LLM then retries the same tool call with **identical arguments**. The gateway
canonical args hash from task-94b27344 is computed with whitespace / key-order /
empty-field normalisation (task-2d989055 step 4), so the retry matches even if the
LLM reformats the JSON. On the retry, the gate consumes the approval record
atomically (CAS) and the handler runs.

If the operator rejects, the retry hits the rejection and returns
`approval_rejected`. The LLM should stop rather than loop.

### Retries, expiry, and idempotency

Approval records are consume-once. If the handler runs, succeeds, and the LLM
retries again (e.g. after a network glitch), the gate sees no matching record and
enqueues a **new** approval. That second retry is what `idempotency_key` is for:
set it once per logical call and the downstream gateway dedupes, returning the
prior result without re-creating the resource.

Approval records expire after 30 minutes (configurable via
`CORDUM_MCP_APPROVAL_TTL`). After expiry, the next retry enqueues a fresh approval
— the LLM should surface the new link to the operator.

## Catalogue

### cordum_create_workflow

**Purpose.** Register a new workflow definition from a spec so it can be triggered
by `cordum_trigger_workflow` later.

**Input schema.**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `steps` | object | yes | Step definitions keyed by step id. TOP-level field — do NOT wrap in a `spec` object. |
| `id` | string | no | Stable workflow id. Generated server-side when omitted. |
| `name` | string | no | Human-readable name. |
| `description` | string | no | Free-form description. |
| `org_id` | string | no | Tenant; defaults to caller's tenant. |
| `team_id` | string | no | Team label. |
| `version` | string | no | Workflow version tag. |
| `timeout_sec` | int | no | Soft timeout for the whole run (seconds, max 604800 = 7 days). |
| `config` | object | no | Workflow-wide config. |
| `parameters` | array | no | Declared input parameters. |
| `input_schema` | object | no | JSON Schema validated per run. |
| `idempotency_key` | string | no | Retry-safe key — gateway dedupes. |

**Approval scope.** `mcp_write` (medium risk tier).

**Output.** `{ "workflow_id": "wf-<uuid>", "version": "v1", "status": "created" }`.

**Failure modes.**

- `invalid_request` — missing spec / name collision inside the spec.
- `conflict` — workflow ID already exists (use the idempotency key to retry safely).
- `-32099 approval_required` — human approval pending; retry after approve.

**Idempotency.** The workflow store's `TrySetRunIdempotencyKey` already dedupes
duplicate create-workflow posts when the `Idempotency-Key` header matches.

**Audit.** `mcp.tool_approval(outcome=enqueued|approved|consumed)` pair +
`mcp.tool_invocation(approval_status=consumed, tool_name=cordum_create_workflow)`.

### cordum_install_pack

**Purpose.** Install a marketplace pack so its capabilities become available to
agents. Targets `POST /api/v1/marketplace/install` (JSON); `POST /api/v1/packs/install`
is a multipart-upload admin endpoint and is NOT what the MCP tool uses.

**Input schema — two modes.**

*Marketplace lookup* (preferred):

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `pack_id` | string | yes (catalog mode) | Canonical ID, e.g. `cordum/slack`. |
| `catalog_id` | string | no | Marketplace catalog override. |
| `version` | string | no | Pinned version; defaults to marketplace latest. |

*Direct URL* (air-gapped / private):

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `url` | string | yes (url mode) | Direct pack bundle URL. |
| `sha256` | string | yes (url mode) | Expected SHA-256 digest. Required whenever `url` is set. |

*Common flags:*

| Field | Type | Description |
|-------|------|-------------|
| `force` | bool | Overwrite existing install. |
| `upgrade` | bool | Allow version bump over an existing install. |
| `inactive` | bool | Install disabled (explicit activation required). |
| `idempotency_key` | string | Retry-safe key. |

**Approval scope.** `mcp_write` (medium risk tier).

**Output.** `{ "pack_id": "...", "version": "...", "installed": true }`.

**Failure modes.** `not_found` (unknown pack); `already_installed` (409; use a new
version or `cordum_uninstall_pack` first); `entitlement_exceeded` (license tier
limit hit).

### cordum_uninstall_pack

**Purpose.** Uninstall a previously installed pack, revoking its capabilities.

**Input schema.**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `pack_id` | string | yes | Installed pack ID |
| `reason` | string | no | Audit-visible justification |
| `idempotency_key` | string | no | Retry-safe key |

**Approval scope.** `mcp_write_admin` (high risk tier).

**Failure modes.** `not_found` (pack not installed); `in_use` (active workflows
depend on this pack — returns the dependency list).

### cordum_register_agent

**Purpose.** Register a new AI agent identity so it can authenticate against the
MCP gateway. Targets `POST /api/v1/agents`.

**Input schema.**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | yes | Human-readable name (e.g. `release-bot`). |
| `owner` | string | yes | Owning team/org for audit attribution. |
| `risk_tier` | enum | yes | `low` \| `medium` \| `high` \| `critical`. |
| `description` | string | no | What the agent does. |
| `team` | string | no | Department label. |
| `allowed_topics` | array | no | Topics this agent may drive jobs into. |
| `allowed_pools` | array | no | Worker pools this agent may schedule against. |
| `allowed_servers` | array | no | MCP server-name globs this agent may call. Omitted/empty fail-closes server-guarded MCP actions. |
| `allowed_tools` | array | no | MCP tools the agent may call. |
| `allowed_resources` | array | no | `cordum://` resource URI globs this agent may target. Omitted/empty fail-closes resource-guarded MCP actions. |
| `entitlements` | array | no | Capability tokens for MCP actions that declare `required_entitlement`. |
| `data_classifications` | array | no | Clearance labels. |
| `idempotency_key` | string | no | Retry-safe key. |

Note: the gateway generates the agent `id` server-side. Don't send one from the
MCP tool — the previous contract allowed it and the gateway silently dropped it.

**Approval scope.** `mcp_write_admin` (high risk tier).

**Output.** `{ "id": "agt-<uuid>", "name": "...", "owner": "...", "risk_tier": "...", "registered": true }`.

**Failure modes.** `missing_required` (400 when name/owner/risk_tier absent);
`invalid_risk_tier` (must be low/medium/high/critical); `tenant_mismatch`
(cross-tenant register forbidden).

### cordum_update_policy_bundle

**Purpose.** Save a new version of a policy bundle. The gateway signs the content
with the tenant's policy-signing key (task-fcd39725) before persisting — the MCP
client **never** holds the private key.

**Input schema.**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `bundle_id` | string | yes | Policy bundle ID (e.g. `secops/core`) |
| `content` | string | yes | Full YAML content |
| `author` | string | no | Audit actor — defaults to the calling principal |
| `message` | string | no | Commit-style message |
| `enabled` | bool | no | Whether the bundle is active after save |
| `idempotency_key` | string | no | Retry-safe key |

**Approval scope.** `mcp_write_admin` (high risk tier).

**Output.** `{ "bundle_id": "...", "updated_at": "...", "signed": true, "key_id": "prod-key-1" }`.

**Failure modes.**

- `invalid_request` — empty content / missing bundle_id.
- `yaml_parse_error` — YAML did not parse (422 + parser messages).
- `signing_unavailable` (503) — strict mode but no signing key configured; the
  gateway refuses to persist an unsigned bundle. Operator action required.
- `-32099 approval_required` — standard approval flow.

**Kernel reload.** The safety kernel's config watcher picks up the new signed
bundle on the next tick (sub-second). No MCP-side wait is required.

### cordum_revoke_worker_session

**Purpose.** Revoke a worker's active **session** (not the persistent credential),
forcing it to re-authenticate on the next connect. Targets
`POST /api/v1/workers/{id}/revoke-session`. The DELETE-on-credentials path was
never the right target — that revokes the whole credential, a strictly broader
operation than the DoD required.

**Input schema.** `{ "worker_id": "...", "reason": "...", "idempotency_key": "..." }`.

**Approval scope.** `mcp_write_admin` (high risk tier).

**Reason.** `reason` travels in the JSON body; the gateway surfaces it on the
`worker_trust_change` SIEMEvent so forensic tools can reconstruct the
revocation motive.

**Failure modes.** `no_active_session` → 200 with `{"revoked": true, "reason":
"no_active_session"}` — the call is idempotent by design so CI scripts can
retry safely.

### cordum_set_agent_scope

**Purpose.** Update an agent's authorized MCP scope, including tool/server/
resource/entitlement allowlists and mutating-tool preapproval.

**Input schema.**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `agent_id` | string | yes | Agent to update |
| `allowed_tools` | array<string> | yes (pass `[]` to clear) | Full replacement list of MCP tool names |
| `allowed_servers` | array<string> | no (pass `[]` to clear) | Full replacement list of MCP server-name globs |
| `allowed_resources` | array<string> | no (pass `[]` to clear) | Full replacement list of `cordum://` resource URI globs |
| `entitlements` | array<string> | no (pass `[]` to clear) | Full replacement list of required-entitlement tokens |
| `preapproved_mutating_tools` | array<string> | yes (pass `[]` to clear) | Tools this agent may call without human approval |
| `idempotency_key` | string | no | Retry-safe key |

**MCPGate fail-closed semantics.** `AllowedServers`, `AllowedResources`, and
`Entitlements` are enforced by the action-gate layer after identity resolution.
Leaving a list absent or empty does **not** mean wildcard; server-guarded,
resource-guarded, and required-entitlement MCP actions deny with
`server_not_allowlisted`, `resource_not_allowlisted`, or `unlicensed`.

**Approval scope.** `mcp_write_admin` (high risk tier).

**`preapproved_mutating_tools` semantics.** Each entry is either an exact tool
name or a `prefix*` trailing-glob (e.g. `cordum_install_*` matches
`cordum_install_pack` but not `cordum_uninstall_pack`). Leading-`*` and
interior-`*` are deliberately refused to keep the grammar auditable. See
[scope-preapproval.md](./scope-preapproval.md) for when preapproval is
appropriate and how to audit it.

**Output.** `{ "agent_id": "...", "allowed_tools": [...], "preapproved_mutating_tools": [...] }`.

## Audit trail fields

Every mutating call produces two SIEMEvents that land on the Merkle audit chain:

1. `mcp.tool_approval(outcome=enqueued)` — emitted by the gate when it enqueues.
   Extra carries `tool_name`, `args_hash`, `approval_id`, `requester`,
   `principal`, `reason`.

2. `mcp.tool_approval(outcome=approved|rejected|expired)` — emitted by the store
   when the human acts. Extra adds `resolver`, `resolution_reason`, `decision`.

3. `mcp.tool_approval(outcome=consumed)` — emitted by the gate when the LLM's
   retry claims the approved record. Adds `consumed_at`.

4. `mcp.tool_invocation(approval_status=consumed|preapproved)` — emitted by the
   tool-invocation auditor when the handler runs. Carries `tool_name`,
   `agent_id`, `tenant`, `duration_ms`, `result_size`, `approval_id` (absent
   when `approval_status=preapproved`).

The preapproved-bypass flow skips emissions 1-3 — no approval record exists —
but still emits 4 with `approval_status=preapproved` so forensics can distinguish
scope bypass from consumed human approval.

## Worked example: end-to-end create_workflow

Operator goal: register a "hello-world" workflow via Claude Code.

**Step 1.** Operator asks Claude Code: *"Create a workflow that logs 'hello' and
exits cleanly."*

**Step 2.** Claude Code calls `cordum_create_workflow`:

```json
{
  "name": "hello-world",
  "description": "Minimal demo workflow",
  "spec": {
    "steps": [
      { "id": "log", "type": "log", "input": { "message": "hello" } }
    ]
  },
  "idempotency_key": "session-0f3a-call-1"
}
```

**Step 3.** The gate returns JSON-RPC -32099:

```json
{
  "code": -32099,
  "message": "tool \"cordum_create_workflow\" matches approval scope \"mcp_write\"",
  "data": { "approval_id": "apr-1a2b3c", "tool": "cordum_create_workflow" }
}
```

Claude Code surfaces: *"This tool needs approval. Approve at:
http://localhost:8081/approvals?mcp=apr-1a2b3c"*

**Step 4.** The operator opens the dashboard, clicks the approval, reviews the
canonical args in the modal, and clicks **Approve**.

**Step 5.** Claude Code re-issues the same call with the same
`idempotency_key`. The gate claims the approved record, the handler runs, and
the response is:

```json
{ "workflow_id": "wf-7a6c8d", "version": "v1", "status": "created" }
```

**Step 6.** The audit chain now carries four events:

| # | event_type | Extra |
|---|-----------|-------|
| 1 | mcp.tool_approval | outcome=enqueued, tool_name=cordum_create_workflow, approval_id=apr-1a2b3c |
| 2 | mcp.tool_approval | outcome=approved, resolver=operator@acme |
| 3 | mcp.tool_approval | outcome=consumed, consumed_at=… |
| 4 | mcp.tool_invocation | approval_status=consumed, tool_name=cordum_create_workflow, duration_ms=… |

**Step 7.** Any retry of the same logical call (via
`idempotency_key=session-0f3a-call-1`) returns the same `workflow_id` without
enqueuing a second approval — the workflow store's idempotency middleware
deduplicates downstream.

The full loop — from prompt to registered workflow — took two clicks and one
Claude Code reply. Every step is on the audit chain. The LLM never held a
signing key, a session token, or bypassed a human gate.

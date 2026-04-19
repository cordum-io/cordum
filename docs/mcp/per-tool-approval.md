# MCP per-tool approval gates

Cordum can require human approval before any MCP tool call executes.
The gate runs in the gateway's MCP server, so it covers every MCP
client (dashboard, agents, CLI, third-party automation) without those
clients having to do anything special — a gated tool returns a
JSON-RPC `-32099` error carrying the approval ID, and the same call
re-issued after approval succeeds.

This document covers the threat model, how to flag tools, the human
and automation approval flows, and the audit fields you can rely on
for compliance.

## Threat model

The risks per-tool approval addresses:

| Risk | How the gate mitigates it |
|------|---------------------------|
| **Compromised agent makes a destructive call** (e.g. an LLM jailbreak invokes `db.drop`) | The kernel intercepts the call before the worker handler runs. The agent only ever sees an "approval pending" response; the destructive code path is unreachable until a human approves. |
| **Misconfigured agent calls the wrong tool** (e.g. a code-review agent issues `files.delete` because of a prompt typo) | Same gate — the call never executes without an approver acknowledging the args. |
| **Insider attempts to escalate through their own agent** | The self-approval guard refuses any approval whose approver principal matches the requesting agent's identity. |
| **Replay attack on a previously approved call** | Approvals are consume-once; any second call with identical args re-enqueues a fresh approval. |
| **Approval rubber-stamped without seeing the args** | The approval record carries a SHA-256 hash of the canonical args JSON; the dashboard's "Review args" modal fetches and renders the full args before the approver clicks Approve. |

The gate does **not** protect against:
* Tools that are not flagged `requires_approval`.
* Bugs in the tool handler itself (the gate is about *whether* to call,
  not *what* the call does).
* Out-of-band access to the underlying system that bypasses MCP.

## How to flag a tool

There are two ways to mark a tool as gated. Runtime config wins so you
can flip a flag without a rebuild.

### Code-time

Set `RequiresApproval` on the `mcp.Tool` struct when registering:

```go
registry.Register(mcp.Tool{
    Name:             "files.delete",
    Description:      "Delete one or more files",
    RequiresApproval: true,
    ApprovalScope:    "filesystem",  // optional tag for runtime rules
}, deleteHandler)
```

`ApprovalScope` is an opaque tag that runtime rules can match against;
it has no semantics on its own.

### Runtime config

The system config at `id=mcp_policy` (set via `PUT /api/v1/config?scope=system&id=mcp_policy`) holds an ordered list of glob rules:

```yaml
tools:
  - tool_name_pattern: "fs.temp.*"
    requires_approval: false        # exempt temp-area cleanup
  - tool_name_pattern: "fs.*"
    requires_approval: true
    approval_scope: "filesystem"
  - tool_name_pattern: "db.drop"
    requires_approval: true
    approval_scope: "destructive"
```

Rules are evaluated **in order**; the first matching rule wins. Place
narrow exceptions before broad rules. Setting `requires_approval: false`
explicitly turns OFF a code-gated tool — useful for signed CI bypass.

The endpoint is admin-only. All writes go through the existing config
audit trail.

## Approval flow

### Human (dashboard)

1. Operator navigates to **/approvals** in the dashboard.
2. The "MCP tool calls" section at the top shows pending approvals as
   cards with `Tool / Requester / Tenant / Args hash (short) / Expires`.
3. Clicking **Review args** opens a modal that fetches the full record
   (including the args JSON) for inspection.
4. **Approve** or **Reject** posts to
   `/api/v1/mcp/approvals/{id}/{approve|reject}`.
5. The blocked MCP client retries the original tools/call; the gate
   sees the new APPROVED record, marks it consumed, and the handler
   runs.

### Automation (cordumctl)

```bash
# List what's pending.
cordumctl mcp pending

# Approve, with a reason for the audit trail.
cordumctl mcp approve <approval_id> --reason "release-bot: scheduled cleanup"

# Or reject.
cordumctl mcp reject <approval_id> --reason "wrong tenant"
```

Exit codes:
* `0` — resolved.
* `3` — self-approval refused (you cannot resolve your own call).
* `4` — approval not found (already expired or wrong ID).
* `1` — any other error (network, permissions, etc.).

### REST

```http
GET  /api/v1/mcp/approvals?status=pending
GET  /api/v1/mcp/approvals/{id}
POST /api/v1/mcp/approvals/{id}/approve   {"reason": "..."}
POST /api/v1/mcp/approvals/{id}/reject    {"reason": "..."}
```

Response on a gated MCP tools/call:

```json
{
  "jsonrpc": "2.0",
  "id": 7,
  "error": {
    "code": -32099,
    "message": "approval required",
    "data": {
      "approval_id": "9c6e4f1a8b1b4b2c8e0f1a2b3c4d5e6f",
      "tool": "files.delete",
      "reason": "tool \"files.delete\" matches approval scope \"filesystem\""
    }
  }
}
```

## Args hash semantics

Each approval is keyed on the canonical SHA-256 of the args JSON. Two
calls with semantically identical args (different key order, varying
whitespace) hash to the same value — the canonicaliser does an
`Unmarshal → Marshal` round-trip, and Go's `encoding/json` sorts map
keys at every depth.

Two calls with **different** args produce different hashes and require
**separate approvals**. This is intentional: an approval to delete
`/tmp/foo` must not implicitly authorise deleting `/etc/passwd`.

The args hash is short-displayed in the dashboard table and CLI so
operators can disambiguate at a glance. The full args are visible in
the dashboard modal and via `cordumctl mcp pending --json`.

### Consume-once

When the gate finds a pre-approved record matching the (tenant, agent,
tool, args_hash) tuple, it marks the record `consumed_at` and lets the
call through. Any subsequent call with identical args **re-enqueues a
fresh approval** — there is no replay window.

## Self-approval guard

The handler refuses any approval whose approver principal matches the
record's `requester` (the agent that issued the original tools/call).
The check parses the composite identity string the auth middleware
produces (`apikey:abcd|principal:agent-1`) and compares the principal
segment.

Match → HTTP `403` with body:

```json
{"error": "self-approval not permitted: ...", "code": "self_approval_denied", "status": 403}
```

The record stays `pending` so a different admin can still resolve it.

This guard mirrors the existing job-approval guard in
`handleApproveJob` byte-for-byte — same composite identity, same code
constant — so SIEM rules that already filter on `self_approval_denied`
catch MCP attempts too.

## Timeout

A new approval lives for `MCPApprovalRequest.TTL` (defaults to 5
minutes / 300 seconds). The gate's `SweepExpired` runs from the
existing reaper loop; on each sweep it transitions every PENDING
record whose `expires_at` has passed to `expired` (with
`Decision = expire`).

To override the default for a tool, set the request's `TTL` at enqueue
time. There is no global config knob today — each tool's gate decision
is per-request to keep one slow-approving operator from blocking
unrelated calls.

## Audit trail

Every lifecycle event emits an `audit.SIEMEvent` with:

| Field | Value |
|-------|-------|
| `event_type` | `mcp.tool_approval` |
| `tenant_id` | tenant of the call |
| `agent_id` | requesting agent |
| `action` | one of `enqueued`, `approved`, `rejected`, `expired`, `consumed` |
| `severity` | `INFO` for `approved`/`consumed`, `MEDIUM` for `enqueued`/`rejected`/`expired` |
| `decision` | the model.ApprovalDecision string when set |
| `reason` | the operator's reason (for approve/reject) or the gate's match reason (for enqueue) |
| `identity` | the resolver principal (only for approve/reject) |
| `extra.tool_name` | the MCP tool name |
| `extra.args_hash` | full SHA-256 hex |
| `extra.approval_id` | the approval ID |
| `extra.requester` | the requesting agent ID |
| `extra.outcome` | duplicate of `action`, kept for SIEM rules that filter on Extra |
| `extra.resolver` | resolver principal (when present) |

The events are hashed into the tenant's audit chain by the chainer in
`core/audit`, so any tampering (delete, reorder, alter) breaks the
chain and is detected by `/api/v1/audit/verify`.

## Reference

* Code: `core/mcp/registry.go` (`ApprovalGate` interface, `effectiveApprovalForTool`), `core/controlplane/gateway/mcp_approvals.go` (store + lifecycle), `core/controlplane/gateway/mcp_gate.go` (gate adapter), `core/controlplane/gateway/mcp_approval_handlers.go` (HTTP).
* Tests: `core/mcp/registry_test.go`, `core/mcp/server_test.go` (TestToolsCallApprovalRequiredMapsTo32099), `core/controlplane/gateway/mcp_approvals_test.go`, `core/controlplane/gateway/mcp_gate_test.go`, `core/controlplane/gateway/mcp_approval_handlers_test.go`.
* Dashboard: `dashboard/src/hooks/useMcpApprovals.ts`, `dashboard/src/components/approvals/McpApprovalCard.tsx`, `McpApprovalsSection.tsx`.
* CLI: `cmd/cordumctl/mcp.go`.

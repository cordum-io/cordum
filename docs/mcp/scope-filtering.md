# Scope-based MCP Tool Filtering

Cordum's MCP server exposes a registry of tools to AI agents. By default every
registered tool would be visible to every connected agent — a 200-tool surface
is a large blast radius, especially when one of those tools can delete jobs,
export audit logs, or trigger workflows. Scope filtering narrows that surface
per-agent at `tools/list` and again at `tools/call`, closed by default, and
audits every rejection.

This document is the operator-facing reference for how filtering is decided,
how to configure it, and how to observe it.

## Threat model

What we defend against:

- **Over-broad tool inventory.** An agent whose job is "read Jira tickets"
  should not be able to see or call `jobs.cancel` — even if it never would.
- **Compromised credentials.** A stolen API key lets an attacker connect as
  that identity, but scope filtering caps what the stolen identity can do.
- **Privilege-escalation probes.** Every scope denial is logged with a
  sub-reason so repeated "too-low-tier" attempts against sensitive tools
  surface on the SIEM and on the per-identity dashboard panel.
- **Policy drift.** A runtime misconfiguration cannot weaken a code-declared
  tool: runtime overrides can only tighten, never loosen.

What this does NOT defend against:

- Logic bugs inside an allowed tool. That's the job of input validation,
  approval gating (per-tool approvals), and signed outbound calls.
- An agent misusing a tool it IS allowed to use. That's the job of the
  safety kernel and audit chain.

Scope filtering is one layer in the MCP zero-trust stack; it sits alongside
per-tool approval gates (this epic) and signed outbound MCP calls
(epic-level, tracked separately) to form defence-in-depth.

## The three gates

A tool is visible / callable for an identity only when ALL three gates pass.

### 1. AllowedTools glob match

`AgentIdentity.AllowedTools` is a list of glob patterns matched against
`tool.Name`. `*` matches any name; `fs.*` matches `fs.read`, `fs.write`, and
so on. An empty list means "no tools" — an identity with no configured
tools sees nothing (fail-closed, opt-in).

### 2. Risk-tier ordering

Each tool declares a `RiskTier` (`low < medium < high < critical`). Each
identity declares its own `RiskTier`. Filtering admits a tool only when
`actor_tier >= tool_tier`. An identity at `medium` can call low and medium
tools but not high or critical.

Unknown / empty tier values are treated as `high` — a new tool that forgets
to declare a tier starts restricted, not permissive.

### 3. DataClassifications superset

A tool may declare `DataClassifications` such as `["pii"]` or `["phi",
"secrets"]`. The identity's `DataClassifications` list must be a superset —
every classification the tool touches must also be in the identity's list.
Missing classifications deny the tool. Empty tool classifications skip the
gate entirely.

### Fail-closed defaults

- **No identity on the request context.** `tools/list` returns an empty
  slice; `tools/call` returns `-32098 not_authorized`.
- **Unknown tier on the identity.** Same result as no identity.
- **Empty `AllowedTools`.** Zero tools visible.
- **Unknown tier on a tool.** Treated as `high` — operators must explicitly
  opt a tool into a lower tier.

## Runtime overrides

Operators can tighten (but never loosen) scope requirements at runtime via
the config surface:

```
PUT /api/v1/config
{
  "scope": "system",
  "scope_id": "default",
  "data": {
    "mcp_tool_policy": {
      "tools": [
        {
          "tool_pattern": "fs.*",
          "min_risk_tier": "high",
          "required_data_classifications": ["pii"]
        }
      ]
    }
  }
}
```

Precedence:

- If runtime `min_risk_tier` is strictly higher than code-declared tier,
  runtime wins.
- If runtime `min_risk_tier` is equal or lower, code wins (runtime cannot
  loosen).
- Runtime `required_data_classifications` are UNIONED with code-declared
  values. Operators can add new classification requirements but cannot
  remove existing ones.

The write endpoint is admin-only via the existing handleSetConfig RBAC
check; reads are readable via the standard config GET path.

Runtime changes take effect on the next request. The filter cache is keyed
on `(identity fingerprint, config_version)` so `SetConfig` bumps the version
and invalidates every cached slice at once.

## Identity propagation

### HTTP transport (gateway)

`mcpAuth` middleware on `/mcp/message` + `/api/v1/mcp/message` reads the
`X-Agent-Id` header. If present, it resolves via the agent identity store
and attaches the resulting `*mcp.AgentIdentity` to the request context.
If absent, it falls back to the auth-principal's linked worker identity
(useful for workers that already authenticate with a credentialed
`PrincipalID`).

The HTTP transport stamps the resolved identity onto `JSONRPCMessage`
before enqueuing it for the MCP dispatcher.

### stdio transport (cordum-mcp)

Set `--agent-id <X>` or `CORDUM_MCP_AGENT_ID=<X>`. The binary resolves the
identity once at boot via `GET /api/v1/agents/<X>`. On any failure
(network, 404, revoked/suspended) the process exits non-zero — there is
no silent fallback. Once resolved, the `StdioTransport.SetDefaultIdentity`
stamps every inbound message.

Scope enforcement is on by default in the gateway HTTP transport. In
stdio it is on only when an agent-id has been resolved — so the legacy
"dev mode without credentials" path still works for local debugging.

## Observability

### mcp_tool_denied SIEM events

Every `tools/call` that fails the filter emits `audit.EventMCPToolDenied`
with:

| Field        | Value |
|--------------|-------|
| `event_type` | `mcp.tool_denied` |
| `severity`   | `HIGH` |
| `agent_id`   | The denied identity |
| `tenant_id`  | Tenant derived from request context |
| `extra.tool_name`  | The tool name that was denied |
| `extra.sub_reason` | `tool_not_in_allowed_list` \| `risk_tier_too_low` \| `missing_data_classification` \| `no_identity` |

These flow through the same audit chain used by safety decisions, so SIEM
rules can correlate scope denials with broader activity.

### Dashboard tab

The `/agents/:id` page has a "Tool visibility" tab (this task). It renders
two cards:

1. **Tools this identity can see.** Calls
   `GET /api/v1/agents/:id/tools` — shows the filtered catalogue with
   risk tier, classifications, and approval markers.
2. **Recent denials.** Calls `GET /api/v1/agents/:id/denied-events` —
   shows the last 50 `mcp_tool_denied` events with their sub-reason.

### cordumctl

```
cordumctl mcp tools list --agent-id <X>      # what X can see
cordumctl mcp tools list                     # full admin catalogue
cordumctl mcp tools list --agent-id <X> --json   # machine-readable
```

## Relationship to sibling MCP controls

Scope filtering is one of three zero-trust controls shipped under
Phase 1: MCP Zero-Trust Governance.

- **Per-tool approval gates.** When a tool has `RequiresApproval=true`
  (or matches an `mcp_policy` runtime rule), a call must wait for
  human approval. Scope filtering runs FIRST — a call an identity
  doesn't have the scope for never reaches the approval gate, so
  attackers can't probe the approval workflow to enumerate the tool
  surface.
- **Signed outbound MCP calls** (task-ba236f62). When Cordum acts
  as an MCP CLIENT calling external MCP servers, every call is
  signed with the caller's ECDSA P-256 key so the remote can
  verify the caller's identity. Scope filtering governs what a
  caller can REQUEST; signing proves who made the request.

All three controls operate independently. Defence in depth: a
privilege-escalation probe must pass the scope filter, satisfy the
approval gate, and be signed by a key the remote MCP trusts before any
effect is observable.

## Operational checklist

- Every agent identity has a populated `AllowedTools`, `RiskTier`, and
  (when needed) `DataClassifications`.
- High-sensitivity tools (`jobs.delete`, `workflows.cancel`,
  `audit.export`) are declared at `risk_tier: high` or `critical` in
  their code registration.
- Runtime `mcp_tool_policy` overrides are reviewed in the same change-
  management flow as safety rules.
- The dashboard "Recent denials" panel is checked weekly per critical
  identity; repeated denials for the same tool usually mean the
  identity's scope is out of step with its workload and should be
  rescoped rather than expanded ad-hoc.

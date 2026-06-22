# MCP tool-call policy gate (EDGE-102)

Cordum's MCP server routes every `tools/call` request through the Edge
action-policy pipeline before forwarding to the upstream tool handler.
This page documents what the gate evaluates, the decision contract, the
emitted events, and the artifact-pointer semantics so platform operators
and pack authors can reason about how a tool invocation lands in the
audit/compliance trail.

The policy entry-point is `core/mcp.InvokeToolWithPolicy`. The MCP server
wires it via `MCPServer.WithPolicyGate(server, deps)`; an un-wired server
falls through to the legacy direct-dispatch path (dev/test only).

Production action gates are blockers/approval gates. They produce
`ALLOW`, `DENY`, `THROTTLE`, or `REQUIRE_HUMAN`, and they do not emit
enforceable generic constraint maps. Typed runtime constraints still
come from policy bundles and SafetyKernel `PolicyConstraints`.

## Request flow

```
MCP client
   │  tools/call (server, tool, arguments)
   ▼
core/mcp.MCPServer.invokeTool
   │  CallMetadata{tenant, principal, agent_id, session_id, execution_id}
   ▼
core/mcp.EvaluateToolCall
   │  1. fail-closed if CallMetadata absent or tenant empty
   │  2. argument_redactor.Redact(args)
   │  3. verifyRedactionCompleteness — fail closed on sentinel leak
   │  4. BuildActionDescriptorFromToolCall
   │     ├── normalize path-like args ➜ desc.TargetPath
   │     ├── enforce byte-length cap (MaxToolCallArgsBytes = 1 MiB)
   │     └── copy approval_claim arg verbatim into ApprovalClaim.ClaimText
   │  5. PolicyDispatcher.Dispatch — runs the blockers-only actiongate
   │     pipeline in order: tenant → file → url → mcp → mutation → provenance
   │  6. emit mcp.tool.pre OR mcp.tool.failed (deny/throttle) event
   ▼
decision branch
   ├── ALLOW                           ───►  upstream tool handler  ───►  mcp.tool.post
   ├── DENY / THROTTLE                  ───►  short-circuit (no upstream call)
   ├── REQUIRE_HUMAN                    ───►  ApprovalHandoff.ConsumeActionGateDecision
   │                                            ├── invariant check
   │                                            ├── preapproval check
   │                                            └── MCPApprovalStore.Enqueue
   └── upstream error                   ───►  mcp.tool.failed (with sanitized error)
```

## What `actiongate.mcp` evaluates

The MCP gate (`core/policy/actiongates/mcp_gate.go`) inspects the request
identity and the descriptor and decides:

| Check | Source | Failure mode |
| --- | --- | --- |
| `auth.Tenant` non-empty | `core/controlplane/gateway/auth` | DENY `unauthorized / missing_auth` |
| `agent_id` resolves to an MCP identity | `MCPIdentityResolver.ResolveMCPIdentity` | DENY `service_unavailable` on resolver error; DENY `unauthorized / unknown_identity` on miss |
| Server in `AllowedServers` | `mcp.AgentIdentity.AllowedServers` | DENY `forbidden / server_not_allowlisted` |
| Tool in `AllowedTools` | `mcp.AgentIdentity.AllowedTools` | DENY `forbidden / tool_not_allowlisted` |
| `target_resource.id` in `AllowedResources` | `mcp.AgentIdentity.AllowedResources` | DENY `forbidden / resource_not_allowlisted` |
| Identity holds `RequiredEntitlement` | `descriptor.RequiredEntitlement` vs `mcp.AgentIdentity.Entitlements` | DENY `forbidden / missing_entitlement` |
| No DangerousParamRule matches | `MCPGateOptions.DangerousParamRules` (tool glob → name+value) | DENY `forbidden / dangerous_param` |
| Reachability probe succeeds | `ReachabilityProbe` (optional; nil skips) | DENY `service_unavailable / unreachable` |

Cross-tenant target writes are blocked by the upstream `tenant_gate` before
control reaches the MCP gate; the MCP gate trusts the tenant gate for that
boundary and does not re-check `TargetResource.OwnerTenant`.


### Session-taint destructive scope

The session-taint deny is deliberately conjunctive: Cordum denies only when a
prior tool result has tainted the MCP session with prompt-injection content AND
the next tool call is destructive. The destructive classifier is a scoping
predicate, never a standalone policy decision. A clean session issuing the same
delete mutation is not denied by taint, and a tainted session running a read or
benign mutation still flows through the normal allow-list checks.

Destructive scope is detected in two ways:

- tool-name globs (`CORDUM_MCP_DESTRUCTIVE_TOOL_GLOBS`, default
  `*delete*,*remove*,*archive*`);
- GraphQL-proxy arguments: string arguments named by
  `CORDUM_MCP_DESTRUCTIVE_MUTATION_ARG_KEYS` (default
  `query,mutation,gql,graphql`) are scanned for a GraphQL `mutation` document,
  and the invoked field name is matched against
  `CORDUM_MCP_DESTRUCTIVE_MUTATION_GLOBS` (default
  `delete_*,remove_*,archive_*,delete,remove,archive`).

The second path covers generic API passthrough tools (for example an upstream
`all_monday_api` tool) whose tool name is not destructive but whose arguments
carry a destructive mutation such as `delete_item`, `delete_items`, or
`delete_board`. The decision records only the matched identifier (for example
`mutation:delete_item`) in `Extra["taint_destructive_match"]`; it does not copy
raw attacker-controlled GraphQL text into the audit reason.

## Verb classification

When the descriptor carries a destructive `ActionVerb`, the mutation gate
fires after the MCP gate and requires a backend-resolved EdgeApproval (no
claim text grants on its own). The current destructive set is in
`core/policy/actiongates/mutation_gate.go` (`destructiveVerbs`):

| Class | Verbs |
| --- | --- |
| Destruction | `delete`, `drop`, `truncate`, `purge` |
| Data movement | `export`, `backup_restore` |
| Account / access | `revoke_access`, `disable_user`, `rotate_credentials`, `transfer_ownership` |
| Secrets / keys | `secrets_write`, `secrets_delete`, `key_rotate`, `key_delete` |
| Configuration | `config_write`, `config_delete` |
| Tenant lifecycle | `tenant_create`, `tenant_delete` |
| Licensing | `license_grant`, `license_revoke`, `license_change` |
| Financial / governance | `payment_execute`, `payment_approve`, `admin_grant`, `role_assign` |

Read-only and non-mutating verbs short-circuit the mutation gate to the
zero decision; the pipeline continues to the provenance gate for the
universal "no claim text wins" rule.

## Decision semantics

| Decision | Behaviour |
| --- | --- |
| `ALLOW` | Forward to upstream; emit `mcp.tool.pre` then `mcp.tool.post`. |
| `ALLOW_WITH_CONSTRAINTS` | Compatibility/future-typed dispatcher path only. Production action gates do not emit this decision. If a non-actiongate test/future dispatcher returns it, the MCP bridge forwards like ALLOW and emits `Decision=CONSTRAIN` with the provided typed constraints; do not rely on action-gate `Constraints` for enforcement. |
| `DENY` | Short-circuit; emit `mcp.tool.failed`; return `IsError=true` with sanitized reason. |
| `THROTTLE` | Short-circuit; emit `mcp.tool.failed` with code `throttled`; caller back-pressures. |
| `REQUIRE_HUMAN` | Bridge calls `ApprovalHandoff.ConsumeActionGateDecision`; upstream is not invoked. |

Failure-closed is the universal default. Missing metadata, an artifact-
store outage on an oversized event, a redaction completeness leak, or a
nil `EventEmitter` all reject with no event emitted (we never write an
unattributed audit row).

> **Constraints persistence (post-EDGE-102).** Production action gates
> are blockers-only and do not populate enforceable generic
> `Constraints`. The MCP bridge still has a compatibility data plane:
> if a non-actiongate/future typed dispatcher returns
> `ALLOW_WITH_CONSTRAINTS`, the constraint identifiers and parameters are
> carried on the typed `AgentActionEvent.Constraints` field
> (`map[string]any` with `json:"constraints,omitempty"`, defined at
> `core/edge/event.go`). The field is populated on `mcp.tool.pre`,
> `mcp.tool.post` (`Decision=CONSTRAIN`), and `mcp.tool.failed` events,
> per `task-3d5c4f37` follow-up commits
> [`30c07614`](https://github.com/cordum/cordum/commit/30c07614)
> (EDGE-102 `MCPServer.WithPolicyGate` wire-up at gateway boot) and
> [`453ed0f4`](https://github.com/cordum/cordum/commit/453ed0f4)
> (mcp-tool-policy AWC decision-row alignment). Structured logs emit
> `constraint_count` only via `logToolCallDecision` — constraint
> **values** are never logged. Dashboard and audit consumers read the
> typed `Constraints` payload off the persisted event via the standard
> `SessionExportAssembler` path (`core/edge/export.go`); the redaction
> contract still applies, so any constraint value-shaped fields stay
> bounded to identifiers and structured parameters, never freeform
> tool input. Today, normal typed runtime constraints are produced by
> policy bundles and SafetyKernel `PolicyConstraints`, not by production
> action gates.

## Approval flow integration

`REQUIRE_HUMAN` decisions route through the gateway-side
`gatewayApprovalGate.ConsumeActionGateDecision` with the
canonical action hash:

```
ActionHash = sha256("<tenant> | <server> | <tool> | <normalized-target-path>")
```

Path normalization (backslash → forward slash) is applied in
`core/mcp.CanonicalActionHash` so Windows and POSIX callers operating on
the same logical file converge on a single approval lifecycle key.

Precedence at the approval-handoff site (matches `gatewayApprovalGate.Check`):

1. **MCP invariants** — `ErrMCPInvariantDeny` always wins, even over an
   action-gate ALLOW from a pack-contributed rule.
2. **Preapproval** — short-circuit; no approval-store write.
3. **Approval store** — `ClaimPreApproved` first, else
   `EnqueueMCPApproval` and surface the returned reference as
   `approval pending: <ref>` content to the client.

The bridge's `mcp.tool.pre` event carries `Decision = require_approval` so
the audit trail records the gating point even if the lifecycle resolves
asynchronously.

Single-use approval semantics are enforced by the Edge approval store's
`ClaimApproval` CAS consume path when the retry presents the bound
`approval_ref`. Mutation/action gates may add `single_use=true` as an
audit breadcrumb on ALLOW, but no action-gate constraint map enforces
the one-shot transition.

For destructive verbs, the later provenance gate does not treat claim text or a
pending/requested approval event as proof. It requires the backend approval to
be approved and the tenant audit chain to contain a resolved approval event
(`EventEdgeApprovalResolved` / `edge.approval_resolved`) with decision
`approved` or `approve` and exact tenant, `approval_ref`, and `action_hash`
matches. Requested-only, malformed, wrong-ref/hash, rejected/expired, or
unverifiable audit evidence fails closed before the tool invocation is allowed.
The verification uses the shared audit chain/window limits and never persists
raw tool arguments or outputs as provenance evidence.

## Emitted events

All three event kinds populate `core/edge.AgentActionEvent` with the
session-correlation fields the gateway middleware stashed into context:

| Field | Source |
| --- | --- |
| `event_id` | `deps.EventIDFactory()` (uuid-shaped 32 hex chars by default) |
| `session_id` | `CallMetadata.SessionID` |
| `execution_id` | `CallMetadata.ExecutionID` |
| `tenant_id` | `CallMetadata.Tenant` |
| `principal_id` | `CallMetadata.Principal` |
| `layer` | `edge.LayerMCP` |
| `kind` | `mcp.tool.pre`, `mcp.tool.post`, or `mcp.tool.failed` |
| `tool_name` / `action_name` | `params.Name` |
| `decision` | edge-decision enum (see Decision semantics) |
| `decision_reason` | gate's `Reason` (already redacted; never carries raw upstream error text) |
| `input_redacted` | argument_redactor output (inline ≤ `edge.MaxInputRedactedBytes`) |
| `artifact_pointers` | populated on size > 64 KiB OR high-severity finding |
| `error_message` | populated on `mcp.tool.failed` from upstream errors; pre-sanitized |

The retry-dedupe key is `<server>|<tool>|<event_id>`; a stable
`EventIDFactory` produces idempotent pre/post pairs on transient retries.

### Redacted fields

The default argument redactor scrubs by field-name (case-insensitive) and
by regex heuristic. Operators can layer additional rules through the
policy bundle's `policy.mcp.argument_redaction.rules` section.

**Field-name strip (replaces value with `[REDACTED:<family>]`):**

- `password`, `passwd`, `pin_code` (and any operator-added field)
- `api_key`, `apiKey`, `apikey`
- `token`, `access_token`, `refresh_token`
- `authorization`
- `secret`, `client_secret`
- `private_key`, `privateKey`

**Regex heuristics (replaces matching substring):**

| Pattern | Family |
| --- | --- |
| `AKIA[0-9A-Z]{16}` | `aws_access_key` |
| `sk_live_[a-zA-Z0-9]{24,}` | `stripe_secret` |
| `sk-[A-Za-z0-9_\-]{16,}` | `api_key` (Anthropic-style and similar) |
| `gh[opusr]_[A-Za-z0-9]{16,}` | `github_token` (PAT/oauth/server/user/refresh) |
| `eyJ[a-zA-Z0-9_\-]+\.[a-zA-Z0-9_\-]+\.[a-zA-Z0-9_\-]+` | `jwt` |
| `-----BEGIN [A-Z ]+PRIVATE KEY-----...-----END...-----` | `pem_private_key` |

### Defense-in-depth completeness check

After the redactor runs, `verifyRedactionCompleteness` re-scans the
output for the high-severity sentinel set. If any pattern survives (rule
misconfig, partial match, hostile redactor stub), `EvaluateToolCall`
returns `redaction_failed` and emits **no event**. The contract is that
no raw credential ever lands in a Redis-persisted audit row, even if the
upstream rule set was incomplete.

## Artifact-pointer thresholds

| Bound | Threshold | Behaviour |
| --- | --- | --- |
| Hard request cap | 1 MiB (`MaxToolCallArgsBytes`) | `BuildActionDescriptorFromToolCall` returns `args_too_large`; nothing reaches the gate. |
| Inline event budget | 64 KiB (`edge.MaxInputRedactedBytes`) | Redacted payload larger than this is written to `ArtifactStore` and the inline event carries a 4 KiB summary + pointer. |
| High-severity small payload | any size + high-severity finding | Same `ArtifactStore.Put`; the inline event keeps its full redacted map plus `_artifact_pointer` / `_artifact_sha256` / `_high_severity_finding: true`. |
| Inline summary cap | 4 KiB (`inlineRedactedSummaryBytes`) | Truncated head of redacted payload retained inline alongside the pointer for triage. |

`ArtifactPointer` carries `{artifact_type, session_id, execution_id, event_id, tenant_id, retention_class, sha256, uri}`. Request payloads use
`edge.ArtifactTypeMCPRequest`; response payloads use
`edge.ArtifactTypeMCPResponse`. An `ArtifactStore` outage at any of
these triggers is a fail-closed condition — the event is not emitted.

The byte-length check is enforced on `json.Marshal` bytes, not rune
count. Multibyte UTF-8 cannot smuggle past the cap by reporting fewer
runes than bytes.

## Tenant isolation contract

* `CallMetadata.Tenant` is non-empty or the request fails closed (`missing_mcp_metadata`).
* `descriptor.TargetResource.OwnerTenant` is checked by the upstream
  `tenant_gate` against `auth.Tenant`; cross-tenant writes deny before
  `actiongate.mcp` runs.
* Approval-store records are keyed on `(tenant, agent_id, tool, action_hash)`;
  the action hash includes the tenant string so cross-tenant approval
  hijack is impossible by construction.
* `ArtifactStore.Put` receives `TenantID` from the calling event; backend
  implementations partition by tenant prefix.

## See also

- `docs/edge/observability.md` — Prometheus metric labels for the
  decision counters (`cordum_edge_action_decisions_total` carries
  `layer=mcp` when the pre/post path emits).
- `docs/edge/api.md` — `/api/v1/edge/action/evaluate` payload shape that
  external pack-side hooks consume; the in-process MCP path uses the
  same descriptor.
- `core/policy/actiongates/mcp_gate.go`, `mutation_gate.go` — production
  gate implementations (worker-1a1b's task-bf56d8c8 / AgentShield).
- `core/mcp/policy_evaluate.go` — the bridge wrapper this page describes.

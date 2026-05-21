# MCP approval hold and resume (EDGE-103)

When an MCP `tools/call` triggers a `REQUIRE_HUMAN` decision from the
action-gate pipeline, Cordum's MCP server returns a JSON-RPC approval-
required error rather than dispatching the call. The client retries the
same call with an `_approval_ref` field once a human resolver has
approved the request; Cordum atomically consumes the approval against
the stored args + policy snapshot and forwards the call upstream.
For destructive action-gate paths, consume is followed by ProvenanceGate:
the tenant audit chain must include an approved
`EventEdgeApprovalResolved` / `edge.approval_resolved` event with the same
tenant, `approval_ref`, and `action_hash`. A requested-only approval event is
not sufficient provenance.

Production action gates are blockers/approval gates: they allow, deny,
throttle, or require human approval. They do not emit enforceable
generic runtime constraints. Typed `ALLOW_WITH_CONSTRAINTS` runtime
limits come from policy bundles and SafetyKernel `PolicyConstraints`,
not from this approval-hold path.

EDGE-103 layers the **resume** path on top of EDGE-102's policy gate.
The initial **hold** path is unchanged from EDGE-102.

## Protocol

```
Client                            MCP Server                           Edge Approval Store
  │  tools/call (args, no _ref)        │                                      │
  │ ────────────────────────────────▶  │                                      │
  │                                    │  EvaluateToolCall →                  │
  │                                    │  DecisionRequireApproval             │
  │                                    │  ConsumeActionGateDecision           │
  │                                    │  ────────────────────────────────▶  │
  │                                    │                                    EnqueueApproval
  │                                    │  ◀────────────────────────────────  │
  │                                    │  approval_ref                        │
  │  JSON-RPC -32099 approval_required │                                      │
  │ ◀────────────────────────────────  │                                      │
  │  error.data {approval_ref,         │                                      │
  │              expires_at,           │                                      │
  │              args_hash,            │                                      │
  │              retry_hint}           │                                      │
  │                                    │                                      │
  │       (human resolver approves via /api/v1/edge/approvals/{ref}/approve)   │
  │                                    │                                      │
  │  tools/call (args, _approval_ref)  │                                      │
  │ ────────────────────────────────▶  │                                      │
  │                                    │  ProcessApprovalClaim →              │
  │                                    │  ClaimApproval (atomic CAS)          │
  │                                    │  ────────────────────────────────▶  │
  │                                    │  ◀────────────────────────────────  │
  │                                    │  consumed=true / approval            │
  │                                    │                                      │
  │                                    │  upstream tool dispatch              │
  │                                    │  (args have _approval_ref stripped)  │
  │                                    │                                      │
  │  tools/call result                 │                                      │
  │ ◀────────────────────────────────  │                                      │
```

## JSON-RPC error catalogue

| Code | Name | When | error.data fields |
| --- | --- | --- | --- |
| `-32099` | `approval_required` | Initial hold. Caller did not present `_approval_ref`; action-gate returned `REQUIRE_HUMAN`. | `approval_ref` (new), `expires_at`, `args_hash`, `retry_hint` |
| `-32096` | `approval_lifecycle_error` | Caller presented `_approval_ref` but the store refused the claim (fail-closed lifecycle). | `kind`, `approval_ref`, `reason` |
| `-32098` | `not_authorized` | Scope filter rejected the call (pre-policy). | `tool`, `sub_reason`, `agent_id` |
| `-32097` | `gateway_misconfigured` | Server-side wiring defect (e.g. missing `CallMetadata` in context). | (none) |

### `error.data.kind` enum on `-32096`

The discriminator is snake_case lowercase, matching the Go
`edge.ApprovalConflictKind` enum so the wire format and the server-side
typed error never drift.

| `kind` | Caused by |
| --- | --- |
| `not_found` | `approval_ref` doesn't resolve to any record (typo / cross-tenant probe / long-stale replay). |
| `rejected` | Human resolver rejected the approval (Status=Rejected). |
| `expired` | Past the stored `ExpiresAt`. |
| `consumed` | Already consumed once; replay attempt. |
| `args_mismatch` | Canonical args hash differs from what was approved (callsite mutation post-approval). |
| `policy_mismatch` | Active policy snapshot differs from what was bound at hold-creation. |
| `tuple_mismatch` | `session_id`/`execution_id`/`event_id` mismatch (cross-session replay). |
| `self_approval` | Caller is the requester OR the approver — defense against own-approval loops. |
| `cross_tenant` | `approval_ref` belongs to a different tenant. |
| `unknown` | Unclassified `ErrApprovalConflict`. |

## Args canonicalization

The `_approval_ref` field is stripped from the arguments BEFORE the
input hash is computed and BEFORE the args reach the upstream tool
handler. The hash is computed over the canonical SHA-256 of the
remaining payload (see `mcp.CanonicalActionHash`) so a Windows-style
backslash path and the equivalent POSIX path produce one approval key.
Any caller-side mutation of the args between hold and resume changes
the hash and the store fires `kind=args_mismatch`.

## Policy-snapshot binding

The bundle version active at hold-creation is bound on the approval
record. If the policy bundle is rotated mid-hold, the resume call hits
`kind=policy_mismatch` by design — the approval was granted under
policy-v1 and cannot grant a call evaluated under policy-v2. Operators
who genuinely need a stale-policy approval to apply must re-resolve
under the new policy.

## TTL bounds

`WithApprovalMaxTTL(d)` on the Edge store caps every approval's
`ExpiresAt` to `(createdAt + d)` regardless of the caller-supplied
value. The cap is enforced at **creation**; consume-time cannot extend.
Redis `EXPIREAT` runs alongside the in-record `ExpiresAt` field for
defense in depth.

Default: unset (no clip). Recommended production value: 30 minutes
(matches plan step-3 default; tune per workload).

## Consume-once

`ClaimApproval` is atomic via Redis WATCH/MULTI/EXEC; a successful
consume flips `Status` from `Approved` to `Consumed` and sets
`ConsumedAt`. A second consume on the same `approval_ref` hits
`kind=consumed`. Concurrent consumes on the same ref are serialised
(see `TestRedisStoreApprovalConcurrentClaimConsumesOnce`).

This CAS transition is the single-use enforcement point. The mutation
gate may include `single_use=true` as an audit breadcrumb on an allow,
but no action-gate `Constraints` map enforces single-use.

For destructive tools, the upstream dispatch still depends on resolved approval
audit evidence. Missing, malformed, wrong-ref/hash, rejected/expired, or
unverifiable provenance fails closed; raw arguments and tool outputs are not
persisted as approval evidence.

## Self-approval prohibition

Defense in depth at three layers:

1. **`MutationGate`** (EDGE-102's `core/policy/actiongates/mutation_gate.go`) — refuses when `descriptor.RequesterAgentID == approval.ApproverAgentID`.
2. **`ClaimApproval` CAS** (EDGE-103 step-3) — refuses when
   `ApprovalClaimRequest.CallerAgentID` matches `approval.Requester` OR
   `approval.ResolverID`. Returns `*ApprovalConflictError{Kind: self_approval}`.
3. **MCP entry-path `ProcessApprovalClaim`** — propagates the typed kind
   to the JSON-RPC layer; the caller sees `-32096` `kind=self_approval`.

Any one of these three closes the hole; all three together survive a
refactor that bypasses one layer.

## Cross-tenant prohibition

The Edge approval store keys every approval by `tenant_id`.
`ClaimApproval` returns `ErrNotFound` (mapped to `kind=not_found`) on a
cross-tenant `approval_ref` rather than a more specific
`cross_tenant` kind — leaking the existence of an approval to the
wrong tenant is reconnaissance value an attacker shouldn't get. The
`cross_tenant` kind on the enum is reserved for future store paths
where the existence-leak risk is mitigated (e.g. audit-only mode).

## Audit-event schemas

The EDGE-103 entry-path emits one structured event per outcome via the
edge `EventEmitter` (the same emitter EDGE-102 wired into the bridge
pre/post/failed). Event kinds and the field schema:

| EventKind | Emitted when |
| --- | --- |
| `mcp.approval.required` | Initial hold (mirrors EDGE-102 `mcp.tool.pre` with decision=require_approval). |
| `mcp.approval.consumed` | `ClaimApproval` accepted; about to dispatch upstream. |
| `mcp.approval.rejected` | `kind=rejected`. |
| `mcp.approval.expired` | `kind=expired`. |
| `mcp.approval.args_mismatch` | `kind=args_mismatch`. |
| `mcp.approval.policy_drift` | `kind=policy_mismatch`. |
| `mcp.approval.self_approval_attempt` | `kind=self_approval`. |
| `mcp.approval.cross_tenant_attempt` | `kind=not_found` arising from a cross-tenant ref. |

**Field schema (NEVER args / prompts / tokens):**

```json
{
  "event_id": "<uuid>",
  "session_id": "<from CallMetadata>",
  "execution_id": "<from CallMetadata>",
  "tenant_id": "<from CallMetadata>",
  "principal_id": "<from CallMetadata>",
  "approval_ref": "<edge_appr_...>",
  "requester": "<principal_id of original tool caller>",
  "decision": "deny|allow|require_approval",
  "kind": "<from ApprovalConflictKind, omitted on .required/.consumed>",
  "reason": "<short non-leaking text>",
  "gate_id": "actiongate.mutation"
}
```

A redacted-fixture test (`TestNoRawSecretInApprovalEvent`) ensures no
secret-shape substring (`sk-...`, `ghp_...`, `AKIA...`, PEM blocks)
ever lands in the event — defense in depth alongside EDGE-102's
defense-in-depth completeness check.

## See also

- `docs/edge/mcp-tool-policy.md` — EDGE-102 policy gate (the layer that
  produces the `REQUIRE_HUMAN` decision EDGE-103 holds).
- `core/edge/approval_store_redis.go::ClaimApproval` — the atomic CAS.
- `core/edge/approval_store.go::ApprovalConflictError` — typed error
  carrying the `Kind` that maps to `error.data.kind`.
- `core/mcp/approval_hold.go::ProcessApprovalClaim` — the entry-path
  helper that handleToolsCall composes before invokeTool dispatch.

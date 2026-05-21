# MCP approval-hold and resume

The MCP approval-hold lifecycle bridges a `REQUIRE_HUMAN` policy
decision with the Edge approval store so a held tool call can resume
once an operator approves. The mint path runs inside the gateway's
action-gate evaluation; the consume path runs inside the MCP server
when the client retries the call with an `_approval_ref` argument.
For destructive action-gate approvals, a successful consume is not the whole
provenance contract: the tenant audit chain must also contain an approved
`EventEdgeApprovalResolved` / `edge.approval_resolved` event for the same
tenant, `approval_ref`, and `action_hash`. Requested-only approval rows remain
pending-review context and fail closed as provenance.

Production action gates are blockers/approval gates only: they allow,
deny, throttle, or require human approval. They do not emit enforceable
generic runtime constraints. Typed `ALLOW_WITH_CONSTRAINTS` limits are
owned by policy bundles and SafetyKernel `PolicyConstraints`, while
approval single-use is enforced by the Edge approval store claim path.

This document is the contract for the EDGE-103 lifecycle. For the
broader threat model and human approval flow see
[`per-tool-approval.md`](per-tool-approval.md). For preapproved
scopes see [`scope-preapproval.md`](scope-preapproval.md).

## Lifecycle

```
                              tools/call (no _approval_ref)
                                       │
                              EvaluateToolCall ── pre event (require_approval)
                                       │
                              dec.requiresHuman() == true
                                       │
                              ApprovalHandoff.ConsumeActionGateDecision
                                       │
                              mintEdgeApprovalForActionGate
                                       │
                              edge.Store.EnqueueApproval
                                       │
                              return approval_ref to client
                                       │
                              ...operator approves out of band...
                                       │
                              tools/call (with _approval_ref)
                                       │
                              ProcessApprovalClaim → ClaimApproval
                                       │
                              ProvenanceGate verifies resolved
                              approval audit evidence
                                       │
                              invokeTool with `_approval_ref`-stripped args
```

The mint and consume paths each compute a `(ActionHash, InputHash)`
pair through `BuildMCPApprovalBinding`. Any drift between the two
sides surfaces as `ApprovalConflictKindArgsMismatch` on the consume —
EDGE-103 reopen #1 was a divergence in the input-hash derivation; PR
#276 Sub-E finding #15 consolidated all three call sites (policy-gate
mint, gateway Edge mint, server consume) onto the single helper so
future drift is impossible.

The resolved audit event must match the same `ActionHash` and tenant/ref used
by the approval store. Wrong tenant/ref/hash, malformed event JSON,
rejected/expired outcomes, missing Redis/audit verifier dependencies, or
compromised hash/HMAC/linkage are treated as fail-closed evidence gaps rather
than as recoverable client retries.

## `_approval_ref` retry argument

The client receives an `approval_ref` string in the first call's
result envelope. To resume, the client re-sends the original
`tools/call` payload with one extra JSON field:

```json
{
  "name": "fs.write",
  "arguments": {
    "path": "/var/data/x.db",
    "contents": "hi",
    "_approval_ref": "edge_appr_xyz"
  }
}
```

The `_`-prefix marks the field as server-reserved. `ProcessApprovalClaim`
strips it before the canonical input-hash computation **and** before
the upstream handler ever sees the payload. The downstream tool runs
against the exact bytes the original gated call carried.

## Canonical action and input hash binding

`BuildMCPApprovalBinding(tenant, server, params, _ string) (actionHash, inputHash string)`
is the **single** source of truth for both hashes. It:

1. Strips `_approval_ref` from `params.Arguments` so a retry that
   echoes the ref hashes identically to the original gated call.
2. Canonicalises the stripped JSON (key order normalised, whitespace
   stripped) so `{"a":1,"b":2}` and `{"b":2,"a":1}` produce the same
   `InputHash`.
3. Extracts the first matching path-like arg
   (`path`/`file_path`/`target_path`/`filepath`) into the action key.
4. Returns `(ActionTupleHash(tenant, server, tool, target_path), InputHash)`.

`ActionHash` binds the tuple `(tenant, server, tool, target_path)`.
`InputHash` binds the canonical arg bytes. An honest args mutation
(e.g. different `contents`) flips `InputHash` but keeps `ActionHash`
stable.

## Policy snapshot requirement

`WithApprovalHold` refuses to enable the path when `PolicySnapshot` is
nil. `ProcessApprovalClaim` mirrors the check and returns
`errMissingPolicySnapshot` if a direct caller passes `PolicySnapshot=nil`.

Both guards exist because the resulting `ApprovalClaimRequest` carries
a `PolicySnapshot` field the Edge store validates non-empty. Without
the guard a misconfigured deploy would either fail at the store
(per-request churn) or, on a permissive fake, let a hold register as
"approved" against an empty policy.

Production callers wire the snapshot to the gateway's
`PolicySnapshotProvider`, which derives a deterministic string from
the active policy bundle digest.

## Blank-linkage fail-closed behaviour

Both `EvaluateToolCall` and `ProcessApprovalClaim` refuse the call
when ANY of the four identity fields on `CallMetadata` is blank:

| Field | Source | Used for |
|-------|--------|----------|
| `Tenant` | gateway tenant middleware | tenant isolation, action-hash tuple |
| `SessionID` | edge session middleware | audit join, approval-store key |
| `ExecutionID` | edge execution middleware | audit join, approval-store key |
| `AgentID` | edge identity middleware | mint event id, audit attribution |

Each function emits zero events on the fail-closed path and returns
`errMissingMCPMetadata`. The JSON-RPC layer maps this to `-32097`
(`gateway misconfigured`) with `error.data="missing_mcp_metadata"`.
Operators page on the dedicated code instead of chasing a generic
`-32603 internal error`.

## Identity-keying rule

The approval-hold lifecycle keys on **caller identity**, not on
MCP server identity. Specifically:

* `ToolCallApprovalContext.AgentID` MUST be sourced from
  `CallMetadata.AgentID` (the agent making the call).
* `ToolCallApprovalContext.Tenant` MUST be sourced from
  `CallMetadata.Tenant`.
* `ToolCallApprovalContext.Server` is the MCP server identifier
  (e.g. `cordum.builtin`) and is part of the `ActionHash` tuple
  for tool disambiguation, **not** part of the caller key.
* `edge.ApprovalClaimRequest.CallerAgentID` MUST be the same caller
  principal that received the initial `approval_pending` response.

Keying on server identity would let two distinct agents share an
approval slot — an authenticated agent A could consume a pending
approval minted for agent B simply by issuing the same tool with the
same `_approval_ref`. The self-approval guard at the store layer is
the last line of defense; the identity-keying contract here is the
first.

`InvokeToolWithPolicy.requiresHuman` enforces this on the mint side by
sourcing `AgentID` from `CallMetadataFromContext(ctx).AgentID`, not
from `PreEvent.PrincipalID` and not from the `server` parameter.

`ApprovalHandoff` is required for any `REQUIRE_HUMAN` decision; a nil
handoff returns a clear configuration error rather than silently
bypassing the approval step. Tests:

* `TestInvokeToolWithPolicy_ApprovalHandoffUsesCaller` — locks the
  caller-identity routing contract.
* `TestInvokeToolWithPolicy_RequireApproval_RequiresHandoff` — locks
  the "no handoff means no upstream" half of the contract.

## Conflict kinds and JSON-RPC mapping

When `ClaimApproval` refuses the claim it returns an
`*edge.ApprovalConflictError` whose `Kind` rides on the JSON-RPC
`-32096 approval lifecycle error` envelope:

```json
{
  "code": -32096,
  "message": "approval lifecycle error",
  "data": {
    "kind": "args_mismatch",
    "approval_ref": "edge_appr_xyz",
    "reason": "input_hash differs from minted approval"
  }
}
```

`Kind` is the snake_case enum from `edge.ApprovalConflictKind`:

| Kind | Meaning |
|------|---------|
| `not_found` | The `_approval_ref` does not resolve to any record. |
| `rejected` | The approval was explicitly declined. |
| `expired` | The approval TTL elapsed before consume. |
| `consumed` | The approval already fired; retry with a fresh ref. |
| `args_mismatch` | The canonical input hash differs from the minted approval. |
| `policy_mismatch` | The active policy snapshot differs from the minted approval's snapshot. |
| `self_approval` | The approving identity matches the requesting agent. |
| `cross_tenant` | The approval belongs to a different tenant. |
| `tuple_mismatch` | The action-hash tuple differs. |
| `approval_store_unavailable` | The Edge store errored at mint time (transient outage). |

Clients branch retry logic on `Kind`. A `consumed` requires the user
to re-trigger the gated action; an `args_mismatch` typically reflects
a client-side arg mutation between mint and resume and should NOT
auto-retry.

## Test pins

| Concern | Test |
|---------|------|
| Hash drift across mint/consume retry boundary | `TestApprovalHold_HashRoundtrip_Identical` |
| `_approval_ref` stripped before canonical hash | `TestBuildMCPApprovalBinding_StripsApprovalRef` |
| Blank linkage refused (gate side) | `TestPolicyEvaluate_BlankLinkage_FailsClosed` |
| Blank linkage refused (consume side) | `TestProcessApprovalClaim_BlankLinkage_FailsClosed` |
| `WithApprovalHold` refuses nil snapshot | `TestApprovalHold_RequiresSnapshot` |
| `ProcessApprovalClaim` refuses nil snapshot | `TestProcessApprovalClaim_NilSnapshot_FailsClosed` |
| Caller identity used (not server identity) | `TestInvokeToolWithPolicy_ApprovalHandoffUsesCaller` |
| `ApprovalHandoff` required on REQUIRE_HUMAN | `TestInvokeToolWithPolicy_RequireApproval_RequiresHandoff` |
| Typed conflict kinds round-trip | `TestProcessApprovalClaim_TypedConflictKind` |

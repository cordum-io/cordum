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
                              ProcessApprovalClaim → GetApproval → ClaimApproval
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
| `AgentID` | edge identity middleware | caller identity, audit attribution |

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
bypassing the approval step. A handoff must return a non-empty approval
reference or an error: an empty or blank reference fails the call closed
with `-32097`, the tool does not run, and the outcome is never cached, so
an identical retry reaches the handoff again (task-06784e6d). Tests:

* `TestInvokeToolWithPolicy_ApprovalHandoffUsesCaller` — locks the
  caller-identity routing contract.
* `TestInvokeToolWithPolicy_RequireApproval_RequiresHandoff` — locks
  the "no handoff means no upstream" half of the contract.

## Policy gate on: event binding and single authority (task-63400922)

With `mcp.policy_gate_enabled=true` every `tools/call` runs through the
policy evaluator, which emits an `mcp.tool.pre` event before the tool
runs. That event carries `input_hash` (the canonical argument hash, with
`_approval_ref` stripped) and `policy_snapshot`.

- **Mint.** Both mint sites bind the Edge approval to that event's
  `event_id`: the action-gate handoff (`mintEdgeApprovalForActionGate`)
  and the registry gate's dual-write for `RequiresApproval` tools
  (`dualWriteEdgeApproval`). `EnqueueApproval` refuses an `event_id`
  that names no stored event, and an `input_hash` or `policy_snapshot`
  that differs from the event's. A gate-on mint without the pre-event
  fails closed before `EnqueueApproval`.
- **Claim.** `ProcessApprovalClaim` reads the approval with a
  tenant-scoped `GetApproval` and claims with its stored `event_id`, as
  Edge evaluate retries do: the resumed call cannot know the held call's
  per-call event. Session, execution, hashes, snapshot and caller still
  come from the resumed call, so the store's tuple, args and policy checks
  apply unchanged.
- **Single authority.** Only the Edge approval can resume a held call:
  1. approve it with `POST /api/v1/edge/approvals/{approval_ref}/approve`;
  2. resume once by re-sending the call with `_approval_ref`. The claim is
     consumed atomically, and the registry gate lets that one call, with
     the same tool and arguments, through once. A replay gets `-32096`
     `not_found` ("approval not claimable").

  The MCP approval record the registry gate dual-writes (`approval_id` in
  the `-32099` data) is correlation-only. Approving it authorizes nothing:
  it never lets a retry without `_approval_ref` run the tool. Within the
  semantic dedupe TTL (10 minutes), a byte-identical retry by the same
  principal in the same session replays the resumed call's cached result
  without running the tool again.
- **Outages.** An Edge store error on the mint, the approval lookup or the
  claim returns `-32096` with `kind=approval_store_unavailable`, and the
  tool does not run.
- **Policy change during the call.** Both mint sites compare the snapshot
  stamped on the call's `mcp.tool.pre` event with the current snapshot.
  If a policy publish changed it between those reads, nothing is minted:
  the call returns `-32096` `kind=policy_snapshot_changed` with a fixed
  reason. Retry the call without `_approval_ref`: it is evaluated under
  the current policy, and the error is never cached. A blank snapshot on
  either side is not a confirmed change; a blank current snapshot still
  fails with `approval_store_unavailable` (see **Snapshot**). A publish
  after the comparison, including after minting, remains possible: an
  approval with an older snapshot is refused at resume with
  `policy_mismatch`, as before.
- **Snapshot.** Gate-on mints need a non-empty policy snapshot. The Edge
  store refuses an approval without one, so a deployment with no policy
  snapshot fails every gate-on mint with `-32096`.
- **Audit join.** The held call's `mcp.tool_invocation` records
  `approval_status=required` with the MCP record id as `approval_id`. The
  resumed call's records `approval_status=consumed` with the Edge
  `approval_ref` as `approval_id`. The `-32099` data carries both ids, and
  the Edge approval's `event_id` names the held call's `mcp.tool.pre`
  event.
- **Action-gate holds.** A call the action-gate pipeline holds resumes the
  same way; see the next section.

## Action-gate REQUIRE_HUMAN resume (task-cd9df90a)

With `mcp.policy_gate_enabled=true`, the action-gate pipeline can answer a
`tools/call` with `REQUIRE_HUMAN`. The call returns the text result
`approval pending: <approval_ref>` with an Edge ref, and the tool does
not run.

- **Where the hold comes from.** The production pipeline is a fixed gate
  list, and only the MCPGate answers `REQUIRE_HUMAN` for an `mcp_call`:
  its fail-closed taint hold (sub-reason
  `taint_lookup_unavailable_failclosed`). It holds a call when all three
  apply:
  1. `CORDUM_MCP_TAINT_FAILCLOSED_DESTRUCTIVE` is set to a true value. The
     gateway reads it once at boot; unset means off.
  2. The tool is destructive (default globs `*delete*`, `*remove*`,
     `*archive*`).
  3. The session-taint lookup failed.

  MutationGate never fires for `mcp_call`: it acts only on mutation
  actions, and TenantGate only on tenant-query and mutation actions. MCP
  descriptors never carry the `requires_provenance` tag that
  ProvenanceGate needs.
- **Resume.** Approve the ref with
  `POST /api/v1/edge/approvals/{approval_ref}/approve`, as a principal
  other than the caller. Then re-send the identical call with
  `_approval_ref`. The server claims the approval atomically and
  evaluates the call again. When the action gate again answers
  `REQUIRE_HUMAN`, the consumed approval authorizes this one call only if
  its tool, action hash and input hash equal the call's.
  - The tool runs once. Its `mcp.tool.post` or `mcp.tool.failed` event
    carries `approval_ref`, and its `mcp.tool_invocation` records
    `approval_status=consumed`.
  - This check does not spend the registry gate's single use, so a tool
    that is also `RequiresApproval` passes both gates on one approval.
  - The SecOps invariant DENY floor still applies: an invariant that
    denies the tool refuses the resume, and nothing runs.
  - DENY still wins: if the new evaluation denies the call, the deny
    result is returned and nothing runs.
  - The claim has used the approval up before evaluation, so a DENY, a
    refusal or an upstream error burns it. Another attempt needs a new
    hold and approval.
- **Dedupe.**
  - A call that carries `_approval_ref` is never answered from the
    semantic dedupe cache. It evicts the cached result for the call and
    never waits on another call's slot.
  - A successful resume is cached: within the dedupe TTL (10 minutes), an
    identical retry without `_approval_ref`, by the same principal in the
    same session, replays the executed result and does not run the tool
    again.
  - Until then, the pending result stays cached for identical retries
    without `_approval_ref`: a retry gets the same `approval pending:`
    text from the in-process dedupe store, or a `result_sha256`
    placeholder of it over Redis, which never stores result bodies
    (`TestRefless_PendingStillCached`).
- **Errors.**
  - `-32096` `args_mismatch`: the call's arguments differ from the
    approved call. The claim refuses it; a consumed approval that does not
    bind the call is refused the same way, without `approval_ref` in the
    data.
  - `-32096` `not_found` ("approval not claimable"): a replay of a
    consumed ref.
  - `-32097` (gateway misconfigured): the call still carries
    `_approval_ref` when the action gate holds it, because no approval
    hold is wired to consume the ref; or the approval handoff cannot
    authorize a consumed approval. Nothing is minted in either case. A
    held call without `_approval_ref` also gets `-32097` when the
    action-gate handoff has no Edge approval mint or no Edge session
    metadata, or returns no approval reference; the tool does not run and
    the error is never cached (task-06784e6d).
- **Preapproval and the legacy MCP approval store (task-06784e6d).**
  Neither resolves an action-gate hold. Scope preapproval waives only the
  registry gate's per-tool approval, so a preapproved identity's held call
  gets the same `approval pending: <approval_ref>` and resumes the same
  way. The action-gate handoff never claims or enqueues a legacy MCP
  approval record.

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

Claim conflict kinds are the snake_case enum from `edge.ApprovalConflictKind`.
`approval_store_unavailable` and `policy_snapshot_changed` originate in the
MCP layer, not `ClaimApproval`:

| Kind | Meaning |
|------|---------|
| `not_found` | The `_approval_ref` does not resolve to any record. |
| `rejected` | The approval was explicitly declined. |
| `expired` | The approval TTL elapsed before consume. |
| `consumed` | The approval already fired; retry with a fresh ref. |
| `args_mismatch` | The canonical input hash differs from the minted approval. |
| `policy_mismatch` | The active policy snapshot differs from the minted approval's snapshot. |
| `policy_snapshot_changed` | The policy changed while the call was being evaluated, before any approval was minted. Nothing was minted; retry the call. |
| `self_approval` | The approving identity matches the requesting agent. |
| `cross_tenant` | The approval belongs to a different tenant. |
| `tuple_mismatch` | The action-hash tuple differs. |
| `approval_store_unavailable` | An approval store errored: the Edge store at mint, approval lookup or claim time, or the legacy MCP approval store during claim or enqueue (transient outage; the client may retry); or a policy-gate mint could not bind to its pre-event. `reason` is always the fixed text `approval store unavailable`; the gateway logs the underlying error and never returns it to the client. |

Clients branch retry logic on `Kind`. A `consumed` requires the user
to re-trigger the gated action; an `args_mismatch` typically reflects
a client-side arg mutation between mint and resume and should NOT
auto-retry. A `policy_snapshot_changed` is safe to retry at once.

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
| Gate-on mint binds the policy pre-event | `TestApprovalHold_MintBindsRealPreEvent` |
| Gate-on mint without a pre-event fails closed | `TestApprovalHold_DualWriteFailsClosedWithoutPreEvent`, `TestGate_EdgeMintOutageFailsClosed` |
| Claim binds the stored `event_id` | `TestApprovalHold_ClaimUsesStoredEventID` |
| Gate-on HTTP resume executes exactly once | `TestPolicyGateEdgeResume_HTTPExecutesExactlyOnce` |
| Legacy MCP approval cannot resume under the gate | `TestPolicyGateEdgeResume_HTTPLegacyApprovalCannotResume` |
| Gate-on mint and claim outages return `-32096` | `TestPolicyGateEdgeResume_HTTPMintOutageFailsClosed`, `TestPolicyGateEdgeResume_HTTPClaimOutageFailsClosed` |
| Mid-call policy change mints nothing; retry binds the current snapshot | `TestEdgeMintPolicySnapshotDrift_IsRetryable`, `TestJSONRPC_PolicySnapshotChangedWiresTo32096` |
| Blank snapshots preserve existing mint outcomes | `TestEdgeMintPolicySnapshotDrift_BlankSnapshotsKeepTodaysOutcome` |
| Action-gate `REQUIRE_HUMAN` resume executes once over HTTP | `TestActionGateResume` |
| A resume is never answered from the cached pending result | `TestResume_NeverServedFromCachedPending`, `TestResume_DoesNotWaitOnInFlightRefless` |
| A consumed approval that does not bind the call fails closed | `TestResume_BindingMismatchFailsClosed` |
| The invariant floor applies on resume | `TestResume_InvariantFloorDenies` |
| An unconsumed `_approval_ref` or a non-authorizing handoff fails closed | `TestResume_UnconsumedApprovalRefArgFailsClosed`, `TestResume_HandoffWithoutAuthorizerFailsClosed` |
| Scope preapproval is not consulted on an action-gate hold | `TestActionGateHold_PreapprovalNotConsulted` |
| A preapproved identity's action-gate hold resumes once over HTTP | `TestActionGatePreapproval` |
| The action-gate handoff without an Edge mint fails closed with `-32097`, claiming and enqueueing no legacy record | `TestActionGateHandoff_EdgeMintUnwiredFailsClosed` |
| An empty or blank handoff reference fails closed with `-32097` and is never cached | `TestInvokeToolWithPolicy_EmptyHandoffRefFailsClosed` |

# Cordum Edge Identity Contract

EDGE-039 and EDGE-042 were both bugs about implicit identity contracts
between components that weren't written down. This document is the
canonical reference for what each Edge ID means and who owns it. New
maintainers and reviewers consult this before touching audit, evidence,
approval, or cache code.

## 1. Decision IDs — the dual-witness model

Every policy decision produces TWO independent records that share the
same `(session_id, execution_id)` parent but have DISTINCT identifier
namespaces. Collapsing them into one record (the EDGE-039 bug) loses
the dual-witness signal — auditors can no longer cross-check that the
gateway's authoritative decision matches the agentd's local
observation.

| Field         | Owner                               | Format                | Stored in              |
|---------------|-------------------------------------|-----------------------|------------------------|
| `event_id`    | Gateway (`handlers_edge_evaluate.go appendEdgeEvaluateOutcome`) | UUID                  | `edge:events:<id>`     |
| evidence id   | Agentd (`core/edge/agentd/local_server.go:266` `EventID: "agentd-" + randomHex(16)`) | `agentd-<32hex>`      | Separate event with `parent_event_id` pointing at gateway record |

### Audit invariant

For every Claude action that crossed the gateway evaluate path:

- **EXACTLY ONE** gateway record exists (UUID-shaped event_id).
- **AT MOST ONE** agentd evidence record exists (agentd-prefixed id).
- The agentd record references the gateway's event_id via
  `parent_event_id`; the gateway record does NOT reference the agentd
  id (the gateway is authoritative; agentd is the local observer).

### Why two records, not one

The gateway records the authoritative decision (which rule fired, what
the policy snapshot was, and any typed policy-bundle/SafetyKernel
constraints emitted). The agentd records
the local-context evidence (cache hit/miss, fail mode, agentd timing,
hook env). Collapsing them as EDGE-039 attempted (sharing the gateway
event_id) hit the idempotency window on the second write because both
components were trying to write under the same key. The fix preserved
the distinct ID namespaces.

### Synthetic example

```
Gateway event_id:  evt-3f8a4c2d-1e9b-4c5e-9d3a-8a7b6c5d4e3f
Agentd evidence:   agentd-7a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d
                          └─ parent_event_id: evt-3f8a4c2d-1e9b-4c5e-9d3a-8a7b6c5d4e3f
```

The agentd id is distinguishable at a glance from a gateway id by the
`agentd-` prefix. Audit consumers can group per-decision by joining on
`parent_event_id == event_id` for any record whose id starts with
`agentd-`.

## 2. action_hash — the action canonicalization key

`action_hash` is the deterministic SHA-256 of the canonical action
shape. It anchors approval reusability and the EDGE-018 safe-allow
cache.

### Owner

`core/controlplane/gateway/handlers_edge_evaluate.go:619`
`edgeEvaluateActionHash(event, policySnapshot)`. The hash payload is a
struct serialized to JSON with these fields (order matters for
deterministic byte-output):

- `tenant_id`
- `session_id`
- `execution_id`
- `principal_id`
- `layer` (e.g. `hook`)
- `kind` (e.g. `hook.pre_tool_use`)
- `tool_name`
- `tool_use_id` (omitempty)
- `action_name` (classifier-emitted)
- `capability` (classifier-emitted)
- `risk_tags` (classifier-emitted, sorted ascending)
- `labels` (classifier-merged)
- `input_hash` (already-hashed input payload)
- `policy_snapshot` (`cfg:<sha>`)

Returns `"sha256:" + hex.EncodeToString(sum[:])`.

### Stability requirement

Any layer recomputing the hash MUST produce the identical value. The
classifier owns the upstream fields (action_name, capability, risk_tags,
labels) — see `core/edge/classifier.go` `ClassifyEvent`. The gateway
seals them with the policy snapshot. agentd does NOT recompute; it
treats the gateway's value as authoritative and stores it on the
evidence record.

### The (tenant, session, execution, action_hash) tuple

`core/edge/approval_store_redis.go:68`
`edgeApprovalTupleIndexKey(TenantID, SessionID, ExecutionID, ActionHash)`
is the SMembers-indexed key for approval reusability per EDGE-042. An
approval enqueued under the tuple is reusable IFF a subsequent
evaluate produces the SAME tuple AND the approval is not yet
terminal-resolved.

### Why action_hash alone is NOT enough for reuse

Cross-session and cross-tenant boundaries. An approval granted to
tenant A's session S1 cannot be reused on tenant A's session S2 —
the user-visible context differs even when the action_hash is
identical (e.g. two parallel Claude sessions both running `Read .env`
with the same policy snapshot). The 4-tuple ensures approvals stay
scoped to their original observation context.

### Why bypass the action_hash index on findReusable

`handlers_edge_evaluate.go:923 findReusableEdgeApprovalForAction`
deliberately bypasses the action_hash index and paginates the
principal index instead. Reason: `ClaimApproval` SRems consumed
approvals from the action_hash index, so a previously-claimed
approval can no longer be looked up via that index — but the
auto-consume retry needs to find it. Principal-index pagination
hits all the principal's approvals regardless of consumed state and
filters by tuple in-memory.

### Synthetic example

```
sha256:0ed4c2b9f8a7e6d5c4b3a2918f7e6d5c4b3a2918f7e6d5c4b3a2918f7e6d5c4b
```

## 3. approval_ref lifecycle

ApprovalRef is the public-facing reference for an Edge approval —
clients see it in the evaluate response, dashboard renders it in the
URL, and resolvers act on it.

During action-gate evaluation, a caller-presented `approval_ref` is the
primary lookup key. The gateway resolves that exact ref inside the
authenticated tenant and only then compares the resolved record's
`action_hash` to the current canonical action hash. A fake or
cross-tenant ref must miss/fail closed even if another approved record
exists for the same `action_hash`.

For destructive mutations, `approval_ref` is also bound to audit-chain
provenance. After the mutation gate validates the backend approval, the
production gateway pipeline verifies the tenant's audit hash-chain slice for
that approval through `server.auditChainer` and `core/audit.VerifyChain`.
The chain must contain a canonical resolved approval audit event
(`EventEdgeApprovalResolved` / `edge.approval_resolved`) whose decision is
`approved` or `approve` and whose tenant, `approval_ref`, and `action_hash`
exactly match the stored approval. The earlier approval-requested event is not
enough; it records that review was needed, not that the destructive retry is
authorized.

Missing Redis/audit-chainer/verifier dependencies return an explicit
`service_unavailable` denial and never fail open. Compromised hash/HMAC/linkage,
malformed event JSON, wrong tenant/ref/hash, non-approved terminal outcomes, or
missing resolved approval-window evidence remain hard fail-closed provenance
denials. If retention trims older history, a partial verifier result is allowed
only when in-window resolved approval evidence is still present. Verification is
bounded by the shared audit verify window and stream-scan caps. HMAC
verification uses `CORDUM_AUDIT_HMAC_KEY` via `Chainer.HMACKeyForVerify`; the
key itself is never logged or surfaced to clients, and raw payloads/transcripts
are not copied into provenance evidence.

### States and transitions

`core/edge/approval.go:5-13` `ApprovalStatus` has these values:

| Status        | Set by                          | Terminal? |
|---------------|----------------------------------|-----------|
| `pending`     | `EnqueueApproval` at evaluate time | No       |
| `approved`    | Human resolver via `/approve`     | Yes       |
| `rejected`    | Human resolver via `/reject`      | Yes       |
| `expired`     | TTL-driven expire watcher          | Yes       |
| `invalidated` | System sweep on tenant-isolation/cleanup | Yes |

### Approved → consumed/claimed (sub-state)

`approved` is a stored terminal state; `consumed` is NOT a stored
state — it is a one-time use marker. EDGE-042's
`findReusableEdgeApprovalForAction` + `ClaimApproval` flow auto-
consumes an `approved` record on the first matching retry; subsequent
retries see no reusable approval and a fresh approval is enqueued
under a NEW approval_ref.

Evaluation does not consume. The single-use transition happens only in
the existing `ClaimApproval` CAS path when execution/retry presents the
bound `approval_ref` with the matching tuple and policy snapshot.

### Fresh-deny (no approval enqueued)

When the policy decision is a hard DENY (not REQUIRE_APPROVAL), the
approval workflow never runs and no `pending` record is created. The
evaluate response carries `permission_decision=deny + exit_code=2 +
rule_id` from the kernel's decision; no approval_ref is emitted.

### permissionDecisionReason substrings

Each lifecycle exit produces a substring that hooks and dashboard
humans rely on:

- `pending`: `"approval required: <reason>; retry after approve"`
- `approved` (auto-consumed): no approval blob — proceed to allow path
- `rejected`: `"approval rejected by <resolver>; <reason>"`
- `expired`: `"approval expired; retry to re-enqueue"`
- `invalidated`: `"approval invalidated; retry to re-enqueue"`

### Synthetic example

```
ApprovalRef: edge-approval-7c3a8b1d-9e2f-4a5b-8c7d-6e5f4a3b2c1d
Status:      approved
Auto-consumed at: 2026-05-04T08:42:13Z by claim from event evt-...
```

## 4. ID propagation chain

The Cordum Edge architecture passes a stable ID chain end-to-end:

```
Hook              Agentd              Gateway              Safety Kernel       Audit
────              ──────              ───────              ─────────────       ─────
session_id        ✓ pass through      ✓ tenant validation  ✓ as label          ✓ stored
execution_id      ✓ generate-or-pass  ✓ tenant validation  ✓ as label          ✓ stored
event_id          (n/a — gateway-     ✓ generate           (n/a)               ✓ as own id
                   issued)
agentd-evidence   ✓ generate          (n/a — agentd-only)  (n/a)               ✓ stored as
                                                                                 separate event
trace_id          ✓ generate-or-pass  ✓ pass through       ✓ as label          ✓ stored
job_id            ✓ pass through      ✓ pass through       ✓ as label          ✓ stored
workflow_run_id   ✓ pass through      ✓ pass through       ✓ as label          ✓ stored
```

### Ownership summary

- **session_id**: hook generates (or inherits from `~/.claude/settings.json`). Tenant-scoped at gateway.
- **execution_id**: agentd issues at execution-create time. Validated by gateway against the parent session's tenant.
- **event_id**: gateway issues at evaluate time. Authoritative.
- **agentd-evidence id**: agentd issues at agentd-side decision-record time. Distinct namespace.
- **trace_id**: hook generates (UUID) or inherits from a parent trace.
- **job_id / workflow_run_id**: populated only when the Edge action is part of a workflow-driven Cordum job. Otherwise empty. The fields exist so audit can correlate Edge sessions to upstream job lifecycles when there's a real production workflow.

### Tenant-scoping invariant

Every ID in the propagation chain is validated against `X-Tenant-ID` at
the gateway boundary. A request claiming session_id from another
tenant is rejected with 403 (per the
`requireEdgePermissionOrRole` middleware + the
`tenant-from-auth-not-from-body` invariant established by EDGE-008.7).

## 5. Cross-references

- EDGE-039 — gateway/agentd ID-collision postmortem (this doc would
  have prevented the collapse).
- EDGE-042 — action_hash auto-consume + reuse-key fix.
- EDGE-018 — agentd safe-allow cache; cache key derives from
  action_hash + policy_snapshot.
- EDGE-052 — global policy authority; the `cfg:<sha>` snapshot
  identifier feeds into the action_hash payload.
- EDGE-008.7 — principal-from-auth precedent (tenant validation at
  gateway boundary applies the same trust-boundary discipline).

## See also

- `docs/edge.md` — Edge architecture overview.
- `docs/edge/cordum-agentd.md` — agentd internals and the evidence-event
  emit path.
- `docs/edge/cordum-hook.md` — hook lifecycle and session/execution
  generation.
- `docs/edge/classifier-trust-boundary.md` — classifier-owned label
  namespaces (EDGE-069).
- `docs/policy/global-authority.md` — Global policy authority + the
  `cfg:<sha>` snapshot id (EDGE-052).

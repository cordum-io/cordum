# Atomic store-and-hash (WATCH/MULTI) — evaluated and rejected

Status: **Rejected — 2026-04-23** (task-090ab6af step 5).
Revisit trigger: a future StaleRequest regression that the shared
canonicaliser + `protojson.UnmarshalOptions{DiscardUnknown: true}`
together cannot close.

## Problem

The approval-repair classifier at
`core/infra/store/job_store.go:ClassifyApprovalRepair` compares a
`SafetyDecisionRecord`'s recorded `RequestHash` against a freshly
recomputed hash of the stored `JobRequest`. Any divergence between the
scheduler's pre-store view of the request and the reconciler's
post-store view of the same request risks tripping
`ApprovalConflictStaleRequest` on a benign approval, which then
auto-DENYs a legitimate single-step workflow.

Historically two narrow paths caused such divergence:

1. **task-3527fdc5 / task-fa783d7a** — the scheduler used to hash the
   in-memory proto (which could carry proto unknown fields from a
   newer SDK) while the reconciler hashed the Redis-read protojson
   (which drops unknowns). Closed by routing both sides through a
   `protojson` round-trip inside the canonicaliser.
2. **task-090ab6af** — several `protojson.Unmarshal` call sites in
   `job_store.go` (GetJobRequest, ApplyApprovalRepair, ResolveApproval)
   lacked `UnmarshalOptions{DiscardUnknown: true}`, so newer SDKs could
   break the read path outright on forward-compat fields. Fixed by
   adding `DiscardUnknown` at every JobRequest and PolicyConstraints
   unmarshal site in the file.

Post-task-090ab6af state: the scheduler, gateway, and store all call
`core/protocol/reqhash.Hash`. The canonical hash is, by construction,
stable across:

- a fresh in-memory proto vs one read from Redis,
- a proto carrying forward-compat unknown fields vs the roundtripped
  form (DiscardUnknown + EmitUnpopulated round-trip inside
  `reqhash.Canonical`),
- approval label churn (`approval_*`, `bus.LabelBusMsgID`) added
  between submit and approve,
- scheduler env-var injection (`config.EffectiveConfigEnvVar`).

The remaining divergence window is bounded to fields the canonicaliser
explicitly strips — i.e., exactly the fields whose mutation MUST NOT
break approval.

## Option A — Atomic store-and-hash via WATCH/MULTI

Wrap every `SetJobRequest` in a Redis WATCH/MULTI block that also
reads the just-stored bytes back, hashes them, and writes that hash
onto the companion `SafetyDecisionRecord` in the same transaction:

```
WATCH job:req:<id>
SET job:req:<id> <marshalled bytes>
GET job:req:<id>
-- (go) compute sha256 of the GET result
HSET safety:decision:<id> request_hash <hash>
EXEC
```

Pros:

- Tautologically correct — the stored hash is computed from the very
  bytes the reconciler will later read, so a re-hash cannot disagree.
- Immune to any future canonicaliser drift (anyone who touches
  `reqhash.Canonical` cannot introduce a divergence because the hash
  is not recomputed against the pre-store view).

Cons:

- Adds a `GET` per `SetJobRequest` (the write path is now read-after-
  write). Measurable perf cost on hot approval paths.
- Complicates the store API: `SetJobRequest` would need to return the
  computed hash, and every caller that persists a
  `SafetyDecisionRecord` would need to consume it. The current clean
  separation ("store persists proto, scheduler persists hash") would
  blur.
- Rolling upgrade: during a mixed-binary deploy an old gateway/
  scheduler would write `SafetyDecisionRecord.RequestHash` computed
  the old way, while a new store would write it computed from the
  stored bytes. Reconciler must handle both shapes for at least one
  release.
- Reconciler semantics shift: today the reconciler defends against
  silent store mutation by recomputing the hash; with an atomic store-
  and-hash, the reconciler effectively trusts whatever is persisted.
  That is a stronger invariant (preserving it requires an ACL or
  signature on the key), not a free lunch.

## Decision — Reject for now

The unification delivered by task-090ab6af closes every failure mode
we currently know about:

- Forward-compat SDK fields: handled by `DiscardUnknown` at all five
  `protojson.Unmarshal` sites in `job_store.go` and by the existing
  `protojson` round-trip inside `reqhash.Canonical`.
- Scheduler-vs-reconciler hash drift: handled by collapsing both
  implementations into one call to `reqhash.Hash`.
- Approval-label and env-var churn: already handled by
  `reqhash.Canonical`'s strip step.

The atomic path buys defence against a failure mode we have no
evidence of (silent mutation of the stored bytes by some third-party
observer), at the cost of a perf hit and a rolling-upgrade protocol.
That trade is not warranted in the current posture.

## Revisit triggers

Re-open this decision if any of the following appears:

- A StaleRequest regression whose root cause is *not* covered by the
  canonicaliser's strip + round-trip (i.e., the pre- and post-store
  views diverge on a field the canonicaliser does not touch).
- A security finding that third-party Redis write access can mutate
  the stored request undetected (the chain-evidence model assumes only
  the gateway writes to the key; the atomic path would narrow the
  trust boundary further).
- A performance audit showing that the current non-atomic write path
  is not actually on the critical path, so the perf cost of the
  atomic variant is a non-issue.

Absent any of those, the simpler, canonicaliser-based fix stays in
place.

# Policy Bundle Store

Redis-backed storage for the unified Policy Studio bundles introduced in
epic-d9a6c0a1. The store sits in `core/policy/` and is consumed by both
job-side (safetykernel) and edge-side evaluators via the
[`BundleStore`](../core/policy/bundle_store.go) interface; the unified
view replaces the per-track bundle tables that lived in v2.

## Purpose

Policy Studio's three-surface IA (`/policies`, `/policies/bundles`,
`/policies/decisions`) needs a single canonical store for:

- Bundle envelopes (id, name, scope binding, metadata).
- Per-bundle version history (immutable BundleVersion records with
  full RuleSnapshot for tamper-evident rollback).
- Per-scope active deployment + ordered deploy/rollback history.

`BundleStore` is the contract; `BundleRedisStore` is the canonical
implementation.

## Redis schema

| Key | Type | Purpose | Cardinality | TTL |
|---|---|---|---|---|
| `policy:bundle:{id}` | STRING (JSON Bundle envelope, no Versions) | Bundle metadata | 1 per bundle | none (immutable + long-lived) |
| `policy:bundle:{id}:versions` | ZSET (member=version, score=DeployedAt unix nanos) | Per-bundle version index | 1 per bundle | none |
| `policy:bundle:{id}:version:{version}` | STRING (JSON BundleVersion incl. RuleSnapshot + AuditHash) | Immutable version blob | 1 per (bundle, version) | none |
| `policy:scope:{kind}:{value}:active` | STRING (`"{bundleID}:{version}"` pointer) | Active deployment for scope | 1 per scope | none |
| `policy:scope:{kind}:{value}:history` | LIST (each element a Deployment JSON; LPUSH on deploy/rollback) | Ordered history (newest first) | bounded to 100 entries via LTRIM in Lua | none |

### Why the prefix split

`policy:bundle:*` covers everything keyed by bundle id; `policy:scope:*`
covers everything keyed by scope. Step-2 inventory confirmed neither
prefix collides with existing core/* stores (workflow uses `cordum:wf:*`
+ `runKey()`, edge uses `edge:session:*`, registry uses
`sys:workers:*`).

## CRUD operations

| Operation | Atomic? | Idempotent? | Typed errors |
|---|---|---|---|
| `CreateBundle` | SETNX (single key) | yes (duplicate returns ErrBundleExists) | ErrBundleExists |
| `GetBundle` | GET (single key) | yes | ErrBundleNotFound |
| `ListBundlesByScope` | SCAN over `policy:bundle:*` + in-process filter | yes | — |
| `CreateBundleVersion` | SETNX + ZADD pipeline | yes (duplicate version-number returns ErrBundleVersionExists) | ErrBundleVersionExists |
| `ListBundleVersions` | ZRANGE + MGET pipeline | yes | — |
| `GetBundleVersion` | GET (single key) | yes | ErrBundleVersionNotFound |
| `DeployVersionToScope` | **single Lua EVAL** (`deployScript`) | yes | ErrBundleVersionNotFound |
| `RollbackDeployment` | **single Lua EVAL** (`rollbackScript`) | yes | ErrNoRollbackTarget |
| `GetActiveDeployment` | GET pointer + LRANGE history (read-only) | yes | ErrNoDeploymentForScope |
| `ListDeploymentHistory` | LRANGE | yes | — |

## Concurrency model

All multi-key mutations execute inside a single Redis Lua script:

- `deployScript` — validates the requested `policy:bundle:{id}:version:{n}`
  exists, then SETs the active pointer, LPUSHes the deploy event into
  history, and LTRIMs to the last 100 entries. Atomic; concurrent
  deploys to the same scope serialize Redis-side (Redis runs Lua
  scripts in-process to completion).

- `rollbackScript` — reads the current active pointer, locates the
  deploy entry that established it (skipping rollback markers), reads
  the `prev_bundle_id` + `prev_version` fields recorded ON that deploy
  event at deploy-time, SETs active to that prior pair, and LPUSHes a
  rollback marker. Chained rollbacks unwind one step per call
  (`v3 → v2 → v1`); the test `TestRollbackChain` pins this. After a
  deploy-after-rollback (`v1 → v2 → rollback → v3 → rollback`) the
  final rollback restores v1 — the active state immediately before
  v3 was deployed — because that pair was captured on the v3 deploy
  event. The test `TestDeployAfterRollback` pins this regression
  closed (was QA reopen #1 on 2026-05-09).

### Why prev-active is recorded on each deploy event

Without per-event prev-active fields, rollback can only inspect raw
history order, which loses information after a deploy-after-rollback:
the second-most-recent deploy event (v2) is no longer the active
state immediately before the current deploy (v1, post-rollback). The
script reads the current active pointer and writes the deploy event
inside the same Lua execution, so concurrent deploys to the same scope
produce a totally-ordered prev-active chain.

The Lua-EVAL choice is load-bearing: per memory `mem-12f1ceeb`,
go-redis's WATCH+TxPipelined sequence corrupts the connection pool when
miniredis returns errors, which broke the workflow store's
`TestHandleJobResult_UpdateRunRetry_Succeeds` during task-a45b8eb1.
Lua scripts have no equivalent failure mode under miniredis.

## Bounded sizes

History lists are LTRIMmed to the most recent 100 entries (constant
`deploymentHistoryCap` in `bundle_store_keys.go`). The cap fires inside
each Lua mutation, so no separate sweeper is needed. 100 is the
DoD-defined upper bound; tune if dashboard's "Deployments" tab needs
deeper history.

## Open questions for v3

1. **`ListBundlesByScope` SCAN cost** — current impl scans every
   `policy:bundle:*` key and filters in-process. At ~1k bundles per
   tenant this is fine; at 100k+ a per-scope ZSET index would amortize
   the cost. Defer until a real performance signal appears.

2. **Cluster-mode key colocation** — the deploy/rollback Lua scripts
   touch two keys (active + history) for a single scope. Both keys
   share the `policy:scope:{kind}:{value}:` prefix; with Redis
   Cluster's hash-tag rule, wrap the scope segment in `{...}` when the
   cluster topology requires colocation. Not needed for single-node
   deployments.

3. **Audit-chain integration** — `Deployment.AuditHash` is currently
   plumbed but unused; Backend 5 will populate it via the existing
   `core/audit` chain.

## Cross-references

- Task: `task-b349524a` (Backend 2 of epic-d9a6c0a1).
- Predecessor: `task-3bf37e32` (Backend 1 — types).
- Consumers: Backend 3 (job evaluator), Backend 4 (edge evaluator),
  Backend 5 (unified evaluator entry-point).
- Spec: `docs/specs/policy-studio-rewrite.md`.

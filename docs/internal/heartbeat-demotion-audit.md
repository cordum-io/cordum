# Heartbeat Demotion Audit

Captured: 2026-04-18
Task: task-2336063d (Heartbeat demotion — telemetry only, not authority)
Epic: epic-cb8e0d62 (Phase-2 Boundary Hardening)

This audit catalogs every place in `core/` that currently treats a
heartbeat (or its derived `lastSeen` timestamp) as **authority** for a
worker-visibility, dispatch, or policy decision. Each entry describes
the current behaviour, the demoted behaviour planned for the
`telemetry` mode of `CORDUM_HEARTBEAT_MODE`, and a risk rating that
captures how disruptive the change is for operators today.

The grep that produced the catalog:

```
grep -rn 'heartbeat\|Heartbeat\|lastSeen' core/controlplane/scheduler core/controlplane/gateway
```

filtered to non-test files and decision-point call sites (visibility
filters, dispatch picks, response shaping). Telemetry-only uses
(metrics, log lines, dashboard last-seen display) are intentionally
excluded — those keep working unchanged.

## Authority sites (heartbeat gates a decision)

### 1. `core/controlplane/scheduler/registry_memory.go` — workers map TTL filter

| Line(s) | Method | Current behaviour | Demoted behaviour | Risk |
|---|---|---|---|---|
| 103 | `WorkersForPool` | `if entry.hb == nil OR now.Sub(entry.lastSeen) > r.ttl { skip }` — workers whose heartbeat hasn't been seen within `defaultWorkerTTL=30s` are excluded from the per-pool worker list used by dispatch. | Skip the lastSeen TTL check; consult `WorkerTrustState.SessionValid` instead. A worker with a valid session token continues to be eligible for dispatch even if its heartbeat publisher hiccupped. | **High** — direct dispatch path. |
| 120 | `Snapshot` | Same TTL gate excludes stale workers from the snapshot returned to the gateway. | Replace with SessionValid filter; expose lastSeen as informational metadata. | **High** — gateway/dashboard rely on `Snapshot` for the worker list returned by `/api/v1/workers`. |
| 135 | `WorkersForLabels` | Same TTL gate inside the label-selector worker filter used by job routing. | Replace with SessionValid filter. | **High** — routing path. |
| 156 | `IsAlive(workerID)` | Returns `time.Since(entry.lastSeen) <= r.ttl`. Callers use this to short-circuit scheduling when a worker has gone quiet. | Return `WorkerTrustState.SessionValid && !RevokedAt`. Heartbeat freshness can become a separate `IsRecentlyHeard()` accessor for telemetry consumers. | **High** — public registry API surface; multiple call sites. |
| 177 | `Cleanup` | TTL-driven garbage collection: a worker entry is dropped when `now.Sub(entry.lastSeen) > r.ttl`. | Drop entries when `SessionExp.Before(now)` AND `now.Sub(entry.lastSeen) > graceTTL` so the registry doesn't grow unbounded but a brief heartbeat outage doesn't flush an actively-trusted worker. The grace TTL stays at the existing 30s default; session exp is the trigger. | **Medium** — affects long-running registry growth; behaviour difference is invisible in normal operation but matters for memory accounting. |
| 196 | `Pools` | TTL gate excludes stale workers from the per-pool aggregation. | Replace with SessionValid filter. | **Medium** — feeds into `/api/v1/pools` worker counts; cosmetic for operators but pool capacity decisions ride on it. |

### 2. `core/controlplane/scheduler/strategy_least_loaded.go` — dispatch picker

The `LeastLoadedStrategy.PickSubject` call (line 40) operates on the
`workers map[string]*pb.Heartbeat` returned by `MemoryRegistry`. That
map is *already* TTL-filtered upstream (see §1 above), so no further
heartbeat-age check is needed inside the strategy. After demotion the
strategy continues to receive the (now session-valid-filtered) map and
no edits are required here.

| Line(s) | Method | Current behaviour | Demoted behaviour | Risk |
|---|---|---|---|---|
| 40-185 | `PickSubject` / `loadScore` / `matchesLabels` / `isOverloaded` | Reads `*pb.Heartbeat` fields (load, capacity, labels) for picking the lowest-loaded worker. Heartbeat is the *carrier* of load info but not the gate. | No change. Load fields stay on the Heartbeat message. | **Low**. |

### 3. `core/controlplane/gateway/handlers_workers.go` — `/api/v1/workers` response

| Line(s) | Function | Current behaviour | Demoted behaviour | Risk |
|---|---|---|---|---|
| 41-48 | `handleListWorkers` | Falls back to the in-memory heartbeat map for liveness when the persistent store is unavailable. | Read `WorkerTrustState` first; fall back to lastSeen only as a "telemetry hint" surfaced under the new `last_heartbeat_at` field. | **High** — public API surface. External consumers reading `online` need a smooth migration. |
| 277 | `workerSummaryToResponse` | Sets `last_heartbeat` = `capturedAt` (a heartbeat-derived timestamp) on the response. | Keep `last_heartbeat_at` as informational metadata; add `session_valid` and `session_exp_ms` as the new authority fields. | **Medium** — additive change, but documentation must steer external consumers off `online` derived from heartbeat age. |

## Telemetry-only sites (kept unchanged)

These paths read heartbeat data for display, metrics, or load
balancing — *not* for visibility/auth/policy decisions. They stay as-is
under telemetry mode:

- `core/controlplane/scheduler/types.go` — `Heartbeat` struct field
  definitions.
- `core/controlplane/scheduler/strategy_least_loaded.go::loadScore` /
  `isOverloaded` — load info carried by Heartbeat is still used for
  picking the least-loaded worker among the eligible set.
- `core/controlplane/gateway/handlers_stream.go` — heartbeat events
  are still streamed to dashboards as `BusPacket{Heartbeat}` for
  real-time UI freshness.
- `core/controlplane/gateway/handlers_workers.go::workerSummaryToResponse`
  — the `last_heartbeat` field continues to be returned (renamed in
  step-4 to `last_heartbeat_at` with explicit `telemetry only` doc).
- `core/controlplane/scheduler/engine.go` — heartbeat propagation +
  recording paths are unchanged; only the *consumption* of staleness
  as authority is removed.
- Tests under `core/controlplane/scheduler/*_test.go` —
  heartbeat-recency scenarios remain to verify the *display* + load
  calculations, even after heartbeat is no longer authoritative.

## Demoted-behaviour summary

The replacement contract is:

```
Authority chain:
  WorkerTrustState (from session token)
    → IsAlive / WorkersForPool / WorkersForLabels / Snapshot / Pools
      → dispatch + /api/v1/workers `online`

Telemetry chain (unchanged consumer side, only labelled in docs):
  Heartbeat → last_heartbeat_at + load fields
    → dashboard "last seen Xs ago" + Prometheus heartbeat-age gauge
    → never gates a decision
```

`WorkerTrustState` (introduced in step-2 of this task) is derived from
the session-token store landed by task-66b8fb92's
`SessionTokenIssuer`. A worker with a fresh session that has not been
revoked is "alive" regardless of whether its heartbeat publisher
managed to push a packet in the last 30 s.

## Rollout

`CORDUM_HEARTBEAT_MODE` (step-5):

- `authority` — legacy: every TTL gate above is enforced. Default for
  backward compatibility on the first release containing this code.
- `warn` — session-token authority is enforced; heartbeat staleness
  is computed in parallel and emits a `heartbeat_disagreement`
  SIEMEvent (step-6) when the two signals would have produced
  different decisions. Becomes the default after one release.
- `telemetry` — session-token authority is enforced; heartbeat
  staleness is not computed anywhere on the decision path.

Operators upgrade by leaving the default (`authority`) until they
have visibility of disagreement events, flip to `warn`, watch for
disagreements, then flip to `telemetry`.

## Risk-aware rollback

Each authority site catalogued above is guarded by the
`heartbeatModeEnforcesSession()` helper from step-5. Reverting to
`authority` mode restores the legacy TTL gate without a code change.
The warn-mode disagreement events provide the empirical evidence
operators need before flipping to `telemetry`.

## Out of scope for this task

- `core/agent/` — Agent.Start receives heartbeats from itself; no
  authority-decision sites live here.
- `core/worker/` — Worker.Run already emits heartbeats; same.
- External consumers (Grafana boards, Datadog) keep working — they
  read the same wire fields. Documentation updates land in the
  step-10 architecture doc.

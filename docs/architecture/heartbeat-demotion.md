# Heartbeat Demotion: From Authority to Telemetry

> Phase-2 boundary hardening. Related work: SDK handshake (task-66b8fb92), topic registry (task-436f67e1), audit chain worker_trust_change events (task-2497391e).

## 1. Motivation

Cordum originally treated the worker heartbeat as a load-bearing **authority signal** for two decisions:

1. **Worker visibility** on `/api/v1/workers` and the dashboard's agent registry.
2. **Dispatch eligibility** in the scheduler — a worker was eligible to receive a job iff `time.Since(lastHeartbeat) ≤ WORKER_TTL` (30s by default).

That signal fails silently for at least five reasons:

| Failure | What the operator sees | What actually happened |
|---|---|---|
| Clock skew between worker pod and scheduler | "worker offline" | worker is fine, NTP drift |
| Lost NATS heartbeat packet | "worker offline" | 1 packet in 30s was dropped |
| Worker process in a long GC pause | "worker offline" | worker resumes in 200ms, but was offline-gated for 30s |
| Slow stop-the-world during K8s autoscaling | "cascade of offline workers" | rollout artefact |
| Operator-initiated worker revocation | *no signal* | revocation silently works only if the worker stops heartbeating |

Every one of those misdiagnosed dispatch outages costs oncall time and erodes trust in the control plane.

The fix: the scheduler now issues a cryptographically-bound **session token** on a successful worker handshake (task-66b8fb92). That token is the authoritative trust signal. Heartbeats demote to pure telemetry — still useful for freshness UI and health dashboards, never again for policy.

## 2. Old vs new authority chain

### Before (authority chain ≡ heartbeat recency)

```
    WorkerHeartbeat(pb)
         │
         ▼
    MemoryRegistry.UpdateHeartbeat   ┐
                                      │
    time.Since(lastSeen) ≤ TTL  ◀────┘  authoritative gate
         │
         ├── scheduler dispatch decision
         └── /api/v1/workers `online` field
```

A single network blip broke both signals in lockstep.

### After (authority ≡ session token, heartbeat ≡ telemetry)

```
    WorkerHandshake(pb) ──────►  SessionTokenIssuer.Issue
                                  │
                                  │  writes session:worker:<id>
                                  │        session:revoked:<tenant>:<jti>
                                  ▼
                         WorkerTrustState  ◀── authoritative gate
                                  │
                                  ├── DispatchGate.EligibleWorkers
                                  ├── DispatchGate.IsWorkerEligible
                                  └── /api/v1/workers `online` field

    WorkerHeartbeat(pb) ──────►  MemoryRegistry.UpdateHeartbeat
                                  │
                                  ▼
                   last_heartbeat_at + heartbeat_age_seconds
                                  │
                                  └── telemetry only: dashboard freshness,
                                      Prometheus age gauge, health UI
```

The scheduler and gateway both read `WorkerTrustState` via `TrustResolver.ResolveTrust(ctx, agentID)` — a pure read path over the Redis keys the session-token issuer writes.

## 3. What heartbeat is still good for

Heartbeats remain a valuable **freshness signal** that operators and humans consume:

- **Dashboard "last heartbeat Xs ago" sub-line** on the Agents page. Lets an operator tell at a glance whether a worker is reporting in, without implying anything about dispatch eligibility.
- **Prometheus `cordum_scheduler_worker_heartbeat_age_seconds` gauge**. Used for SLO panels (p95 heartbeat age) and diagnosing GC pauses or NATS lag.
- **Per-worker metrics carried in the heartbeat body**: `cpu_load`, `gpu_utilization`, `memory_load`, `active_jobs`. The scheduler still uses these to pick a least-loaded worker *from the set of trusted workers*.

What heartbeats are **not** allowed to do, anywhere in the codebase:

- Gate `/api/v1/workers` visibility.
- Gate dispatch eligibility.
- Trigger oncall alerts ("worker offline").

Every call site that previously did one of these is catalogued in the internal heartbeat-demotion audit (Cordum engineering).

## 4. Rollout plan

The demotion is feature-flagged by `CORDUM_HEARTBEAT_MODE`, which drives a three-phase migration:

### Phase A — `authority` (default on first release)

```
CORDUM_HEARTBEAT_MODE=authority
```

- Legacy behaviour. Heartbeat TTL gates dispatch exactly as before.
- Session authority is computed but not consulted — all new code paths are compiled-in but inert.
- Operators can validate the new plumbing (dashboards, metrics, audit events) without any behavioural change.

### Phase B — `warn` (release N+1, default)

```
CORDUM_HEARTBEAT_MODE=warn
```

- Session token becomes the authority for dispatch + visibility.
- The legacy heartbeat-recency signal is still computed on the decision path, purely to compare it with session authority.
- Every disagreement emits a structured ERROR log **and** a `heartbeat_disagreement` SIEMEvent on the audit chain, with `direction` = `session_allows_heartbeat_blocks` | `session_blocks_heartbeat_allows`.
- Operators watch the disagreement rate. The expected trajectory is high initially (workers still on old SDK without handshake), tapering to near zero as the fleet upgrades.

Exit criterion for this phase: the `heartbeat_disagreement` event rate settles near zero and has been near zero for at least one full business week.

### Phase C — `telemetry` (release N+2, target)

```
CORDUM_HEARTBEAT_MODE=telemetry
```

- Session token is the only authority. Heartbeat recency is not consulted on the decision path at all.
- `heartbeat_disagreement` events no longer fire — the mode doesn't compute both signals.
- Heartbeat age is still exposed everywhere (metrics, `/api/v1/workers`, dashboard) as a freshness indicator.

### Mode-transition audit

Every flip of `CORDUM_HEARTBEAT_MODE` should be recorded as a `worker_trust_change` SIEMEvent with `reason=heartbeat_mode_transition`, `worker_id="*"`, and `actor` set to the operator who flipped the flag. Use the `scheduler.EmitModeTransition` helper so every call site produces the canonical shape.

## 5. External-consumer migration (/api/v1/workers)

The wire shape is **additive** — legacy clients that read only the heartbeat-era fields keep type-checking.

New consumers should migrate to the new fields:

| Concern | Old field | New field |
|---|---|---|
| "Is this worker dispatchable?" | `last_heartbeat` age | `online` (bool) |
| "When did I last hear from it?" | `last_heartbeat` | `last_heartbeat_at` (ISO8601) + `heartbeat_age_seconds` (int) |
| "Why is it offline?" | implicit (TTL expiry) | `session_state`: `valid` / `no_session` / `session_expired` / `session_revoked` / `trust_store_unready` |
| "When does its session expire?" | n/a | `session_exp_ms` (unix ms) |
| "Was it explicitly revoked?" | n/a | `session_revoked` (bool, present+true only when revoked) |

`last_heartbeat` is kept as an alias for `last_heartbeat_at` so existing dashboards and SDKs keep parsing. Treat it as a deprecated field; remove it in release N+3 once SDKs have migrated.

Pay attention to the `session_state` values in alerts. Paging should route off `session_state != valid`, never off heartbeat staleness.

## 6. Audit events

The chain of audit events that accompanies the demotion:

| Event | When | Severity |
|---|---|---|
| `worker_handshake` (task-66b8fb92) | Worker handshake accept / reject | Info / Medium |
| `worker_trust_change` — `session_issued` | Session token minted | Info |
| `worker_trust_change` — `session_renewed` | Renewed before expiry | Info |
| `worker_trust_change` — `session_revoked` | Operator or scheduler revocation | High |
| `worker_trust_change` — `session_expired` | Natural expiry (telemetry only) | Medium |
| `worker_trust_change` — `heartbeat_mode_transition` | `CORDUM_HEARTBEAT_MODE` flipped | High |
| `heartbeat_disagreement` | Warn mode; per-worker signal divergence | Medium |

All events are SIEMEvent-shaped and flow through the existing audit chain (task-2497391e), so downstream SIEM rules that join on `(tenant, agent_id, event_type)` see the whole rollout without new plumbing.

## 7. Surfaces affected by this change

| Surface | Change |
|---|---|
| `core/controlplane/scheduler/dispatch.go` | New `DispatchGate` that filters by `WorkerTrustState` in warn+telemetry modes. |
| `core/controlplane/scheduler/registry_memory.go` | Additive `SnapshotAll()` so session authority can admit workers with lapsed heartbeats. |
| `core/controlplane/scheduler/engine.go` | Dispatch loop uses `DispatchGate`; heartbeat ingest records trust + age gauges. |
| `core/controlplane/scheduler/trust_state.go` | `TrustResolver` + `EmitTrustChange` / `EmitModeTransition` helpers. |
| `core/controlplane/scheduler/heartbeat_mode.go` | `CORDUM_HEARTBEAT_MODE` flag parsing + `ClassifyDisagreement`. |
| `core/controlplane/scheduler/metrics.go` | `cordum_scheduler_worker_session_valid` + `cordum_scheduler_worker_heartbeat_age_seconds` gauges. |
| `core/controlplane/gateway/handlers_workers.go` | `/api/v1/workers/{id}` response enriched with session fields. |
| `core/controlplane/gateway/handlers_jobs.go` | `/api/v1/workers` list response enriched. |
| `dashboard/src/api/types.ts` + `transform.ts` | Worker type gains session fields; mapper propagates them. |
| `dashboard/src/components/agents/WorkerSessionBadge.tsx` | Reuses StatusBadge primitive. |
| `dashboard/src/components/agents/WorkerSessionLegend.tsx` | Reuses InfoBanner primitive. |
| `dashboard/src/pages/AgentsPage.tsx` | Agent Registry tab status column driven by session state. |
| `core/audit/exporter.go` | `EventHeartbeatDisagreement` + `EventWorkerTrustChange` constants. |
| `docs/operations/runbook-worker-health.md` | Operator runbook with alert migration examples. |

## 8. Rollback

Every site that enforces session authority checks `mode.EnforcesSession()` — returning `false` for `HeartbeatModeAuthority`. Reverting `CORDUM_HEARTBEAT_MODE=authority` is therefore a **no-code-change rollback**: the scheduler immediately resumes legacy heartbeat-TTL semantics, and the dashboard gracefully falls back to the heartbeat-recency `online` computation when the trust resolver is not consulted.

This is the ergonomic rollback we deliberately kept: if a fleet-wide regression surfaces in `warn` mode, operators flip back to `authority` via one env-var redeploy, not a rebuild.

## 9. Further reading

- Internal heartbeat-demotion audit — call-site catalog (Cordum engineering).
- [`docs/operations/runbook-worker-health.md`](../operations/runbook-worker-health.md) — operator alert migration.
- [`core/controlplane/scheduler/trust_state.go`](../../core/controlplane/scheduler/trust_state.go) — trust resolver source.
- [`core/controlplane/scheduler/dispatch.go`](../../core/controlplane/scheduler/dispatch.go) — dispatch gate source.

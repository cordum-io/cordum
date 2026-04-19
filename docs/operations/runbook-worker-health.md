# Runbook: Worker Health under Heartbeat Demotion

This runbook covers worker-health monitoring after the heartbeat-demotion rollout (phase-2 boundary hardening). It replaces the previous `last_heartbeat`-based alerting recipe; the session token is now the authoritative dispatch signal.

## TL;DR

| Question | Old signal | New signal |
|---|---|---|
| Is this worker eligible to receive jobs? | `last_heartbeat_at` age ≤ TTL | `cordum_scheduler_worker_session_valid == 1` |
| Are workers regressing to offline? | `count(worker_heartbeat_age > 30)` | `count(cordum_scheduler_worker_session_valid == 0)` |
| Is a specific worker stale? | `cordum_scheduler_worker_heartbeat_age_seconds` | same — but *as telemetry only* |

Heartbeat age is still exported — use it to diagnose clock skew, NATS backpressure, or a hung worker process. It is **not** safe to alert on as a policy signal.

## Metrics

Two gauges are exposed on the standard scheduler metrics endpoint:

### `cordum_scheduler_worker_session_valid`

- **Type:** Gauge.
- **Labels:** `worker_id`, `tenant`, `pod`.
- **Value:** `1` when the worker's session token is currently trusted (valid exp, not revoked); `0` otherwise.
- **Cardinality:** one row per `(worker, tenant)` tuple, wrapped in the standard `pod` const label for HA replicas.
- **Use cases:**
  - Dashboard "active workers" panels.
  - Pager alerts on `count by (tenant) (cordum_scheduler_worker_session_valid == 0)`.
  - Session churn investigations (`rate` of flips from `1` to `0`).

### `cordum_scheduler_worker_heartbeat_age_seconds`

- **Type:** Gauge.
- **Labels:** `worker_id`, `pod`.
- **Value:** seconds since the last observed heartbeat packet for the worker.
- **Cardinality:** one row per worker, wrapped in `pod`.
- **Use cases:**
  - Diagnose why a worker is stale (GC pause, NATS lag, clock skew).
  - SLO panels showing heartbeat freshness distribution.
  - **Never** drive paging decisions from this gauge alone — a fresh heartbeat with an invalid session token still represents an untrusted worker.

## Suggested Grafana queries

```promql
# Count of trusted workers per tenant
sum by (tenant) (cordum_scheduler_worker_session_valid == 1)

# Count of untrusted workers (alarm if > 0 for > 5m)
sum by (tenant) (cordum_scheduler_worker_session_valid == 0)

# Heartbeat-age distribution (freshness widget, not alert)
histogram_quantile(0.95,
  sum by (le) (rate(cordum_scheduler_worker_heartbeat_age_seconds_bucket[5m]))
)

# Workers with a valid session but a stale heartbeat (≥ 30s) —
# legitimate under the demotion, but useful for diagnosing agent hangs
cordum_scheduler_worker_session_valid == 1
  and
cordum_scheduler_worker_heartbeat_age_seconds > 30
```

## Alert migration

Replace legacy heartbeat-staleness alerts with their session-authority equivalents. Sample migration:

```yaml
# BEFORE — heartbeat-age as authority (deprecated)
- alert: WorkerOfflineCordum
  expr: cordum_worker_heartbeat_age_seconds > 60
  for: 2m

# AFTER — session-token authority
- alert: WorkerUntrustedCordum
  expr: cordum_scheduler_worker_session_valid == 0
  for: 2m
  labels:
    severity: warning
  annotations:
    summary: "Worker {{ $labels.worker_id }} is untrusted"
    description: |
      Session token is missing, expired, or revoked. Heartbeat age is
      informational — see cordum_scheduler_worker_heartbeat_age_seconds
      for freshness context.
```

## Runbook steps for `WorkerUntrustedCordum`

1. Pull the worker's session-state reason: `GET /api/v1/workers/{id}` and inspect `session_state` + `session_revoked`.
2. If `session_state == "session_revoked"`: this is an operator action; confirm via the audit chain (`event_type == "worker_trust_change"`).
3. If `session_state == "session_expired"`: the worker failed to renew. Check the worker process for `handshake_renew` errors; restart if needed.
4. If `session_state == "no_session"`: the worker never handshook. Confirm the worker is on an SDK version that issues a handshake (SDK handshake gap remediation, task-66b8fb92) and that `CORDUM_SDK_HANDSHAKE` is not set to `off`.
5. If `session_state == "trust_store_unready"`: the gateway-side resolver is not wired. Check `CORDUM_HEARTBEAT_MODE` and the scheduler boot log for "heartbeat mode active" lines.

## Heartbeat-age escalation (NOT a session-authority alert)

If `cordum_scheduler_worker_heartbeat_age_seconds` trends upward while `session_valid == 1`, the worker is trusted but losing heartbeat packets. Investigate:

- NATS subscription health (`sys.heartbeat` subject backpressure).
- Worker-process GC / event-loop stalls.
- Clock skew between the worker and the scheduler (the gauge clamps to zero for future-dated heartbeats to guard against this, but a persistently high age with a valid session almost always means clock drift).

This condition alone is **not** a dispatch outage. Do not page oncall for it — file a ticket for the platform team instead.

## Rollout mode quick reference

| `CORDUM_HEARTBEAT_MODE` | Session authority | Heartbeat recency | Disagreement SIEMEvent |
|---|---|---|---|
| `authority` (legacy) | ignored | gates dispatch | no |
| `warn` (transitional) | gates dispatch | compared as telemetry | yes (`heartbeat_disagreement`) |
| `telemetry` (target) | gates dispatch | informational only | no |

Operators should stay in `warn` for at least one release, review the `heartbeat_disagreement` event rate, and then flip to `telemetry` once the count settles near zero.

## References

- `docs/architecture/heartbeat-demotion.md` — strategic context.
- `docs/internal/heartbeat-demotion-audit.md` — call-site audit used to plan the rewire.
- `core/controlplane/scheduler/trust_state.go` — trust resolver source.
- `core/controlplane/scheduler/metrics.go` — gauge registration.

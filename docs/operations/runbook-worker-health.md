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
4. If `session_state == "no_session"`: the worker never completed the
   authenticated challenge/proof exchange. Confirm the installed Go, Python,
   or Node SDK has a complete worker-trust config, the worker/agent/tenant link
   is enrolled, and both control-plane modes are active.
5. If `session_state == "trust_store_unready"`: stop rollout. Check Redis,
   the Ed25519 private/public key pair and key ID on scheduler/gateway, and the
   scheduler boot log. Do not fall back to a self-reported capability
   handshake.
6. If state is `valid` but a packet is rejected, inspect only the stable
   rejection category and public IDs. Verify token subject, packet sender,
   worker ID, tenant, agent, proof-key ID, and audience are the same binding.
   Never print the token or raw proof while debugging.

## Authenticated-handshake remediation

| Failure | Remediation |
|---|---|
| Scheduler/gateway refuses to boot on mode | Use `off` only with `authority`, or `warn`/`enforce` with `warn`/`telemetry`. Empty, typoed, or contradictory values are fatal by design. |
| P-256 authority missing/mismatched | Provide all four `CORDUM_HANDSHAKE_*` settings. Confirm the private key and public SPKI file are the same P-256 key. |
| Session signing authority missing | Provide a real Ed25519 signing key, key ID, and matching `CORDUM_POLICY_PUBLIC_KEY_<ID>` entry. Check Redis before retrying. |
| `unknown_agent`, wrong tenant, or identity mismatch | Confirm the tenant-scoped agent record exists and the worker credential links that exact agent and worker. |
| Unknown/revoked proof key | Rotate to a new worker P-256 key ID and public key; deploy its private key only on the worker. |
| Audience/scheduler mismatch | Use audience `cordum-scheduler` and the exact pinned scheduler ID/key ID. |
| Replay, expired challenge, or skew | Correct clocks and start a new challenge. Never resend the same authenticate packet. |
| Altered nonce/trace/version/capability/signature | Treat as tampering or incompatible bytes; isolate the NATS path and restart the exchange. |
| Expired/revoked/superseded session | Stop new work admission and re-authenticate. Review `worker_trust_change` audit events before clearing an operator revocation. |
| Legacy handshake waits forever | Expected. There is no responder or mint path on old handshake/renew subjects; upgrade to the protobuf challenge/authenticate flow. |

Keep diagnostic logs secret-safe: tenant/worker/agent IDs, bounded trace/request
IDs, public key ID/fingerprint, mode, and rejection category are sufficient.
Session tokens, private keys, raw signatures, nonces, authorization headers,
and complete packets must not enter logs, tickets, or SIEM payloads.

## Heartbeat-age escalation (NOT a session-authority alert)

If `cordum_scheduler_worker_heartbeat_age_seconds` trends upward while `session_valid == 1`, the worker is trusted but losing heartbeat packets. Investigate:

- NATS subscription health (`sys.heartbeat` subject backpressure).
- Worker-process GC / event-loop stalls.
- Clock skew between the worker and the scheduler (the gauge clamps to zero for future-dated heartbeats to guard against this, but a persistently high age with a valid session almost always means clock drift).

This condition alone is **not** a dispatch outage. Do not page oncall for it — file a ticket for the platform team instead.

## Recommended rollout quick reference

| Phase | `CORDUM_SDK_HANDSHAKE` | `CORDUM_HEARTBEAT_MODE` | Dispatch authority | Heartbeat use |
|---|---|---|---|---|
| Compatibility | `off` | `authority` | Legacy heartbeat TTL | Gates dispatch |
| Transition | `warn` | `warn` | Bound worker session | Compared; emits `heartbeat_disagreement` |
| Target | `enforce` | `telemetry` | Bound worker session | Informational only |

These are rollout recommendations, not the complete accepted matrix. Boot
validation accepts `off` only with `authority`, and either `warn` or `enforce`
with either `warn` or `telemetry` heartbeat mode.

In transition, a tokenless heartbeat or generic capability handshake may be
retained for diagnosis, but it must never update trusted liveness, readiness,
capability authority, or the dispatch snapshot. If a tokenless advertisement
appears to make a worker eligible, treat that as a security regression.

Flip both variables together on scheduler and gateway. Before `warn`, enroll
worker proof keys, pin the scheduler P-256 public key on workers, deploy the
Ed25519 session signing/trust authority to scheduler and gateway, and keep
`WORKER_ATTESTATION=off`. Before `enforce`, deploy the same Ed25519 authority to
workflow engine so internal cancel broadcasts remain authenticated. Stay in
the transition phase until disagreement and missing-token rates are understood.
Rollback both variables to `off` + `authority`; do not leave a mixed pair.

## References

- `docs/architecture/heartbeat-demotion.md` — strategic context.
- `docs/sdk/handshake.md` — enrollment, keys, protobuf flow, rotation, and NATS ACLs.
- Internal heartbeat-demotion call-site audit used to plan the rewire (Cordum engineering).
- `core/controlplane/scheduler/trust_state.go` — trust resolver source.
- `core/controlplane/scheduler/metrics.go` — gauge registration.

## See also

- [Cordum Edge runbook](../edge/runbook.md) — operator runbook for the
  Edge surface (sessions, agentd, approvals, artifact pointers, evidence
  export). Edge is parallel to the worker/job pipeline this runbook
  documents; failure modes and triage steps are distinct.

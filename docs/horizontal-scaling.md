# Horizontal Scaling Guide

Cordum supports running 2–6 replicas of every service for high availability and increased throughput. All coordination happens via Redis distributed locks and NATS messaging — no additional infrastructure is required beyond what a standard Cordum deployment already uses.

## Prerequisites

| Component | Minimum for HA | Notes |
|-----------|---------------|-------|
| Redis | 1 node (dev), 6-node cluster (prod) | Single source of truth for all state |
| NATS | 1 node (dev), 3-node cluster (prod) | JetStream enabled for durable subjects |
| Docker Compose / K8s | Required | HA overlay or K8s Deployments with `replicas > 1` |

Redis is the **single source of truth** for jobs, workflows, contexts, results, configuration, DLQ entries, schemas, and locks. Every service connects to the same Redis instance or cluster.

## Per-Service Scaling Guide

| Service | Min | Max | Coordination Mechanism | Notes |
|---------|-----|-----|----------------------|-------|
| api-gateway | 1 | 6 | Redis rate limiter, NATS queue groups | WebSocket clients need session affinity (cookie or IP hash) |
| scheduler | 1 | 3 | Redis job locks, leader election for reconciler/replayer | Only one replica runs the reconciler and pending replayer at a time |
| safety-kernel | 1 | 4 | gRPC load balancing, per-replica decision cache | Decision cache is local; versioned invalidation prevents staleness |
| workflow-engine | 1 | 4 | Redis per-run locks, leader election for reconciler/delay-poller | Only one replica processes each workflow run |
| context-engine | 1 | 4 | Stateless gRPC service backed by Redis | Scales horizontally with no coordination needed |
| cordum-mcp | 1 | 2 | Set `MCP_TRANSPORT=http` for multi-replica | Stdio mode is single-process only |
| cordumctl | N/A | N/A | CLI tool, not a long-running service | — |

## Configuration Reference

### Environment Variables for Horizontal Scaling

| Variable | Default | Service | Purpose |
|----------|---------|---------|---------|
| `REDIS_POOL_SIZE` | `20` | All | Max Redis connections per replica |
| `REDIS_MIN_IDLE_CONNS` | `5` | All | Warm connections kept in the pool |
| `REDIS_RATE_LIMIT` | `true` | api-gateway | Enable Redis-backed distributed rate limiting |
| `MCP_TRANSPORT` | `stdio` | cordum-mcp | Transport mode: `stdio` or `http` |
| `MCP_HTTP_ADDR` | `127.0.0.1:8090` | cordum-mcp | Listen address when `MCP_TRANSPORT=http`; a non-loopback address also needs `CORDUM_MCP_HTTP_AUTH_TOKEN` |
| `AUDIT_TRANSPORT` | (local buffer) | api-gateway | Set to `nats` for durable cross-replica audit export |
| `CORDUM_INSTANCE_ID` | `os.Hostname()` | All | Pod/instance label for Prometheus metrics |
| `SAFETY_DECISION_CACHE_TTL` | `0` (disabled) | safety-kernel | Per-replica decision cache TTL (e.g. `30s`) |
| `SAFETY_DECISION_CACHE_MAX_SIZE` | `10000` | safety-kernel | Max cached decisions per replica |

### Redis Connection Pool Sizing

Each replica maintains its own connection pool. For a 3-replica deployment with `REDIS_POOL_SIZE=20`, the total connections to Redis is up to 60. Sizing guidance:

- **api-gateway**: High connection usage (rate limiter, job store, config, DLQ). Recommend `20–40`.
- **scheduler**: Moderate usage (job locks, reconciler, snapshot writer). Recommend `15–25`.
- **safety-kernel**: Low usage (snapshot history, circuit breaker counters). Recommend `10–15`.
- **workflow-engine**: Moderate usage (run locks, delay timers, workflow store). Recommend `15–25`.

Set `REDIS_MIN_IDLE_CONNS` to ~25% of `REDIS_POOL_SIZE` to reduce connection churn under bursty load.

## Lock Coordination

All distributed locks use the existing `TryAcquireLock`/`ReleaseLock` pattern in `core/infra/store/job_store.go`. Locks are Redis keys with TTLs — if a replica crashes, the lock expires and another replica takes over.

### Redis Lock Keys

| Key Pattern | TTL | Service | Purpose |
|-------------|-----|---------|---------|
| `cordum:scheduler:job:<jobID>` | 30s | scheduler | Per-job processing lock (prevents duplicate dispatch) |
| `cordum:reconciler:default` | 2× poll interval | scheduler | Leader election for reconciler |
| `cordum:replayer:pending` | 2× poll interval | scheduler | Leader election for pending replayer |
| `cordum:wf:run:lock:<runID>` | 30s | workflow-engine, gateway | Per-workflow-run exclusive processing |
| `cordum:workflow-engine:reconciler:default` | 2× scan interval | workflow-engine | Leader election for workflow reconciler |
| `cordum:wf:delay:poller` | 2× poll interval | workflow-engine | Leader election for delay timer poller |
| `cordum:dlq:cleanup` | cleanup interval | api-gateway | Leader election for DLQ eviction |
| `cordum:rl:<key>:<unix_second>` | 2s | api-gateway | Sliding-window rate limit counter |
| `cordum:cache:marketplace` | configurable | api-gateway | Marketplace pack listing cache |
| `cordum:auth:jwks:<issuerHash>` | 1h | api-gateway | OIDC JWKS cross-replica cache |
| `cordum:cb:safety:failures` | configurable | scheduler | Input safety circuit breaker state |
| `cordum:cb:safety:output:failures` | configurable | scheduler | Output safety circuit breaker state |
| `cordum:safety:snapshots` | — | safety-kernel | Last 10 policy snapshot hashes (sorted set) |
| `cordum:bus:processed:<stream>:<seq>` | short | bus layer | JetStream message deduplication |
| `cordum:bus:inflight:<stream>:<seq>` | short | bus layer | In-flight message tracking |

### Lock TTL Rules

- Lock TTLs must be `pollInterval × 2` or use explicit renewal — never hold indefinitely.
- Job locks (30s) are renewed by a background goroutine at `TTL/3` cadence while the job is being processed.
- If a replica crashes mid-processing, the lock expires after the TTL and another replica picks up the work via the reconciler.

## NATS Subject Matrix

### Queue Group Subjects (Load-Balanced)

Only **one** replica receives each message. Used for work distribution.

| Subject | Queue Group | Consumer | JetStream |
|---------|-------------|----------|-----------|
| `sys.job.submit` | `cordum-scheduler` | Scheduler | Yes |
| `sys.job.result` | `cordum-scheduler` | Scheduler | Yes |
| `sys.job.cancel` | `cordum-scheduler` | Scheduler | Yes |
| `sys.job.result` | `cordum-workflow-engine` | Workflow engine | Yes |
| `sys.job.dlq` | `cordum-gateway` | Gateway (DLQ write dedup) | Ephemeral |
| `sys.audit.export` | `audit-exporters` | Audit consumer | Yes |
| `job.<topic>` | per-topic | Workers (SDK) | Yes |
| `worker.<id>.jobs` | per-worker | Workers (SDK) | Yes |

### Broadcast Subjects (Fan-Out)

**Every** replica receives every message. Used for state synchronization.

| Subject | Subscribers | JetStream |
|---------|------------|-----------|
| `sys.heartbeat` | All scheduler + all gateway replicas | No |
| `sys.handshake` | All scheduler replicas | No |
| `sys.config.changed` | All scheduler replicas | No |
| `sys.alert` | All gateway replicas | No |
| `sys.job.progress` | All gateway replicas | No |
| `sys.workflow.event` | All gateway replicas | No |

**Key insight**: Heartbeats are broadcast so every replica independently tracks worker liveness. Job submissions are queue-grouped so only one scheduler processes each job.

### JetStream Durable Consumer Naming

When JetStream is enabled (`NATS_USE_JETSTREAM=true`), queue group subscriptions use shared durable consumer names: `dur_<queue>__<subject>`. All replicas in the same queue group share a single JetStream consumer, ensuring each message is delivered to exactly one replica.

Broadcast subscriptions on durable streams use ephemeral consumers — each replica gets its own consumer, ensuring all replicas receive every message.

### JetStream Streams

| Stream | Subjects | Purpose |
|--------|----------|---------|
| `CORDUM_SYS` | `sys.>` | System events (submit, result, cancel, DLQ, audit) |
| `CORDUM_JOBS` | `job.>`, `worker.*.jobs` | Job dispatch to worker pools |

## Monitoring

### Per-Replica Metrics

Set `CORDUM_INSTANCE_ID` to the pod name so Prometheus metrics include a `pod` label:

```yaml
env:
  - name: CORDUM_INSTANCE_ID
    valueFrom:
      fieldRef:
        fieldPath: metadata.name
```

This enables per-replica dashboarding in Grafana — filter by `pod` label to see individual replica performance.

### Key Metrics to Watch

- `cordum_jobs_total` — per-replica job throughput
- `cordum_rate_limit_hits_total` — rate limit enforcement across replicas
- Redis connection pool usage (via `redis_exporter`)
- NATS consumer lag (via NATS monitoring endpoints)

## PodDisruptionBudgets

To prevent quorum loss during rolling updates or node maintenance:

| StatefulSet | `minAvailable` | Rationale |
|------------|---------------|-----------|
| NATS | 2 (of 3) | NATS requires majority for Raft leader election |
| Redis | 4 (of 6) | Redis Cluster needs majority of primaries for writes |

Application services (gateway, scheduler, etc.) use `maxUnavailable: 1` PDBs. PDB manifests are in `deploy/k8s/base.yaml`.

## Graceful Shutdown

All services use a **15-second shutdown timeout**. On `SIGINT`/`SIGTERM`:

1. Stop accepting new requests (HTTP `Shutdown()`, gRPC `GracefulStop()`)
2. Drain in-flight work (finish current jobs, flush buffers)
3. Release distributed locks (or let TTL expire)
4. Close Redis/NATS connections

**K8s configuration**: Set `terminationGracePeriodSeconds: 30` in pod spec (default). The 15s service timeout fits within the 30s K8s window, leaving headroom for SIGTERM delivery delay and final cleanup.

### Per-Service Shutdown Sequence

| Service | Sequence |
|---------|----------|
| api-gateway | Stop bus taps → HTTP drain → gRPC drain → close stores |
| scheduler | Metrics drain → engine stop → cancel context (stops reconciler/replayer) |
| safety-kernel | gRPC drain → cancel policy watch → wait for goroutines |
| context-engine | gRPC drain → metrics drain |
| workflow-engine | Cancel context → HTTP drain (reconciler/poller stop via context) |

## NATS Route Limitation

The production NATS configuration hardcodes routes for 3 nodes (`cordum-nats-{0,1,2}`). To scale NATS beyond 3 nodes:

1. Update the `routes` list in the NATS StatefulSet configuration
2. Add the new node addresses to all existing nodes
3. Rolling-restart the NATS cluster

This is a known limitation — NATS does not auto-discover new peers in a static route configuration.

## Troubleshooting

The `redis-cli` examples below connect from the repo root to the Compose stack's Redis, which is TLS-only, needs `REDIS_PASSWORD` and publishes port 6379. For another deployment, point `-h`, `--cacert` and `-a` at your Redis.

### Duplicate Job Dispatch

**Symptoms**: Same job appears multiple times in terminal state, or job processed by multiple schedulers simultaneously.

**Diagnosis**:
```bash
# Check job lock in Redis
redis-cli --tls --cacert ./certs/ca/ca.crt -a "$REDIS_PASSWORD" GET "cordum:scheduler:job:<jobID>"

# Check reconciler lock
redis-cli --tls --cacert ./certs/ca/ca.crt -a "$REDIS_PASSWORD" GET "cordum:reconciler:default"
```

**Resolution**: Verify all scheduler replicas are using the same Redis instance. Check that job lock TTL (30s) is not too short for your job processing time.

### Rate Limit Bypass

**Symptoms**: Combined request rate exceeds the configured limit across replicas.

**Diagnosis**:
```bash
# Check if Redis rate limiting is enabled
# Look for REDIS_RATE_LIMIT env var in gateway config

# Check rate limit counters
redis-cli --tls --cacert ./certs/ca/ca.crt -a "$REDIS_PASSWORD" KEYS "cordum:rl:*"
```

**Resolution**: Ensure `REDIS_RATE_LIMIT` is not set to `false`/`0`/`no`. If Redis is unreachable, each replica falls back to per-replica in-memory rate limiting (effectively multiplying the limit by replica count).

### Stale Worker List

**Symptoms**: Different gateway replicas show different worker sets.

**Diagnosis**:
```bash
# Check snapshot writer lock
redis-cli --tls --cacert ./certs/ca/ca.crt -a "$REDIS_PASSWORD" GET "cordum:scheduler:snapshot:writer"

# Check snapshot data
redis-cli --tls --cacert ./certs/ca/ca.crt -a "$REDIS_PASSWORD" GET "sys:workers:snapshot"
```

**Resolution**: The scheduler snapshot writer runs every 5s under a distributed lock. If the lock holder crashes, the lock expires after 30s and another replica takes over. Wait up to 35s for convergence.

### Config Drift Between Replicas

**Symptoms**: Replicas have different system configuration after a config update.

**Resolution**: Check `sys.config.changed` NATS subscription. All scheduler replicas receive config change notifications instantly. Fallback poll interval is 30s if NATS is unavailable.

### DLQ Cleanup Stalled

**Symptoms**: DLQ entries accumulate beyond the configured retention.

**Diagnosis**:
```bash
redis-cli --tls --cacert ./certs/ca/ca.crt -a "$REDIS_PASSWORD" GET "cordum:dlq:cleanup"
```

**Resolution**: If the lock is held by a crashed replica, wait for TTL expiry. The next live replica will acquire the lock and resume cleanup.

### Circuit Breaker Stuck Open

**Symptoms**: All safety checks fail immediately with circuit breaker open error.

**Diagnosis**:
```bash
redis-cli --tls --cacert ./certs/ca/ca.crt -a "$REDIS_PASSWORD" GET "cordum:cb:safety:failures"
redis-cli --tls --cacert ./certs/ca/ca.crt -a "$REDIS_PASSWORD" GET "cordum:cb:safety:output:failures"
```

**Resolution**: Circuit breaker state is shared across replicas via Redis. If the safety kernel recovers, the circuit breaker will close after the configured recovery window. Delete the Redis key to force-reset (use with caution).

### Safety Cache Staleness

**Symptoms**: Policy changes not reflected in safety decisions.

**Resolution**: The decision cache TTL (`SAFETY_DECISION_CACHE_TTL`) controls the maximum staleness window. Each policy update increments the version, which invalidates all cached decisions. During rolling deployments, replicas may briefly serve stale decisions until their cache expires.

## Docker Compose HA Testing

For local HA testing, use the `docker-compose.ha.yaml` overlay:

```bash
# Start 2-replica topology
docker compose -f docker-compose.yml -f docker-compose.ha.yaml up -d --build

# Gateway-1: localhost:8081 (HTTP API)
# Gateway-2: localhost:8083 (HTTP API)

# Verify both gateways are healthy
curl --cacert ./certs/ca/ca.crt -H "X-API-Key: $CORDUM_API_KEY" -H "X-Tenant-ID: default" \
  https://localhost:8081/api/v1/status
curl --cacert ./certs/ca/ca.crt -H "X-API-Key: $CORDUM_API_KEY" -H "X-Tenant-ID: default" \
  https://localhost:8083/api/v1/status

# Run the HA production gate
./tools/scripts/production_gate.sh --gate 19

# Tear down
docker compose -f docker-compose.yml -f docker-compose.ha.yaml down
```

The production gate script (Gate 19) automatically validates no-duplicate dispatch, distributed rate limiting, worker snapshot consistency, and scheduler failover.

## HA Validation Suite

Gate 19 in `tools/scripts/production_gate.sh` runs 5 scenarios against a 2-replica docker-compose topology:

1. **Two-replica deploy + health check** — both gateways and both schedulers are running and healthy
2. **No duplicate dispatch** — 40 jobs submitted across 2 gateways, each reaches terminal state exactly once
3. **Distributed rate limit** — burst of concurrent requests split across gateways, 429s observed from global limit
4. **Worker snapshot consistency** — both gateways return identical sorted worker sets
5. **Scheduler failover** — stop scheduler-2, submit jobs via gateway, verify all complete via scheduler-1, restart and verify no duplicates

Gate 19 is ADVISORY by default and promoted to BLOCKING in `--strict` mode.

## NATS Delivery Semantics

Cordum uses two distinct delivery patterns over NATS, each chosen to match the reliability requirements of the subject:

### Queue Group Subjects (Exactly-Once Delivery)

Queue group subscriptions ensure each message is delivered to **exactly one** replica within the group. When JetStream is enabled, these use shared durable consumer names (`dur_<queue>__<subject>`), providing persistence and redelivery on failure. Used for all work-distribution subjects: `sys.job.submit`, `sys.job.result`, `sys.job.cancel`, `sys.audit.export`, `job.<topic>`, and `worker.<id>.jobs`.

### Broadcast Subjects (Fire-and-Forget)

Broadcast subscriptions deliver each message to **every** replica. These use core NATS (not JetStream) and have no persistence — if a replica is disconnected during a broadcast, it misses the message. This is safe because every broadcast subject has a built-in self-healing mechanism:

| Subject | Self-Healing Mechanism |
|---------|----------------------|
| `sys.heartbeat` | Workers re-heartbeat every 5-10s; missed heartbeat self-corrects on next cycle |
| `sys.handshake` | Workers re-register on their next heartbeat |
| `sys.config.changed` | 30s poll fallback in `config_overlay.go` catches missed notifications |
| `sys.alert` | Informational only; no state dependency |
| `sys.job.progress` | Dashboard re-fetches on reconnect; stale progress is harmless |
| `sys.workflow.event` | Dashboard re-fetches on reconnect; stale events are harmless |

### Rolling Restart Implications

During rolling restarts, broadcast subjects may miss messages while a replica is shutting down and its replacement is starting. This is a deliberate trade-off — the self-healing mechanisms above ensure convergence within seconds. If a new JetStream broadcast subject is added in the future, its ephemeral consumer behavior must be evaluated: ephemeral consumers are deleted on disconnect, so messages published between disconnect and reconnect would be lost.

## Redis Cluster Limitations

When running Redis in cluster mode, Lua scripts and MULTI transactions that touch keys in different hash slots fail with `CROSSSLOT` errors, and `SCAN` reaches only one node. Among the scripts and transactions that touch multiple slots:

| Script | File | Keys | Risk | Status |
|--------|------|------|------|--------|
| `updateRunScript` | `core/workflow/store_redis.go` | 4 KEYS + dynamic index keys | High (every step completion) | Refactored — index updates moved to Go pipeline |
| `idempotencyScopedScript` | `core/infra/store/job_store.go` | 2 KEYS + dynamic `job:meta:` | Medium (job submit) | Documented; use hash tags or pipeline split for cluster |
| `createUserLua` | `core/controlplane/gateway/auth/userstore_redis.go` | 2 KEYS + dynamic `user:id:`, `user:tenant:` | Low (admin operation) | Documented; use hash tags or pipeline split for cluster |
| `chainAppendScript` | `core/audit/chain.go` | `audit:chain:head:<tenant>` + `audit:chain:<tenant>` | High (every audit event) | Cluster path: single-key append on the stream, see below |
| `retentionTrimScript` | `core/audit/retention.go` | `audit:chain:<tenant>` + `audit:chain:anchor:<tenant>` | Medium (retention sweep) | Cluster path: anchor moved in single-key steps, see below |
| Chain verify read (MULTI) | `core/audit/chain_verify.go` | stream + anchor | High (verify, compliance export, ProvenanceGate) | Cluster path: anchor read before and after the stream, see below |
| Legal hold create (MULTI) | `core/audit/legal_hold.go` | `audit:legal_hold:<id>` + `audit:legal_holds:<tenant>` | Low (admin operation) | Two single-key writes, index first |

### Remediation for Redis Cluster

**`updateRunScript` (refactored):** The hot-path Lua script has been split: the atomic GET+SET of the run document stays in Lua (single key), while ZADD/ZREM index updates are performed in a Go pipeline afterward. Index operations are idempotent (ZADD is upsert, ZREM is no-op if missing), so eventual consistency is safe.

**`idempotencyScopedScript` and `createUserLua`:** These remain as multi-key Lua scripts. For Redis Cluster deployments, use hash tags (e.g., `{tenant}:user:...`) to colocate related keys in the same slot, or refactor to pipeline-based approaches similar to `updateRunScript`.

### Audit chain and legal holds on Redis Cluster

The audit chain keeps its single-node key names on a cluster, so a tenant's stream (`audit:chain:<tenant>`), head (`audit:chain:head:<tenant>`) and retention anchor (`audit:chain:anchor:<tenant>`) sit in different slots. Cordum picks the cluster code path from the client type: `REDIS_CLUSTER_ADDRESSES` with more than one seed builds a cluster client. There is no flag. On a single node nothing changes.

- **The head is a hint.** Each event links to the stream's last entry and commits with a single-key compare-and-append on the stream. The head key is written afterwards and only ever moves forward. A crash in between leaves it behind the stream, which is harmless. The head is used only as a floor for the next seq: if it is ahead of the stream, entries were deleted from the tail, and the next event is numbered past the head so the deletion stays visible as `missing` seqs.
- **The trim takes three single-key steps.** (1) The sweeper writes a *pending* anchor: the newest entry to delete plus the anchor it replaces, signed together. (2) It checks the legal hold again, then trims the stream. (3) It confirms, rewriting the anchor without the one it replaced. Until step 3, verify links the stream to the replaced anchor.
- **What verify sees after an interruption:**
  - *Crash, or a new legal hold, after step 1:* the events are still there and verify end to end from the previous anchor. Deleting any of them, including exactly the range the pending anchor names, is reported as `missing`. The next sweep without a hold finishes the trim.
  - *Crash after step 2:* the trimmed events read as `missing` (`compromised`) until the next sweep, outside a legal hold, confirms the trim. An unconfirmed trim looks the same as someone else deleting those events.
- **Verify reads the anchor before and after the stream.** It accepts a verdict that holds against either read, and re-reads (about 150 ms in all) while a sweep moves the anchor or is about to confirm a trim. If the anchor keeps moving it returns an error instead of a verdict.
- **Legal holds** write the tenant index and the hold record as separate commands, index first, so an interrupted create reads as no hold everywhere.
- **Cluster-wide scans** run on every master: retention tenant discovery, `GET /api/v1/audit/legal-holds` without a tenant, and the Edge session sweeper.

These guarantees assume `CORDUM_AUDIT_HMAC_KEY` is set: without it, anchors are unsigned and can be forged on a cluster as on a single node. With it, the cluster sweeper skips a tenant whose anchor does not verify instead of building on it, because confirming a forged pending anchor would sign the deletion it names. Verify reports that anchor as `hmac_mismatch`.

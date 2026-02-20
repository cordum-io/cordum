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
| `MCP_HTTP_ADDR` | `:8090` | cordum-mcp | Listen address when `MCP_TRANSPORT=http` |
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

### Duplicate Job Dispatch

**Symptoms**: Same job appears multiple times in terminal state, or job processed by multiple schedulers simultaneously.

**Diagnosis**:
```bash
# Check job lock in Redis
redis-cli GET "cordum:scheduler:job:<jobID>"

# Check reconciler lock
redis-cli GET "cordum:reconciler:default"
```

**Resolution**: Verify all scheduler replicas are using the same Redis instance. Check that job lock TTL (30s) is not too short for your job processing time.

### Rate Limit Bypass

**Symptoms**: Combined request rate exceeds the configured limit across replicas.

**Diagnosis**:
```bash
# Check if Redis rate limiting is enabled
# Look for REDIS_RATE_LIMIT env var in gateway config

# Check rate limit counters
redis-cli KEYS "cordum:rl:*"
```

**Resolution**: Ensure `REDIS_RATE_LIMIT` is not set to `false`/`0`/`no`. If Redis is unreachable, each replica falls back to per-replica in-memory rate limiting (effectively multiplying the limit by replica count).

### Stale Worker List

**Symptoms**: Different gateway replicas show different worker sets.

**Diagnosis**:
```bash
# Check snapshot writer lock
redis-cli GET "cordum:scheduler:snapshot:writer"

# Check snapshot data
redis-cli GET "sys:workers:snapshot"
```

**Resolution**: The scheduler snapshot writer runs every 5s under a distributed lock. If the lock holder crashes, the lock expires after 30s and another replica takes over. Wait up to 35s for convergence.

### Config Drift Between Replicas

**Symptoms**: Replicas have different system configuration after a config update.

**Resolution**: Check `sys.config.changed` NATS subscription. All scheduler replicas receive config change notifications instantly. Fallback poll interval is 30s if NATS is unavailable.

### DLQ Cleanup Stalled

**Symptoms**: DLQ entries accumulate beyond the configured retention.

**Diagnosis**:
```bash
redis-cli GET "cordum:dlq:cleanup"
```

**Resolution**: If the lock is held by a crashed replica, wait for TTL expiry. The next live replica will acquire the lock and resume cleanup.

### Circuit Breaker Stuck Open

**Symptoms**: All safety checks fail immediately with circuit breaker open error.

**Diagnosis**:
```bash
redis-cli GET "cordum:cb:safety:failures"
redis-cli GET "cordum:cb:safety:output:failures"
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
curl http://localhost:8081/api/v1/status
curl http://localhost:8083/api/v1/status

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

When running Redis in cluster mode, Lua scripts that touch keys in different hash slots will fail with `CROSSSLOT` errors. Cordum has 12 Lua scripts; 9 are single-key (safe for cluster mode). Three scripts touch multiple keys across slots:

| Script | File | Keys | Risk | Status |
|--------|------|------|------|--------|
| `updateRunScript` | `core/workflow/store_redis.go` | 4 KEYS + dynamic index keys | High (every step completion) | Refactored — index updates moved to Go pipeline |
| `idempotencyScopedScript` | `core/infra/store/job_store.go` | 2 KEYS + dynamic `job:meta:` | Medium (job submit) | Documented; use hash tags or pipeline split for cluster |
| `createUserLua` | `core/controlplane/gateway/auth/userstore_redis.go` | 2 KEYS + dynamic `user:id:`, `user:tenant:` | Low (admin operation) | Documented; use hash tags or pipeline split for cluster |

### Remediation for Redis Cluster

**`updateRunScript` (refactored):** The hot-path Lua script has been split: the atomic GET+SET of the run document stays in Lua (single key), while ZADD/ZREM index updates are performed in a Go pipeline afterward. Index operations are idempotent (ZADD is upsert, ZREM is no-op if missing), so eventual consistency is safe.

**`idempotencyScopedScript` and `createUserLua`:** These remain as multi-key Lua scripts. For Redis Cluster deployments, use hash tags (e.g., `{tenant}:user:...`) to colocate related keys in the same slot, or refactor to pipeline-based approaches similar to `updateRunScript`.

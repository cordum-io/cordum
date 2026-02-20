# Horizontal Scaling Guide

This document covers multi-replica deployment considerations for Cordum services.

## NATS Subject Delivery Matrix

Every NATS subject in Cordum uses one of two delivery modes:

- **Broadcast**: All replicas receive every message (empty queue group). Used for state synchronization where every replica needs the same view.
- **Queue group**: Each message is delivered to exactly one replica in the group. Used for work distribution where processing should happen once.

| Subject | Delivery | Queue Group | JetStream Durable | Purpose | Subscriber(s) |
|---------|----------|-------------|-------------------|---------|---------------|
| `sys.heartbeat` | Broadcast | (none) | No | Worker heartbeats — all replicas maintain registry | Scheduler, Gateway |
| `sys.handshake` | Broadcast | (none) | No | CAP protocol handshake — all replicas track components | Scheduler |
| `sys.job.submit` | Queue | `cordum-scheduler` | Yes (`dur_cordum-scheduler__sys_job_submit`) | Job submission — load-balanced across scheduler replicas | Scheduler |
| `sys.job.result` | Queue | `cordum-scheduler` | Yes (`dur_cordum-scheduler__sys_job_result`) | Job results — load-balanced across scheduler replicas | Scheduler |
| `sys.job.result` | Queue | `cordum-workflow-engine` | Yes (`dur_cordum-workflow-engine__sys_job_result`) | Job results — load-balanced across workflow replicas | Workflow Engine |
| `sys.job.cancel` | Queue | `cordum-scheduler` | Yes (`dur_cordum-scheduler__sys_job_cancel`) | Job cancellation — load-balanced across scheduler replicas | Scheduler |
| `sys.job.dlq` | Broadcast | (none) | Ephemeral | DLQ events — all gateways persist + forward to WS | Gateway |
| `sys.job.>` | Broadcast | (none) | No | Job event tap — all gateways forward to WS clients | Gateway |
| `sys.audit.>` | Broadcast | (none) | No | Audit event tap — all gateways forward to WS clients | Gateway |
| `job.<topic>` | Queue | per-topic | Yes | Job dispatch to workers — load-balanced per topic pool | Workers (SDK) |
| `worker.<id>.jobs` | Queue | per-worker | Yes | Direct dispatch to specific worker | Workers (SDK) |

### JetStream Durable Consumer Naming

When JetStream is enabled (`NATS_USE_JETSTREAM=true`), durable subjects use explicit consumer names for reliable delivery:

- **Queue group subscriptions** use shared durable names: `dur_<queue>__<subject>`. All replicas in the same queue group share a single JetStream consumer, ensuring each message is delivered to exactly one replica.
- **Broadcast subscriptions** use ephemeral consumers (no durable name). Each replica gets its own JetStream consumer, ensuring all replicas receive every message.

This distinction is critical for correctness. A shared durable name on a broadcast subscription would cause JetStream to deliver each message to only one replica, breaking state synchronization.

### Streams

Two JetStream streams cover all durable subjects:

| Stream | Subjects | Purpose |
|--------|----------|---------|
| `CORDUM_SYS` | `sys.>` | System events (submit, result, cancel, DLQ) |
| `CORDUM_JOBS` | `job.>`, `worker.*.jobs` | Job dispatch to worker pools |

### Adding New Subjects

When adding a new NATS subject:

1. Determine delivery mode: Does every replica need the message (broadcast) or should only one handle it (queue group)?
2. For queue group subjects on durable streams, the `durableName()` function in `core/infra/bus/nats.go` automatically generates shared consumer names.
3. For broadcast subjects on durable streams, ephemeral consumers are used automatically — no special configuration needed.
4. Add the subject to this matrix table.
5. If the subject should be durable (at-least-once delivery), add it to `isDurableSubject()` in `core/infra/bus/nats.go`.

## PodDisruptionBudgets

Infrastructure StatefulSets have PDBs using `minAvailable` to preserve quorum during node drains:

- **NATS** (`minAvailable: 2`): A 3-node NATS cluster requires 2 nodes for Raft quorum. Draining 2 nodes simultaneously would break consensus and halt message delivery.
- **Redis** (`minAvailable: 4`): A 6-node Redis cluster (3 primary + 3 replica) needs at least 4 nodes to maintain data availability during rolling upgrades.

Application services use `maxUnavailable: 1` PDBs. See [K8s Deployment Guide](./k8s-deployment.md#poddisruptionbudgets) for the full list.

## HA Validation Suite

An end-to-end acceptance suite verifies multi-replica correctness before deployment. It runs 5 scenarios against a 2-replica docker-compose topology:

1. **Duplicate dispatch guard** — jobs submitted round-robin across gateways reach terminal state exactly once
2. **Global rate limit** — distributed rate limiter enforces shared limit across replicas
3. **Worker snapshot consistency** — both gateways return identical worker sets
4. **Config propagation** — config reads match across gateways (shared Redis)
5. **Lock-holder failover** — scheduler takeover after replica crash without duplicate processing

```bash
# Start HA stack and run validation
docker compose -f docker-compose.yml -f docker-compose.ha.yaml up -d --build
bash tests/e2e/ha_validation.sh
```

See [tests/e2e/README.md](../tests/e2e/README.md) for full instructions.

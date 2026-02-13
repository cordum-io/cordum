# ADR-003: Redis as State Store + NATS as Message Bus

- Status: Accepted
- Date: 2026-01-10

## Context

Cordum needs:
1. **Durable state** — job metadata, workflow runs, worker tracking, config, sessions
2. **Pub/sub messaging** — job dispatch, heartbeats, event streaming, workflow triggers
3. **Crash recovery** — services must resume from last-known state after restart

A single system (e.g. PostgreSQL for both) would simplify operations but create
coupling between state queries and message flow.

## Decision

Split responsibilities:
- **Redis** — primary state store for all mutable data (jobs, runs, config, sessions, context pointers)
- **NATS JetStream** — message bus for event delivery, job dispatch, and streaming

### Why Redis for State (Not PostgreSQL)

| Concern | Redis | PostgreSQL |
|---------|-------|------------|
| Latency | Sub-ms reads | 1-5 ms typical |
| Schema | Schemaless (JSON, sorted sets) | Requires migrations |
| Ops complexity | Single binary, minimal config | WAL, vacuuming, extensions |
| Scaling | Redis Cluster or Sentinel | Read replicas, connection pooling |
| Job state TTL | Native key expiry | Requires cron/cleanup jobs |

Redis fits the access pattern: high-frequency reads/writes of small JSON
objects with TTL-based lifecycle. Job metadata, worker heartbeats, and config
lookups benefit from sub-millisecond latency.

### Why NATS JetStream for Messaging

| Concern | NATS JetStream | Redis Pub/Sub |
|---------|---------------|---------------|
| Durability | File-backed streams | Fire-and-forget |
| Consumer groups | Built-in | Requires Streams API |
| Replay | Configurable retention | No replay |
| Back-pressure | Flow control | None |

NATS JetStream provides durable, replayable message delivery needed for job
dispatch (at-least-once), workflow step triggers, and event streaming.

Key source files:
- `core/infra/memory/job_store.go` — Redis job state
- `core/controlplane/scheduler/engine.go` — NATS pub/sub for dispatch
- `config/nats.conf` — JetStream configuration

### Persistence Configuration

Redis: AOF + RDB snapshots (configurable via `redis-server --appendonly yes`)
NATS: JetStream file store with configurable `sync_interval` for durability vs throughput

## Consequences

Positive:
- Clear separation of concerns (state vs messaging)
- Each system optimized for its workload
- Independent scaling and failure domains
- Sub-millisecond state lookups on scheduler hot path

Tradeoffs:
- Two infrastructure dependencies to operate
- No cross-system transactions (eventual consistency between state and events)
- Redis persistence requires operator attention (AOF rewrite, memory limits)

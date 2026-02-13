# ADR-002: Context Pointers vs Inline Payloads

- Status: Accepted
- Date: 2026-01-10

## Context

Agent jobs carry input context (prompts, documents, tool outputs) and produce
result payloads. These can be arbitrarily large — multi-MB conversation
histories, generated code, or binary artifacts.

The NATS bus protocol needs to remain lightweight for routing and scheduling
decisions. Embedding full payloads in bus messages would:
- bloat NATS JetStream storage
- increase scheduler memory pressure
- create serialization bottlenecks on the hot path

## Decision

Use **pointer semantics** on the bus. Job requests carry a `context_ptr` and
results carry a `result_ptr` — opaque Redis keys pointing to the actual payload
stored in the Redis blob store.

Wire protocol (CAP v2):
```protobuf
message JobRequest {
  string context_ptr = 3;  // "ctx:job:<id>" — pointer, NOT payload
}
message JobResult {
  string result_ptr = 3;   // "res:job:<id>" — pointer, NOT payload
}
```

Storage pattern:
- `SetContext(jobID, payload) → "ctx:job:<id>"` — stores payload in Redis with TTL
- `GetContext(ptr) → payload` — dereferences pointer to retrieve data
- Same pattern for `SetResult` / `GetResult`
- TTL managed by `REDIS_DATA_TTL` (default 24h) and `JOB_META_TTL` (default 168h)

Key source files:
- `core/protocol/proto/v1/bus.proto` — wire format
- `core/infra/memory/job_store.go` — Redis pointer storage

## Why Not S3/Object Store

Redis provides sub-millisecond reads, which matters for:
- Workers fetching context at job start
- Dashboard displaying results in real-time
- Output policy scanning dereferenced payloads

For very large payloads (>10 MB), a future extension could tier to object
storage with Redis as a cache. The pointer abstraction makes this transparent.

## Consequences

Positive:
- Bus messages stay small (< 1 KB typical)
- Scheduler processes routing metadata without touching payloads
- Workers dereference only when ready to execute
- Output policy can scan payloads independently of bus flow

Tradeoffs:
- Redis memory usage scales with active job payload sizes
- TTL expiry means payloads are not permanent (by design)
- Network round-trip to Redis for every dereference

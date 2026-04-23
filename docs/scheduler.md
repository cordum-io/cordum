# Scheduler

The Cordum scheduler is the control-plane component that receives
`JobRequest` payloads from the gateway and the workflow engine, runs
them through the safety-kernel decision path, and dispatches them to
a worker in the target pool. This document covers scheduler-specific
behavior that isn't captured in the broader architecture overview;
see `docs/system_overview.md` for how the scheduler fits with the
other six services.

## Flush-on-worker-online

### How it works

When a worker transitions from offline to online — its first
heartbeat after registry startup, or the first heartbeat after its
prior entry expired past the registry TTL — the scheduler
immediately flushes pending dispatch for that worker's pool instead
of waiting for the next `PendingReplayer` poll tick (default: 30
seconds).

The transition is detected inside `MemoryRegistry.UpdateHeartbeatWithTransition`
(`core/controlplane/scheduler/registry_memory.go`): the method
upserts the heartbeat and returns `true` when the worker was either
absent from the registry or had a `lastSeen` older than the registry
TTL at the moment of the heartbeat. The engine type-asserts the
registry for this method in its heartbeat handler
(`core/controlplane/scheduler/engine.go`) — registries that don't
implement it (legacy test mocks) fall back to the non-transition
`UpdateHeartbeat` call, so adding the method was not a breaking
change to the `WorkerRegistry` interface.

When the transition is observed and the heartbeat carries a
non-empty `Pool`, the engine schedules a non-blocking flush via
`scheduleFlushOnWorkerOnline(pool, workerID, traceID)`:

1.  A per-pool `flushLatch` (backed by `sync.Map`) collapses
    concurrent heartbeats from a freshly-scaled fleet into a single
    flush per pool. If three replicas of the same pool come online
    within a handful of milliseconds, only the first acquires the
    latch; the other two exit early.
2.  The flush runs on a goroutine tracked by the engine's `wg`
    waitgroup so `Stop()` drains cleanly.
3.  The goroutine calls `defaultFlushDispatchForPool(ctx, pool, traceID)`,
    which lists pending and scheduled jobs (5-minute lookback, batch
    200), filters them by pool via the `reqBelongsToPool` check, and
    replays each through `engine.handleJobRequest` — the same entry
    point `PendingReplayer` uses for its periodic scans. No forked
    dispatch logic; the flush is a synchronous counterpart to the
    poll-tick replay.
4.  Tests inject a custom flush function via
    `engine.WithFlushDispatchFn(...)`, bypassing the full dispatch
    pipeline so behavior assertions can run in-process.

### Observability

-   **Metric**: `cordum_scheduler_dispatch_flush_on_worker_online_total{pool}`
    — counts flushes that resulted in at least one pending job
    being dispatched. No-op flushes (worker online but queue empty)
    do not increment the counter; use the INFO log below to count
    attempts.
-   **Log**: `INFO flush on worker online pool=<pool>
    worker_id=<id> dispatched=<n> trace_id=<trace>` — always fires,
    regardless of whether any jobs were flushed. The `trace_id` is
    propagated from the bus packet that carried the triggering
    heartbeat so operators can cross-reference with gateway /
    workflow-engine traces.
-   **Latch collisions**: when the per-pool latch blocks a
    concurrent flush, no log or metric is emitted (the collision is
    not a degenerate state — it is the design).

### Non-goals

-   **Not a replacement for poll-tick dispatch.** The
    `PendingReplayer` still runs its periodic scan; this flush is a
    belt-and-suspenders layer that closes the scale-from-zero window,
    not the steady-state dispatcher. Without this, a worker that
    came online 5 seconds after its pending job was enqueued would
    wait for the next poll tick; with it, the job dispatches as soon
    as the heartbeat lands.
-   **No mock-bank / demo-pack / workflow-engine changes.** The fix
    is entirely scheduler-side. Workers continue to send standard
    heartbeats; the workflow engine continues to route steps to
    topics the way it always did.
-   **No safety-kernel changes.** The flush replays jobs through the
    same `handleJobRequest` path that applies all safety checks, so
    policy admission is unchanged.

### Upgrade notes

-   The new behavior is always-on. There is no feature flag: if
    anything in your environment was relying on the previous
    scale-from-zero latency (unlikely — it is strictly a UX
    regression), file a follow-up to surface a flag.
-   The metric is registered on the default pod-scoped registerer.
    Operators who scrape `/metrics` on the scheduler will see the new
    series as soon as the first non-empty flush runs; before any
    flush, the series will not appear (Prometheus convention for
    counters that never observed a value).

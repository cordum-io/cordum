---
sidebar_position: 10
title: "Scheduler Internals"
slug: /concepts/scheduler-internals
---

# Scheduler Internals

The Cordum scheduler is the central job routing and lifecycle engine. It receives
job requests over the message bus, evaluates them against the safety kernel,
routes them to worker pools, and manages state transitions through completion
(including output policy enforcement). This document covers the scheduler's
internal architecture—state machine, output policy integration, reconciliation,
saga/compensation, routing strategy, and circuit breaker.

> **See also**: Pool Routing Config ·
> [Output Policy](/concepts/output-policy) · [Safety Kernel](/concepts/safety-kernel) ·
> [API Reference](/api-reference/full-reference)

---

## 1. Job State Machine

Every job progresses through a well-defined set of states. Terminal states are
**bold**.

```
                           ┌─────────────────────────────────────────────┐
                           │              (job submitted)                │
                           ▼                                             │
                       PENDING ──────────── safety check ────────────────┤
                           │                     │                       │
                   ALLOW / │        REQUIRE_     │ DENY /               │
              ALLOW_WITH_  │        APPROVAL     │ UNKNOWN              │
              CONSTRAINTS  │            │        │                      │
                           ▼            ▼        ▼                      │
                       SCHEDULED   APPROVAL_  **DENIED**                │
                           │       REQUIRED      │                      │
                           │         │ (approved) │                     │
                           │         └──►PENDING──┘                     │
                           ▼                                             │
                       DISPATCHED                                        │
                           │                                             │
                           ▼                                             │
                        RUNNING                                          │
                         │   │                                           │
              succeeded ─┘   └─ failed / timeout / cancelled             │
                 │                   │          │          │              │
                 ▼                   ▼          ▼          ▼              │
           ┌─ sync output ─┐   **FAILED**  **TIMEOUT** **CANCELLED**    │
           │    policy      │        │                                   │
           ▼                ▼        ▼ (FAILED_FATAL                     │
      **SUCCEEDED**  **OUTPUT_       │  + workflow_id)                   │
           │         QUARANTINED**   └──► saga rollback                  │
           │                                                             │
           ▼                                                             │
      async output ──(quarantine)──► **OUTPUT_QUARANTINED**              │
        policy                                                           │
           │                                                             │
           └── (allow) ── no state change                                │
                                                                         │
      max scheduling retries (50) ──────────► **FAILED** + DLQ ─────────┘
```

### Terminal States

| State                | Description                                        |
|----------------------|----------------------------------------------------|
| `SUCCEEDED`          | Job completed successfully, output policy passed   |
| `FAILED`             | Job failed or exhausted retries                    |
| `TIMEOUT`            | Job exceeded deadline or reconciler timeout        |
| `CANCELLED`          | Job cancelled by user request                      |
| `DENIED`             | Safety kernel denied the job                       |
| `OUTPUT_QUARANTINED` | Output policy blocked the result after completion  |

### Key Transition Rules

- **Idempotency**: If a job is already in a terminal state, duplicate results
  are silently ignored.
- **Max scheduling retries**: After 50 failed dispatch attempts (exponential
  backoff 1s–30s), the job moves to `FAILED` and is emitted to the DLQ.
- **Approval flow**: `REQUIRE_APPROVAL` → job waits in `APPROVAL_REQUIRED`.
  When approved (`approval_granted=true` label + matching job hash), the job
  re-enters the normal dispatch flow.

---

## 2. Output Policy Integration

The output policy system provides a two-phase model for scanning job results:

### Phase 1: Sync Metadata Check (hot path)

Runs inline in `handleJobResult` after saga recording, before the final state
transition. Uses `CheckOutputMeta(res, req)` which inspects metadata only
(~1ms target).

```
Job result received (SUCCEEDED)
    │
    ├─ Record saga compensation (if applicable)
    │
    ├─ Sync output check (CheckOutputMeta)
    │   ├─ ALLOW        → state = SUCCEEDED
    │   ├─ QUARANTINE   → state = OUTPUT_QUARANTINED → DLQ + audit event
    │   ├─ DENY         → state = OUTPUT_QUARANTINED → DLQ + audit event
    │   ├─ REDACT       → materialize redaction
    │   │   ├─ redacted_ptr available → state = SUCCEEDED (swap result ptr)
    │   │   └─ redacted_ptr missing   → state = OUTPUT_QUARANTINED
    │   └─ error/skip   → state = SUCCEEDED (fail-open)
    │
    └─ Persist result pointer
```

### Phase 2: Async Content Scan (goroutine)

Runs after the sync phase for `SUCCEEDED` jobs. Uses `CheckOutputContent(ctx,
res, req)` with a 30-second timeout. Dereferences the actual output payload for
deep analysis.

```
Async goroutine (30s timeout)
    │
    ├─ CheckOutputContent(ctx, res, req)
    │   ├─ ALLOW  → no state change
    │   ├─ QUARANTINE/DENY → acquire job lock
    │   │   ├─ current state == SUCCEEDED → downgrade to OUTPUT_QUARANTINED
    │   │   ├─ current state != SUCCEEDED → skip (already transitioned)
    │   │   └─ emit DLQ + audit event
    │   └─ error → skip (fail-open)
    │
    └─ Persist output safety record
```

### Fail-Open Default

If the output safety checker returns an error (e.g., gRPC timeout, circuit
open), the scheduler defaults to `ALLOW`. This is a deliberate fail-open design
to avoid blocking the job pipeline when the output policy service is
unavailable.

### Configuration

| Variable                | Default | Description                          |
|------------------------|---------|--------------------------------------|
| `OUTPUT_POLICY_ENABLED` | `false` | Enable output policy checks          |

The output safety checker is wired via `Engine.WithOutputSafety(checker)` and
toggled with `Engine.WithOutputSafetyEnabled(true)`.

### Output Decisions

| Decision     | Effect                                                |
|--------------|-------------------------------------------------------|
| `ALLOW`      | Job result passes through unchanged                   |
| `QUARANTINE` | Job moves to `OUTPUT_QUARANTINED`, DLQ entry emitted  |
| `DENY`       | Same as `QUARANTINE` (treated identically)            |
| `REDACT`     | Redacted content replaces original result pointer     |

---

## 3. Reconciler

The reconciler runs as a background loop to detect and clean up stale jobs.

### How It Works

1. **Tick interval**: Configurable via `pollInterval` (default 30s).
2. **Distributed lock**: Uses Redis `TryAcquireLock` with key
   `cordum:reconciler:default` (TTL = 2× poll interval). Only one reconciler
   instance runs per tick across all scheduler replicas.
3. **Timeout detection**: Scans for jobs in `DISPATCHED` or `RUNNING` state
   with `updated_at` older than the configured timeout.
4. **Deadline expiration**: Checks jobs with explicit deadlines
   (`budget.deadline_ms`) that have passed.
5. **State transition**: Timed-out jobs move to `TIMEOUT` with a failure reason
   recorded.

### Reconciler Configuration

| Parameter            | Default  | Description                                 |
|---------------------|----------|---------------------------------------------|
| `dispatchTimeout`   | varies   | Max time in `DISPATCHED` before timeout     |
| `runningTimeout`    | varies   | Max time in `RUNNING` before timeout        |
| `pollInterval`      | 30s      | How often the reconciler runs               |
| Lock TTL            | 2× poll  | Distributed lock duration                   |
| Max iterations/tick | 100      | Cap on timeout processing iterations        |
| Batch size          | 200      | Jobs fetched per iteration                  |
| Max retries/job     | 3        | Retry attempts for state transition errors  |

### Pending Replayer

A separate component (`PendingReplayer`) runs alongside the reconciler to
recover orphaned jobs:

- **Pending jobs**: Jobs stuck in `PENDING` longer than `pendingAge` (default
  2 minutes) are re-submitted through `handleJobRequest`.
- **Approved jobs**: Jobs in `APPROVAL_REQUIRED` with `approval_granted=true`
  label are replayed to resume dispatch.
- **Distributed lock**: `cordum:replayer:pending` (TTL = 2× poll interval).
- **Metrics**: Orphan replays are counted via `IncOrphanReplayed(topic)`.

```
PendingReplayer tick (every 30s)
    │
    ├─ Scan PENDING jobs older than 2 minutes
    │   └─ For each: load JobRequest → handleJobRequest(req, traceID)
    │
    └─ Scan APPROVAL_REQUIRED jobs older than 2 minutes
        └─ For each with approval_granted=true: handleJobRequest(req, traceID)
```

---

## 4. Saga / Compensation

The saga manager provides durable rollback for multi-step workflows.

### Recording Compensation

When a job **succeeds** and has a `Compensation` field defined in its request:

1. A compensation job template is built from the original request + compensation
   overrides (topic, context, priority, budget, labels, env).
2. The template is serialized and pushed onto a Redis list (`saga:<workflow_id>:stack`).
3. Compensation jobs always inherit `CRITICAL` priority.
4. An idempotency key is auto-generated from
   `sha256(workflow_id|job_id|comp_topic|capability|step_index)`.

### Rollback Trigger

Rollback fires when a job result arrives with status `FAILED_FATAL` and the job
belongs to a workflow:

```
handleJobResult (FAILED_FATAL + workflow_id)
    │
    └─ goroutine: saga.Rollback(ctx, workflowID) [30s timeout]
        │
        ├─ Acquire saga lock (saga:<workflow_id>:lock, TTL 2min)
        │
        ├─ Pop compensation requests from stack (LIFO order)
        │   └─ For each compensation:
        │       ├─ Assign new job ID (comp-<uuid>)
        │       ├─ Set labels: saga_compensation=true, saga_workflow_id=<id>
        │       ├─ Soft safety check:
        │       │   ├─ DENY → skip this compensation
        │       │   ├─ UNAVAILABLE → proceed anyway
        │       │   └─ ALLOW → dispatch
        │       └─ Publish to sys.job.submit
        │
        └─ Release saga lock
```

### Compensation Properties

| Property            | Value                                  |
|---------------------|----------------------------------------|
| Priority            | `CRITICAL` (always)                    |
| Labels              | `saga_compensation=true`, `is_compensation=true` |
| Env                 | `saga_compensation=true`, `saga_workflow_id=<id>` |
| Idempotency         | Auto-generated hash unless explicitly set |
| Safety check        | Soft — deny skips, unavailable proceeds |
| Unmarshal errors    | Logged + sent to DLQ as `saga_unmarshal_failed` |

---

## 5. Advanced Routing

The scheduler uses a least-loaded strategy with label-based placement.

### Routing Algorithm

```
PickSubject(req, workers)
    │
    ├─ Resolve topic → pool mapping from PoolRouting config
    │   └─ If preferred_pool label set → narrow to that pool only
    │
    ├─ Filter eligible pools by job `requires` vs pool capabilities
    │
    ├─ Preferred worker shortcut:
    │   └─ If preferred_worker_id label matches a healthy, non-overloaded
    │      worker in an eligible pool → return direct subject immediately
    │
    ├─ Score all workers in eligible pools:
    │   score = active_jobs + (cpu_load / 100) + (gpu_utilization / 100)
    │   └─ Skip overloaded workers (see threshold below)
    │
    └─ Select worker with lowest score → return direct subject
```

### Label Hints

| Label                    | Effect                                         |
|--------------------------|------------------------------------------------|
| `preferred_pool`         | Restrict dispatch to a specific pool           |
| `preferred_worker_id`    | Direct dispatch to a specific worker if healthy|
| `placement.*`            | Placement constraint matching on worker labels |
| `constraint.*`           | Worker capability constraint matching          |
| `node.*`                 | Node selector label matching                   |

### Overload Detection

A worker is considered overloaded if **any** of these are true:

- `active_jobs / max_parallel_jobs >= 0.9` (90% utilization)
- `cpu_load >= 90`
- `gpu_utilization >= 90`

### Reason Codes

When dispatch fails, a reason code is attached to the DLQ entry:

| Code               | Meaning                                        |
|--------------------|------------------------------------------------|
| `no_pool_mapping`  | No pool configured for the job's topic         |
| `no_workers`       | No workers available in the target pool        |
| `pool_overloaded`  | All workers in the pool exceed load thresholds |
| `tenant_limit`     | Tenant concurrency limit reached               |
| `safety_denied`    | Safety kernel denied the job                   |
| `dispatch_failed`  | Generic dispatch failure                       |

### Exponential Backoff

Retryable scheduling errors use exponential backoff with cryptographic jitter:

```
delay = min(base × 2^attempt + jitter, max)
  base   = 1s
  max    = 30s
  jitter = random [0, 500ms) (crypto/rand)
  max attempts = 50 (then FAILED + DLQ)
```

---

## 6. Circuit Breaker (Safety Client)

The safety client wraps the gRPC connection to the safety kernel with a circuit
breaker to prevent cascading failures.

### State Diagram

```
                     ┌───────────────────────┐
                     │       CLOSED          │
                     │  (normal operation)    │
                     │  failures reset on     │
                     │  each success          │
                     └───────┬───────────────┘
                             │
                    3 consecutive failures
                             │
                             ▼
                     ┌───────────────────────┐
                     │        OPEN           │
                     │  (all requests return │
                     │   SafetyUnavailable)  │
                     │  duration: 30s        │
                     └───────┬───────────────┘
                             │
                       30s elapsed
                             │
                             ▼
                     ┌───────────────────────┐
                     │     HALF-OPEN         │
                     │  (allow up to 3 probe │
                     │   requests)           │
                     └──┬────────────────┬───┘
                        │                │
                 any failure        2 successes
                        │                │
                        ▼                ▼
                      OPEN            CLOSED
                   (30s again)     (fully recovered)
```

### Circuit Breaker Constants

| Parameter             | Value | Description                               |
|----------------------|-------|-------------------------------------------|
| `safetyTimeout`       | 2s    | gRPC call timeout per safety check        |
| `safetyCircuitFailBudget` | 3 | Failures before circuit opens            |
| `safetyCircuitOpenFor` | 30s  | Duration circuit stays open               |
| `safetyCircuitHalfOpenMax` | 3 | Max probe requests in half-open state   |
| `safetyCircuitCloseAfter` | 2  | Successes needed to close from half-open |

### Multi-Replica State (Redis-Backed)

Circuit breaker state is shared across all scheduler replicas via Redis:

| Redis Key | Purpose |
|-----------|---------|
| `cordum:cb:safety:failures` | Input safety circuit breaker failure counter |
| `cordum:cb:safety:output:failures` | Output safety circuit breaker failure counter |

When any replica records a failure, the shared counter increments. When the
threshold is reached, all replicas observe the open state simultaneously. This
prevents the scenario where one replica's circuit is open while another
continues sending requests to a degraded safety kernel.

If Redis is unavailable, each replica falls back to local-only circuit breaker
tracking (per-process counters).

### Behavior When Circuit Is Open

All safety checks return `SafetyUnavailable` with reason `"safety kernel
circuit open"`. The scheduler treats `SafetyUnavailable` as a retryable
condition — the job is requeued with a 5-second delay.

### Input Policy Fail Mode

The scheduler's behavior when the safety kernel is unreachable (circuit open or
gRPC timeout) is controlled by the `POLICY_CHECK_FAIL_MODE` setting:

- **Fail-closed (default)**: The job is requeued with exponential backoff. This
  is the safe default — no job passes through without a policy decision.
- **Fail-open**: The job is allowed through with a warning log
  (`"input policy fail-open: safety kernel unreachable"`) and the
  `cordum_scheduler_input_fail_open_total` Prometheus counter is incremented
  (labeled by `topic`). This trades safety guarantees for availability.

Configuration:
- **Env var**: `POLICY_CHECK_FAIL_MODE` — values: `closed` (default), `open`
- **Config file**: `config/safety.yaml` under `input_policy.fail_mode`

The env var takes precedence over the config file value.

---

## 7. Environment Variables

| Variable                         | Default        | Description                              |
|---------------------------------|----------------|------------------------------------------|
| `OUTPUT_POLICY_ENABLED`          | `false`        | Enable output policy checks              |
| `SAFETY_KERNEL_TLS_CA`           | (none)         | Path to safety kernel CA certificate     |
| `SAFETY_KERNEL_TLS_REQUIRED`     | `false`        | Require TLS for safety kernel connection |
| `SAFETY_KERNEL_INSECURE`         | `false`        | Allow insecure (non-TLS) connection      |

### Scheduler Constants (compile-time)

| Constant               | Value | Description                                     |
|------------------------|-------|-------------------------------------------------|
| `storeOpTimeout`       | 2s    | Timeout for Redis store operations              |
| `jobLockTTL`           | 60s   | TTL for per-job distributed locks (with renewal) |
| `maxSchedulingRetries` | 50    | Max dispatch attempts before DLQ                |
| `retryDelayBusy`       | 500ms | Delay when job lock is busy                     |
| `retryDelayStore`      | 1s    | Delay after store operation failure             |
| `retryDelayPublish`    | 2s    | Delay after bus publish failure                 |
| `retryDelayNoWorkers`  | 2s    | Delay when no workers available                 |
| `safetyThrottleDelay`  | 5s    | Delay when safety kernel throttles              |
| `backoffBase`          | 1s    | Exponential backoff base for scheduling retries |
| `backoffMax`           | 30s   | Maximum backoff delay                           |
| `maxRenewalFailures`   | 3     | Consecutive renewal failures before abandon     |

---

## 8. Distributed Locking

The scheduler uses Redis-based distributed locks to ensure consistency:

| Lock Key                      | TTL           | Release          | Renewal     | Purpose                              |
|-------------------------------|---------------|------------------|-------------|--------------------------------------|
| `cordum:scheduler:job:<id>`   | 60s           | Explicit (defer) | Yes (ttl/3) | Per-job mutex for state transitions  |
| `cordum:reconciler:default`   | 2× poll interval | TTL expiry    | No          | Single-writer reconciler             |
| `cordum:replayer:pending`     | 2× poll interval | TTL expiry    | No          | Single-writer pending replayer       |
| `cordum:workflow-engine:reconciler:default` | 2× poll interval | TTL expiry | No | Single-writer workflow reconciler |
| `cordum:wf:run:lock:<runID>`  | 30s           | Explicit (defer) | Yes (ttl/3) | Per-run mutex for workflow steps     |
| `saga:<workflow_id>:lock`     | 2 min         | Explicit         | No          | Per-workflow saga rollback mutex     |
| `cordum:scheduler:snapshot:writer` | 10s      | Explicit         | No          | Single-writer snapshot writer        |
| `cordum:wf:delay:poller`  | 10s           | TTL expiry       | No          | Single-writer delay timer poller     |

### Lock-Hold Pattern for Horizontal Scaling

The reconciler, pending replayer, and workflow reconciler use a **TTL-based
lock-hold pattern** instead of explicit release. After acquiring the lock and
running `tick()`, they do **not** call `ReleaseLock`. The lock expires naturally
after its TTL (2× poll interval).

**Why**: If the lock is acquired, tick runs (~10–100ms), and then immediately
released, a second replica can grab the lock within the same poll cycle and
double-process the same jobs. By holding the lock until TTL expiry, only one
replica can run `tick()` per TTL window, preventing duplicate dispatch,
duplicate timeout transitions, and duplicate orphan replays.

```
Replica A: ──acquire──tick()──────────────────TTL expires──
Replica B: ──(blocked)────────────────────────acquire──tick()──
                    ◄── TTL window (2× poll) ──►
```

**Per-job and per-run locks** (`cordum:scheduler:job:<id>`,
`cordum:wf:run:lock:<runID>`) still use explicit `defer ReleaseLock` because
they protect short, targeted operations (single state transition or single run
reconciliation) rather than entire tick cycles.

### Job Lock TTL Renewal

Per-job locks (`cordum:scheduler:job:<id>`) use **TTL renewal** to prevent lock
expiry during long-running operations (safety checks, routing, publish). The
base TTL is 60s and a background goroutine renews the lock every `ttl/3` (20s).

**How it works**:
1. `withJobLock` acquires the lock with a 60s TTL via `TryAcquireLock`.
2. A goroutine starts a `time.Ticker` at `ttl/3` (20s) and calls `RenewLock`
   (Lua: `if GET key == token then PEXPIRE key ttl`).
3. When `fn()` completes, the renewal goroutine is cancelled and drained
   **before** `ReleaseLock` runs, preventing a renewal from racing with release.
4. If a renewal fails (Redis error), the lock still has up to 60s of remaining
   TTL as a safety margin.
5. If `RenewLock` fails **3 consecutive times** (`maxRenewalFailures`), the
   renewal goroutine logs an error and exits. The lock is allowed to expire
   via its 60s TTL. The operation (`fn()`) continues — only renewal stops.

```
withJobLock("job-123", 60s, fn):
  ──acquire(60s)─┬──fn() runs────────────────────────┬──release──
                 │                                    │
  renewal:       ├──20s──renew──20s──renew──20s──renew│
                 │          (each resets TTL to 60s)  │
                 └────────────────────────────────────┘
                                                cancel → drain → release
```

**Renewal abandon (3-strike rule)**: Under Redis pressure, each job's renewal
goroutine self-limits to at most 3 failed attempts before stopping. This
prevents renewal storms from many locked jobs across replicas generating
excessive Redis load and log noise. Intermittent failures do not trigger
abandon — the consecutive failure counter resets to zero on each successful
renewal. With a 60s TTL, the lock expires safely even without renewal.

### Snapshot Writer Lock

The scheduler writes a worker snapshot (`sys:workers:snapshot`) to Redis every
5 seconds. With multiple scheduler replicas, concurrent writes can produce
corrupted or partial snapshots.

**Solution**: Before each snapshot write, the scheduler acquires a Redis lock
(`cordum:scheduler:snapshot:writer`, TTL 10s). Only the lock holder writes.
Non-leader replicas skip silently (debug log). The lock is released immediately
after the write completes, so failover is fast.

```
Replica A: ──acquire──write──release──────────────────────acquire──write──release──
Replica B: ──(skip)───────────────────acquire──write──release──(skip)──────────────
                      ◄── 5s tick ──►
```

**Leader crash recovery**: If the lock holder crashes without releasing, the
lock expires via its 10s TTL. The next tick (5s later), another replica acquires
the lock and resumes writing. Maximum snapshot staleness on crash: ~15s.

### Distributed Workflow Run Locks

Each workflow run is protected by a **two-layer locking** scheme for
cross-replica mutual exclusion:

1. **Local mutex** (fast): Per-run `sync.Mutex` prevents intra-process
   contention and avoids unnecessary Redis round-trips.
2. **Redis lock** (distributed): `cordum:wf:run:lock:<runID>` (TTL 30s) with
   automatic renewal at `ttl/3` (10s) prevents cross-replica concurrent
   modification of the same run.

```
Same-process goroutines:
  G1: ──local.Lock──Redis.Lock──work──Redis.Unlock──local.Unlock──
  G2: ──local.Lock(blocked)────────────────────────local.Lock──...──

Cross-replica:
  Replica A: ──Redis.Lock──work──Redis.Unlock──
  Replica B: ──Redis.Lock(contended, local-only fallback)──work──
```

**Graceful degradation**: If Redis is unavailable or the lock is contended by
another replica, the engine proceeds with local-only locking and logs a warning.
This preserves single-replica backward compatibility and avoids blocking the
workflow pipeline during transient Redis failures.

**Release order**: Redis lock is released **before** the local mutex (per
design: avoids holding a distributed lock while waiting for a local resource).

The workflow reconciler's `HandleJobResult` and `reconcileRun` also acquire
`cordum:wf:run:lock:<runID>` via the job store, providing consistent
cross-replica protection across both the engine and reconciler paths.

### Durable Workflow Delay Timers

Workflow delay steps (`delay_sec`, `delay_until`) and retry backoff use
`time.AfterFunc` to schedule run resumption. These in-memory timers are lost
if the engine crashes or restarts. Durable delay timers add a Redis sorted set
as a persistence layer.

**Redis key**: `cordum:wf:delay:timers` (sorted set)
- **Member**: `workflowID:runID`
- **Score**: Unix seconds of fire time

**How it works**:
1. `scheduleAfter()` persists delays ≥10s to the sorted set via `ZADD`, then
   creates the in-memory `time.AfterFunc` as before.
2. When the timer fires, the sorted set entry is removed via `ZREM` before
   calling `StartRun`.
3. Delays <10s skip Redis (fast path — not worth the round-trip for sub-10s).

```
scheduleAfter(30s):
  ──ZADD(fireAt=now+30s)──AfterFunc(30s)──...30s...──ZREM──StartRun──
```

**Crash recovery** (`recoverDelayTimers`, called on startup):
1. **Past-due timers**: Atomic Lua script (`ZRANGEBYSCORE + ZREM`) pops all
   entries with score ≤ now. Each is fired immediately via `StartRun`.
2. **Future timers**: `ZRANGEBYSCORE` fetches entries with score > now. Each is
   re-scheduled via `scheduleAfter(remaining)`, which re-adds to the ZSET
   (idempotent via `ZADD`).

```
Engine restart:
  ──PopFiredDelays(now)──fire each──ListFutureDelays──reschedule each──
```

**Background poller** (`startDelayPoller`, runs every 5s):
- Catches timers lost by crashed replicas that haven't restarted yet.
- Uses distributed lock (`cordum:wf:delay:poller`, TTL 10s) so only one
  replica polls at a time.
- Every ~5 minutes, cleans stale entries (>1h past-due) to prevent unbounded
  ZSET growth from orphaned timers (e.g. run deleted while timer was pending).

| Lock Key                    | TTL  | Release    | Purpose                    |
|-----------------------------|------|------------|----------------------------|
| `cordum:wf:delay:poller`    | 10s  | TTL expiry | Single-writer delay poller  |

**Graceful degradation**: If Redis is unavailable during `scheduleAfter`, the
timer is still created in-memory (logged as warning). The reconciler provides
eventual recovery for delay steps via `NextAttemptAt` checks.

---

## 9. Crash-Safe Message Processing

NATS JetStream provides **at-least-once delivery** with AckWait (10min) and
MaxDeliver (100). However, there are crash windows between processing and
acknowledgment that can cause duplicate work or data loss. The bus layer adds
Redis-backed guards to close these gaps.

### Idempotency Guard

When `NatsBus.WithRedis(client)` is set, every durable JetStream subscription
checks a processed-message key before invoking the handler:

```
Message arrives (stream=CORDUM_JOBS, seq=42):
  ──EXISTS cordum:bus:processed:CORDUM_JOBS:42──
     │                                │
     │ exists → Ack (skip handler)    │ not exists → process
     │                                │
     └────────────────────────────────┘
                                      │
                                ──handler()──SET processed──Ack──
```

| Redis Key                                      | TTL   | Purpose                         |
|------------------------------------------------|-------|---------------------------------|
| `cordum:bus:processed:<stream>:<seq>`           | 10min | Idempotency dedup (= AckWait)   |
| `cordum:bus:inflight:<stream>:<seq>`            | 2min  | Observability (in-flight msgs)  |

**Crash scenario covered**: Replica A processes a message, crashes before Ack.
NATS redelivers to Replica B after AckWait. B finds the processed key in Redis
and skips processing (just Acks). Without the guard, B would double-process.

### In-Flight Tracking

Before calling the handler, the bus sets a short-lived inflight key
(`cordum:bus:inflight:<stream>:<seq>`, TTL 2min). This key is deleted after the
handler completes. If the replica crashes mid-processing, the key expires via
TTL. This is informational only — the actual retry mechanism is JetStream's
redelivery.

### DLQ-First Termination

When a message reaches permanent failure (poison pill or corrupt payload), the
bus calls `OnMessageTerminated` (DLQ write) **before** calling `msg.Term()`.
If the DLQ callback returns an error (e.g. Redis unavailable), the message is
**Nak'd with 5s delay** instead of terminated. This prevents the scenario where
`Term()` succeeds, the replica crashes, and the DLQ entry is never written —
permanently losing the message.

```
Before (unsafe):  msg.Term() → OnMessageTerminated() → (crash = message lost)
After  (safe):    OnMessageTerminated() → success? → msg.Term()
                                        → error?  → msg.NakWithDelay(5s) (retry)
```

### Graceful Degradation

If Redis is unavailable (connection error, timeout), the idempotency check and
inflight tracking are silently skipped. Processing continues with JetStream-only
semantics (at-least-once with AckWait-based redelivery). A warning is logged on
the first Redis failure per message.

---

## 10. Metrics

The scheduler exposes the following metrics:

### Job Lifecycle

| Metric                           | Type      | Labels         |
|----------------------------------|-----------|----------------|
| `jobs_received`                  | Counter   | `topic`        |
| `jobs_dispatched`                | Counter   | `topic`        |
| `jobs_completed`                 | Counter   | `topic`, `status` |
| `safety_denied`                  | Counter   | `topic`        |
| `safety_unavailable`             | Counter   | `topic`        |
| `dispatch_latency`               | Histogram | `topic`        |
| `job_lock_wait`                  | Histogram | —              |
| `active_goroutines`              | Gauge     | —              |
| `stale_jobs`                     | Gauge     | `state`        |
| `orphan_replayed`                | Counter   | `topic`        |

### Output Policy

| Metric                           | Type      | Labels              |
|----------------------------------|-----------|---------------------|
| `output_policy_checked`          | Counter   | `topic`             |
| `output_policy_quarantined`      | Counter   | `topic`             |
| `output_policy_skipped`          | Counter   | `topic`             |
| `output_check_latency`           | Histogram | `topic`, `phase`    |

### Saga

| Metric                           | Type      |
|----------------------------------|-----------|
| `saga_recorded`                  | Counter   |
| `saga_rollback_triggered`        | Counter   |
| `saga_compensation_dispatched`   | Counter   |
| `saga_compensation_failed`       | Counter   |
| `saga_rollback_duration`         | Histogram |
| `saga_active`                    | Gauge     |
| `saga_unmarshal_error`           | Counter   |

---

## 11. Registry Warm-Start from Redis Snapshot

New scheduler replicas start with an empty `MemoryRegistry`. Without warm-start,
it takes up to 30s for heartbeats to fill the registry, causing `ErrNoWorkers`
for any job submitted in that window.

**Solution**: On startup, the scheduler reads `sys:workers:snapshot` from Redis
(the same snapshot written every 5s by the snapshot writer) and hydrates the
registry with `HydrateFromSnapshot()`.

```
Cold start (before):
  Replica B starts ──(0-30s empty registry)──heartbeats fill──ready

Warm start (after):
  Replica B starts ──read snapshot──hydrate──ready (< 1s)
                                              ↑ heartbeats refresh within seconds
```

**Behavior**:
- Snapshot read has a 5s timeout — never blocks startup
- Workers from the snapshot are inserted with `lastSeen = time.Now()`, so normal
  30s TTL expiry applies (stale workers evicted if no live heartbeat follows)
- If Redis is unavailable, snapshot is missing, or data is corrupt: log warning
  and continue with cold start (heartbeats fill the registry as before)
- Live heartbeats always take precedence over snapshot data (last-write-wins)

**Redis key**: `sys:workers:snapshot` (same key used by snapshot writer)

---

## 12. Source Files

| File                            | Purpose                                |
|---------------------------------|----------------------------------------|
| `engine.go`                     | Core engine: packet handling, job request/result processing, output policy integration |
| `types.go`                      | All type definitions: states, decisions, interfaces |
| `safety_client.go`              | gRPC safety client with circuit breaker |
| `output_safety_client.go`       | gRPC output policy client              |
| `reconciler.go`                 | Timeout detection and cleanup loop     |
| `pending_replayer.go`           | Orphaned pending/approved job recovery |
| `saga.go`                       | Compensation stack and rollback logic  |
| `strategy_least_loaded.go`      | Least-loaded worker selection          |
| `routing.go`                    | Pool routing data structures           |
| `errors.go`                     | Sentinel scheduling errors             |
| `backoff.go`                    | Exponential backoff with crypto jitter |
| `retry.go`                      | Retry-after error wrapper              |
| `job_hash.go`                   | Job request hashing for approval verification |
| `tenant.go`                     | Tenant extraction helpers              |
| `registry_memory.go`            | In-memory worker heartbeat registry    |

---

## Cross-References

- **Pool Routing Config** — How topics map to pools
  and how `pools.yaml` is structured.
- **[Output Policy](/concepts/output-policy)** — Output scanning rules, scanner
  configuration, and quarantine runbook.
- **[Safety Kernel](/concepts/safety-kernel)** — Input policy evaluation, MCP filters,
  overlays, and the gRPC contract.
- **[API Reference](/api-reference/full-reference)** — REST endpoints for job submission,
  cancellation, and DLQ management.
- **[gRPC Services](/api-reference/grpc-services)** — `SafetyKernel.Check`,
  `OutputPolicyService.CheckOutput`, and other service definitions.
- **[Horizontal Scaling Guide](/operations/horizontal-scaling)** — Multi-replica
  deployment, all Redis lock keys, NATS subject matrix, and troubleshooting.

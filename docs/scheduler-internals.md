# Scheduler Internals

The Cordum scheduler is the central job routing and lifecycle engine. It receives
job requests over the message bus, evaluates them against the safety kernel,
routes them to worker pools, and manages state transitions through completion
(including output policy enforcement). This document covers the scheduler's
internal architecture—state machine, output policy integration, reconciliation,
saga/compensation, routing strategy, and circuit breaker.

> **See also**: [Pool Routing Config](SCHEDULER_POOL_SPEC.md) ·
> [Output Policy](output-policy.md) · [Safety Kernel](safety-kernel.md) ·
> [API Reference](api-reference.md)

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

---

## 8. Distributed Locking

The scheduler uses Redis-based distributed locks to ensure consistency:

| Lock Key                      | TTL           | Release          | Renewal     | Purpose                              |
|-------------------------------|---------------|------------------|-------------|--------------------------------------|
| `cordum:scheduler:job:<id>`   | 60s           | Explicit (defer) | Yes (ttl/3) | Per-job mutex for state transitions  |
| `cordum:reconciler:default`   | 2× poll interval | TTL expiry    | No          | Single-writer reconciler             |
| `cordum:replayer:pending`     | 2× poll interval | TTL expiry    | No          | Single-writer pending replayer       |
| `cordum:workflow-engine:reconciler:default` | 2× poll interval | TTL expiry | No | Single-writer workflow reconciler |
| `cordum:wf:run:lock:<runID>`  | 30s           | Explicit (defer) | No          | Per-run mutex for workflow steps     |
| `saga:<workflow_id>:lock`     | 2 min         | Explicit         | No          | Per-workflow saga rollback mutex     |

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

```
withJobLock("job-123", 60s, fn):
  ──acquire(60s)─┬──fn() runs────────────────────────┬──release──
                 │                                    │
  renewal:       ├──20s──renew──20s──renew──20s──renew│
                 │          (each resets TTL to 60s)  │
                 └────────────────────────────────────┘
                                                cancel → drain → release
```

---

## 9. Metrics

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

## 10. Source Files

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

- **[Pool Routing Config](SCHEDULER_POOL_SPEC.md)** — How topics map to pools
  and how `pools.yaml` is structured.
- **[Output Policy](output-policy.md)** — Output scanning rules, scanner
  configuration, and quarantine runbook.
- **[Safety Kernel](safety-kernel.md)** — Input policy evaluation, MCP filters,
  overlays, and the gRPC contract.
- **[API Reference](api-reference.md)** — REST endpoints for job submission,
  cancellation, and DLQ management.
- **[gRPC Services](grpc-services.md)** — `SafetyKernel.Check`,
  `OutputPolicyService.CheckOutput`, and other service definitions.

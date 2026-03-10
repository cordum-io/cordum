# Scheduler lifecycle bug hunt (2026-03-04)

## Scope
Task: `task-cf4b3c61` — scheduler lifecycle risks around stuck states, heartbeat/worker churn, and cancel/result ordering.

Code audited:
- `core/controlplane/scheduler/engine.go`
- `core/controlplane/scheduler/reconciler.go`
- `core/controlplane/scheduler/pending_replayer.go`
- `core/controlplane/scheduler/saga.go`
- `core/infra/store/job_store.go`
- `core/protocol/capsdk` subjects

## State transition matrix (source: code)

### Allowed transitions (`core/infra/store/job_store.go`)

| From | Allowed To |
|---|---|
| `` (unset/new) | PENDING, APPROVAL_REQUIRED, SCHEDULED, DISPATCHED, RUNNING, FAILED |
| PENDING | APPROVAL_REQUIRED, SCHEDULED, DISPATCHED, RUNNING, DENIED, FAILED, TIMEOUT |
| APPROVAL_REQUIRED | PENDING, SCHEDULED, DISPATCHED, RUNNING, DENIED, FAILED, TIMEOUT |
| SCHEDULED | DISPATCHED, RUNNING, DENIED, FAILED, TIMEOUT, SUCCEEDED, CANCELLED, QUARANTINED |
| DISPATCHED | RUNNING, SUCCEEDED, FAILED, CANCELLED, TIMEOUT, QUARANTINED |
| RUNNING | SUCCEEDED, FAILED, CANCELLED, TIMEOUT, QUARANTINED |
| SUCCEEDED | QUARANTINED (async output-policy downgrade) |
| FAILED/CANCELLED/TIMEOUT/DENIED/QUARANTINED | terminal |

### Runtime transition writers

- Submit path (`engine.handleJobRequest` / `processJob`):
  - Initial: `PENDING`
  - Safety decisions: `APPROVAL_REQUIRED` / `DENIED`
  - Dispatch pipeline: `SCHEDULED -> DISPATCHED -> RUNNING`
  - Terminal on scheduling exhaustion: `FAILED` + DLQ
- Result path (`engine.handleJobResult`):
  - SUCCEEDED/FAILED/TIMEOUT/DENIED/CANCELLED (+ output policy quarantine)
- Cancel path (`engine.HandlePacket` JobCancel + `jobStore.CancelJob`):
  - non-terminal -> `CANCELLED`
- Reconciler (`reconciler.handleTimeouts`, `handleDeadlineExpirations`):
  - stale SCHEDULED/DISPATCHED/RUNNING -> `TIMEOUT`
- Pending replayer (`pending_replayer`):
  - re-invokes submit handler for stale PENDING/APPROVAL/SCHEDULED jobs

## Redis keys / NATS subjects tied to lifecycle

- State + metadata:
  - `job:state:<id>`
  - `job:meta:<id>` (`state`, `attempts`, `deadline_unix`, etc.)
  - indexes: `job:index:state:*`, `job:deadline`, `job:recent`
- Locking:
  - per-job lock: `cordum:scheduler:job:<id>`
  - reconciler lock: `cordum:reconciler:default`
  - replayer lock: `cordum:replayer:pending`
- Bus subjects:
  - submit: `sys.job.submit`
  - result: `sys.job.result`
  - cancel: `sys.job.cancel`
  - DLQ: `sys.job.dlq`
  - heartbeat: `sys.heartbeat`

## Confirmed defects (code-backed)

### DEF-1: Scheduling retries can under-count after publish failure, delaying terminal fail/DLQ

**What happens:**
- Retry budget uses `attempts` from `job:meta:<id>`.
- `attempts` increments when entering `SCHEDULED`.
- On redelivery while still `SCHEDULED`, repeated scheduling attempts can be under-counted depending on state-update semantics.

**Impact:**
- Prolonged retry loops under persistent publish/bus failure.
- Jobs can stay non-terminal longer than configured retry policy intends.

**Paths:**
- `engine.processJob` publish failure branch
- `job_store.SetState(..., SCHEDULED)` attempt accounting

### DEF-2: `handleJobRequest` can proceed when state read fails, risking duplicate dispatch

**What happens:**
- Existing-state gate (skip when RUNNING/DISPATCHED/terminal) depends on `GetState`.
- If `GetState` fails with non-`redis.Nil`, submit path can continue and re-process the same job.

**Impact:**
- Duplicate publish/dispatch risk during Redis partial outages.
- Increases probability of cancel/result race amplification and duplicate worker execution.

**Paths:**
- `engine.handleJobRequest` current-state read gate

### DEF-3 (follow-up): lock-renewal abandonment can allow concurrent handlers after TTL expiry

**What happens:**
- `withJobLock` renewal goroutine stops after `maxRenewalFailures`, but main handler continues.
- If handler outlives lock TTL, another handler can acquire the same lock.

**Impact:**
- Concurrent processing of same job id under sustained Redis renewal failures.
- Elevated duplicate transition/publish race risk.

**Paths:**
- `engine.withJobLock` renewal loop + deferred unlock

## Failure-injection scenarios used

1. Publish-path persistent bus failure while job cycles through `SCHEDULED` retries.
2. Submit-path state-read fault injection (`GetState` non-`redis.Nil` error) with pre-existing RUNNING job.
3. (Design-only follow-up) renewal failure streak + long-running critical section to validate lock-split behavior.

## Remediation expectations

- Enforce strict accounting of scheduling attempts across all retry paths (especially SCHEDULED replays).
- Fail closed on non-`redis.Nil` state-read errors in submit path: return retryable error instead of continuing dispatch.
- Harden lock lifecycle:
  - either abort critical section on renewal abandonment, or
  - use fencing token checks on every state mutation.
- Preserve tenant isolation and DLQ metadata integrity in all failure paths.

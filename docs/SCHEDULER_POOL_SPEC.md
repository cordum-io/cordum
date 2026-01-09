# Scheduler and Pool Routing (current behavior)

This document describes how the scheduler routes jobs to pools, tracks state, and enforces policy/limits.

## Subjects and subscriptions

Scheduler subscribes to:
- `sys.job.submit` (queue `cordum-scheduler`)
- `sys.job.result` (queue `cordum-scheduler`)
- `sys.job.cancel` (queue `cordum-scheduler`)
- `sys.heartbeat` (fan-out)

## Topic -> pool config (`config/pools.yaml`)

Format:

```yaml
topics:
  job.default: default
pools:
  default:
    requires: []
```

Notes:
- Topics map to one or more pools (array accepted).
- Pools can declare `requires` capabilities.

## Requires-based routing

- Job requirements are taken from `JobMetadata.requires`.
- Pools are eligible only if they satisfy all required capabilities.
- If no pool satisfies the requirements, the scheduler returns `no_pool_mapping` and DLQs the job.

## Label hints

Job labels can include scheduling hints:
- `preferred_pool`: restricts routing to a pool if it is mapped for the topic.
- `preferred_worker_id`: routes directly to a specific worker if it is healthy and in an eligible pool.

Other labels are treated as placement constraints if they match worker labels.

## Worker selection

The least-loaded strategy scores candidates by:

```
score = active_jobs + cpu_load/100 + gpu_utilization/100
```

Workers over capacity (by `max_parallel_jobs` or high CPU/GPU) are skipped. If all are overloaded, the scheduler returns `pool_overloaded`.

## Safety and limits

- Safety Kernel is called before dispatch.
- Safety decisions and constraints are persisted in JobStore.
- `max_concurrent_jobs` (policy constraint) is enforced per tenant.
- `max_retries` (policy constraint) is enforced before dispatch.
- `budget.deadline_ms` is stored for per-job deadline enforcement.

## Job state tracking

States (JobStore):

```
PENDING -> SCHEDULED -> DISPATCHED -> RUNNING -> SUCCEEDED|FAILED|CANCELLED|TIMEOUT|DENIED
```

Additional behaviors:
- Per-job Redis lock prevents duplicate dispatch.
- Idempotency keys are deduped in JobStore.
- Reconciler marks stale `DISPATCHED`/`RUNNING` jobs as `TIMEOUT`.

## Reason codes

DLQ reason codes include:
- `no_pool_mapping`
- `no_workers`
- `pool_overloaded`
- `tenant_limit`
- `safety_denied`
- `max_retries_exceeded`

# Scheduler & Pool Routing (current behavior)

This describes how the control-plane scheduler routes jobs to pools, tracks state, and enforces timeouts.

## Subjects & Queue Groups
- Scheduler subscribes to:
  - `sys.job.submit` (queue `coretex-scheduler`)
  - `sys.job.result` (queue `coretex-scheduler`)
  - `sys.heartbeat` (fan-out; used to refresh registry)
- Workers subscribe to their `job.*` subject in a queue group (e.g., `workers-echo`, `workers-repo-scan`).

## Topic → Pool Map (`config/pools.yaml`)
- `job.echo` → `echo`
- `job.chat.simple` → `chat-simple`
- `job.chat.advanced` → `chat-advanced`
- `job.code.llm` → `code-llm`
- `job.workflow.plan` → `workflow-planner`
- `job.workflow.demo` → `workflow-demo`
- `job.workflow.repo.code_review` → `workflow-repo`
- `job.repo.scan` → `repo-scan`
- `job.repo.partition` → `repo-partition`
- `job.repo.lint` → `repo-lint`
- `job.repo.sast` → `repo-sast`
- `job.repo.tests` → `repo-tests`
- `job.repo.report` → `repo-report`

## Heartbeats & Registry
- Heartbeat fields used: `worker_id`, `pool`, `active_jobs`, `cpu_load`, `gpu_utilization`, `capabilities`, `max_parallel_jobs`.
- Latest heartbeat per worker is kept in-memory; registry snapshot is passed to the strategy.
- Registry evicts workers that stop heartbeating after ~30s, so workers must emit heartbeats every few seconds to remain eligible.

## Scheduling Strategy
- `LeastLoadedStrategy` computes a score per worker in the target pool:
  - `score = active_jobs + cpu_load/100 + gpu_utilization/100`
  - Lowest score wins; subject to pool membership.
- Scheduler publishes to a worker-specific subject (`worker.<id>.jobs`) when available so the selected worker receives the job. Workers still subscribe to the shared topic subject in their queue group as a fallback.

## Safety
- Scheduler calls the Safety Kernel gRPC `Check` before picking a subject.
- Decisions are stored in JobStore (`SetSafetyDecision`); denied jobs transition to `DENIED` and increment safety metrics.

## Job State Tracking (Redis JobStore)
- States: `PENDING → SCHEDULED → DISPATCHED → RUNNING → SUCCEEDED|FAILED|CANCELLED|TIMEOUT` plus `DENIED`.
- Transitions are validated; non-monotonic moves are rejected.
- Metadata: topic, tenant (from `JobRequest.env["tenant_id"]`), safety decision, result_ptr, trace membership.
- Indices: per-state sorted sets for reconciliation, `job:recent` for dashboards, and per-job event logs.

## Timeouts & Reconciliation (`config/timeouts.yaml`)
- Reconciler scans `DISPATCHED` and `RUNNING` jobs older than configured thresholds and marks them `TIMEOUT`.
- Default (compose): dispatch 120s, running 300s; scan every 30s.
- Workflow/topic-specific timeouts are also defined for orchestrators.

## Metrics
- Scheduler exports Prometheus metrics on `:9090/metrics`.
- Counters: jobs received/dispatched/completed (by topic/status), safety denies.

## Configuration (env)
- `POOL_CONFIG_PATH` (default `config/pools.yaml`)
- `TIMEOUT_CONFIG_PATH` (default `config/timeouts.yaml`)
- `SAFETY_KERNEL_ADDR`, `NATS_URL`, `REDIS_URL`

# Backend Feature Analysis (current state)

This document summarizes the production-bound backend surface, where it lives in code, and the status of each capability.

## Scheduler
- **Dispatch & safety**: NATS bus dispatch via least-loaded strategy with direct worker routing; safety circuit breaker with half-open logic.  
  Code: `core/controlplane/scheduler/*`.
- **Job metadata**: Redis job store with atomic state transitions (WATCH), tenant/team/principal/safety/meta, deadlines, trace linkage.  
  Code: `core/infra/memory/job_store.go`.
- **Reconciliation**: Timeouts for dispatched/running jobs with retry budget and lock to avoid double work.  
  Code: `core/controlplane/scheduler/reconciler.go`.
- **Config injection**: Effective config resolved via Redis config service and injected into job env (`CORETEX_EFFECTIVE_CONFIG`).  
  Code: `core/controlplane/scheduler/engine.go`, `core/configsvc`.

## Workflow Engine
- **Store**: Redis-backed workflows/runs.  
  Code: `core/workflow/store_redis.go`.
- **Execution**: DAG dispatch with condition gating, `for_each` fan-out, per-step retries/backoff, timeout budget hints, and approval pause/resume.  
  Code: `core/workflow/engine.go`, tests in `core/workflow/engine_test.go`.
- **Identity**: Run carries org/team; job env includes workflow/run/step, tenant/team, foreach metadata, and effective config when available.

## Config Service
- **Hierarchy**: System → Org → Team → Workflow → Step shallow merge.  
  Code: `core/configsvc`.
- **Exposure**: Gateway endpoints for set/get/effective config.  
  Code: `core/controlplane/gateway/`.

## API Gateway
- **Jobs**: Submit/list/get/cancel with filters (state/topic/tenant/team/time).  
  Code: handlers in `core/controlplane/gateway/`.
- **Workflows**: CRUD, start runs, approval endpoint to resume waiting steps.  
  Code: `core/controlplane/gateway/` (workflow handlers).
- **DLQ**: List/delete/retry (re-dispatches job with prior context pointer).  
  Code: `core/controlplane/gateway/`, `core/infra/memory/dlq_store.go`.

## DLQ Store
- **Persistence**: Redis entries with index, get/list/delete, tested.  
  Code: `core/infra/memory/dlq_store.go`.

## Next Focus
- Routing hints in workflow dispatch, per-step cancellation, richer DLQ retry (context replay), server-side pagination/filters, and optional vector/artifact/secret bindings when steps require them.

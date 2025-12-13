# coretexOS Backend Capabilities (Redis + NATS)

This document tracks the current backend features, their status, and where they are exercised. It is code-accurate as of this commit.

## Runtime Stack (active)
- Language: Go
- Bus: NATS (core subjects), DLQ emitter
- State: Redis for job state, contexts/results, workflows/runs, config, DLQ
- Vector/Artifacts/Secrets: not yet wired (planned when steps need embeddings/artifacts)

## Components & Features

### Scheduler
- Dispatch: NATS publish of `sys.job.submit` to worker topics or direct subjects (least-loaded strategy).
- States: Redis job store (atomic via WATCH), deadlines, trace linkage.
- Safety: Safety client with circuit breaker; decisions persisted.
- Reconciler: timeout scans for dispatched/running; lock-based to avoid double processing.
- DLQ: emits to `sys.job.dlq` on failures.
- Hints & cancel: respects preferred worker/pool hints via labels; broadcasts job cancel packets to `sys.job.cancel` and job topics (best-effort).

### API Gateway
- Jobs: submit/list/get/cancel, trace fetch, repo-review helper.
- Workflows: REST CRUD (`/api/v1/workflows`), runs start/get/list, approve step, cancel run; dispatches runs via workflow engine.
- Config: Redis-backed config service (`/api/v1/config` set, `/api/v1/config/effective` get).
- Stream: WS stream of bus packets; worker snapshots.
- DLQ: list/delete/get; retry rehydrates original context into a new job id and re-dispatches.

### Workflow Engine (new)
- Storage: Redis workflows and runs (`core/workflow`), tested.
- Execution: starts runs, dispatches ready steps as jobs (job ID = runID:stepID), consumes job results to advance run state.
- Fan-out: `for_each` expression evaluated against run input/context; child jobs dispatched with index/item metadata; parent aggregated.
- Reliability: per-step retry/backoff (exponential), budget deadline hint from `timeout_sec`, approval steps pause/resume via API; tests cover fan-out/retry/approval.
- Hooks: callbacks on step dispatch/finish for observability.
- Routing: route labels/worker_id propagated to job labels for scheduler hints; cancel guard prevents further dispatch after cancel.

### Config Service (new)
- Redis-backed hierarchical merge (system→org→team→workflow→step) with shallow overrides.
- REST endpoints exposed via gateway.

### DLQ (new)
- Redis-backed DLQ store with add/list/delete/get; retry endpoint in gateway rehydrates context and re-dispatches under a new job id.

### Workers
- Existing Go workers (chat/code/repo) listening on NATS topics; now subscribe to `sys.job.cancel` and honor cancel requests via context cancellation.

## Pending/Next (to align with plan)
- Workflow engine: routing hints to scheduler (preferred worker/pool), per-step cancellation hooks, artifacts/secrets when steps require them.
- Config: extend safety to accept effective config payloads.
- DLQ ops: add tracing/context replay telemetry and pagination.
- Ops filters: server-side pagination/cursors for jobs/traces and richer search.
- Optional: vector store bindings, artifacts (S3), secrets (Vault) when step types require them.

## Key Paths
- Workflow store/engine: `core/workflow/`
- Config service: `core/configsvc/`
- DLQ store: `core/infra/memory/dlq_store.go`
- Gateway wiring: `cmd/coretex-api-gateway/main.go`
- Scheduler/job store: `core/controlplane/scheduler/`, `core/infra/memory/job_store.go`

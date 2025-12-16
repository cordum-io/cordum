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
- Safety: Safety client with half-open circuit breaker; decisions persisted (includes effective_config payload).
- Registry: in-memory with TTL expiry loop to drop dead workers.
- Reconciler: timeout scans for dispatched/running; bounded retries + lock-based to avoid double processing.
- DLQ: emits to `sys.job.dlq` on failures.
- Hints & cancel: respects preferred worker/pool hints via labels; broadcasts job cancel packets to `sys.job.cancel` (best-effort).

### API Gateway
- Jobs: submit/list/get/cancel, trace fetch, repo-review helper; list supports filters (state/topic/tenant/team/time/trace) and cursor pagination (`cursor`/`next_cursor`).
- Workflows: REST CRUD (`/api/v1/workflows`), runs start/get/list, approve step, cancel run; creates runs and kicks off execution via workflow engine.
- Config: Redis-backed config service (`/api/v1/config` set, `/api/v1/config/effective` get).
- Stream: WS stream of bus packets; worker snapshots.
- DLQ: list/delete/get; retry rehydrates original context into a new job id and re-dispatches.

### Workflow Engine Service (new)
- Control plane: `cmd/coretex-workflow-engine` subscribes to `sys.job.result` (queue group) and advances runs independently from the gateway.
- Storage: Redis workflows and runs (`core/workflow`), status indexes for reconciliation.
- Execution: starts runs, dispatches ready steps as jobs (job ID = runID:stepID@attempt), consumes job results to advance run state.
- Fan-out: `for_each` expression evaluated against run input/context; child jobs dispatched with index/item metadata; parent aggregated.
- Dataflow: step `input` supports `${...}` expressions; step outputs are recorded in run context under `steps.<step_id>` and optionally `output_path`.
- Reliability: per-step retry/backoff (exponential), budget deadline hint from `timeout_sec`, approval steps pause/resume via API; reconciler replays terminal job states from JobStore and resumes delayed retries; tests cover fan-out/retry/approval/max_parallel.
- Hooks: callbacks on step dispatch/finish for observability.
- Routing: route labels/worker_id propagated to job labels for scheduler hints; cancel guard prevents further dispatch after cancel.

### Config Service (new)
- Redis-backed hierarchical merge (system→org→team→workflow→step) with shallow overrides.
- REST endpoints exposed via gateway.

### DLQ (new)
- Redis-backed DLQ store with add/list/delete/get; retry endpoint in gateway rehydrates context and re-dispatches under a new job id.

### Workers
- Go workers built on `core/agent/runtime` subscribe to `sys.job.cancel` and honor cancel requests via context cancellation; chat/code/repo worker implementations use this wrapper.

## Pending/Next (to align with plan)
- Workflow engine: richer routing hints to scheduler (per-step worker prefs), per-step cancellation hooks, artifacts/secrets when steps require them.
- DLQ ops: add tracing/context replay telemetry and pagination.
- Ops filters: server-side trace search and richer analytics/alerts.
- Optional: vector store bindings, artifacts (S3), secrets (Vault) when step types require them.

## Key Paths
- Workflow store/engine: `core/workflow/`
- Config service: `core/configsvc/`
- DLQ store: `core/infra/memory/dlq_store.go`
- Gateway server/handlers: `core/controlplane/gateway/` (thin binary: `cmd/coretex-api-gateway/main.go`)
- Safety kernel server: `core/controlplane/safetykernel/` (thin binary: `cmd/coretex-safety-kernel/main.go`)
- Scheduler/job store: `core/controlplane/scheduler/`, `core/infra/memory/job_store.go`

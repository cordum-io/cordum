# Cordum Backend Capabilities (Redis + NATS)

This document tracks the current backend features, their status, and where they are exercised. It is code-accurate as of this commit.

## Runtime Stack (active)
- Language: Go
- Bus: NATS with optional JetStream durability (`NATS_USE_JETSTREAM=1`), DLQ emitter
- State: Redis for job state, contexts/results, workflows/runs, config, DLQ
- Artifacts: Redis-backed store with retention classes
- Schemas: JSON schema registry + validation
- Locks: Redis-backed shared/exclusive locks
- Secrets: `secret://` reference detection and redaction helpers (policy enforcement via risk tags/labels)

## Components & Features

### Scheduler
- Dispatch: consumes `sys.job.submit`, routes to worker topics or direct subjects (least-loaded strategy).
- States: Redis job store (atomic via WATCH), deadlines, trace linkage.
- Safety: Safety client with half-open circuit breaker; decisions persisted (includes effective_config payload).
- Registry: in-memory with TTL expiry loop to drop dead workers.
- Reliability: JetStream mode uses explicit ack/nak on durable subjects; scheduler is idempotent under redelivery (per-job lock + retryable errors).
- Reconciler: timeout scans for dispatched/running; bounded retries + lock-based to avoid double processing.
- Pending replayer: replays PENDING jobs past dispatch timeout to avoid stuck jobs.
- DLQ: emits to `sys.job.dlq` on failures.
- Hints & cancel: respects preferred worker/pool hints via labels; broadcasts job cancel packets to `sys.job.cancel` (best-effort).

### API Gateway
- Jobs: submit/list/get/cancel, trace fetch; list supports filters (state/topic/tenant/team/time/trace) and cursor pagination (`cursor`/`next_cursor`).
- Workflows: create/upsert (`/api/v1/workflows`), list/get, runs start/get/list, approve step, cancel run, rerun, timeline.
- Approvals: job approvals (`/api/v1/approvals/...`) and step approvals (`/api/v1/workflows/.../steps/.../approve`).
- Config: Redis-backed config service (set/get via `/api/v1/config`, effective via `/api/v1/config/effective`).
- Policy: evaluate/simulate/explain + snapshots, bundle list/detail/update, bundle snapshots, publish/rollback, audit (admin role enforced when enterprise RBAC is enabled).
- Schemas: register/get/list/delete JSON schemas.
- Locks: acquire/release/renew/get shared/exclusive locks.
- Artifacts: put/get with retention class metadata.
- Packs: install/list/show/verify/uninstall; pack registry stored in config service.
- Marketplace: list catalogs + install by catalog/URL (`cfg:system:pack_catalogs`).
- Stream: WS stream of bus packets (includes heartbeats and job events).
- Memory: pointer reader (`GET /api/v1/memory?ptr=...`) used by admins or UI clients to inspect `redis://ctx:*`, `redis://res:*`, and `redis://mem:*` keys.
- Security: CORS/WS origin allowlist (set `CORDUM_ALLOWED_ORIGINS` for non-local browser clients).
- DLQ: list/delete/retry; retry rehydrates original context into a new job id and re-dispatches.

### Workflow Engine Service
- Control plane: `cmd/cordum-workflow-engine` (binary `cordum-workflow-engine`) subscribes to `sys.job.result` (queue group) and advances runs independently from the gateway.
- Storage: Redis workflows and runs (`core/workflow`), status indexes for reconciliation.
- Execution: starts runs, dispatches ready steps as jobs (job ID = runID:stepID@attempt), consumes job results to advance run state.
- Fan-out: `for_each` expression evaluated against run input/context; child jobs dispatched with index/item metadata; parent aggregated.
- DAG deps: `depends_on` allows parallel independent steps; a step runs only after all dependencies succeed.
- Failure semantics: failed/cancelled/timed-out deps block downstream steps (no implicit continue-on-error).
- Dataflow: step `input` supports `${...}` expressions; step outputs are recorded in run context under `steps.<step_id>` and optionally `output_path`.
- Step types: approval, delay (timer), notify (SystemAlert), condition (inline boolean output), worker.
- Reliability: per-step retry/backoff (exponential), budget deadline hint from `timeout_sec`, approval steps pause/resume via API; job-result handling returns retryable errors (NAK) under transient store/lock failures so results aren’t dropped; reconciler replays terminal job states from JobStore and resumes delayed retries; tests cover fan-out/retry/approval/max_parallel.
- Rerun: rerun-from-step and dry-run support.
- Validation: workflow input schema validation; step input/output schema validation.
- Audit: append-only timeline for run events.
- Hooks: callbacks on step dispatch/finish for observability.
- Routing: route labels/worker_id propagated to job labels for scheduler hints; cancel guard prevents further dispatch after cancel.

### Config Service
- Redis-backed hierarchical merge (system→org→team→workflow→step) with shallow overrides.
- Effective snapshot includes version and hash.
- REST endpoints exposed via gateway.

### DLQ
- Redis-backed DLQ store with add/list/delete/get; gateway exposes list/delete/retry and re-dispatches under a new job id.

### Workers
- External workers (not in this repo) subscribe to `sys.job.cancel` and honor cancel requests.
- CAP worker runtime SDK is implemented at `sdk/runtime` (heartbeats, progress, cancel handling).

## Pending/Next (to align with plan)
- DLQ ops: add pagination and richer telemetry.
- Ops filters: server-side trace search and richer analytics/alerts.
- Optional: vector store bindings, artifacts (S3), secrets (Vault/KMS) when step types require them.
- CAP signatures: enable signing/verification on the bus when needed.

## Key Paths
- Workflow store/engine: `core/workflow/`
- Config service: `core/configsvc/`
- DLQ store: `core/infra/memory/dlq_store.go`
- Gateway server/handlers: `core/controlplane/gateway/` (thin binary: `cmd/cordum-api-gateway/main.go`, ships as `cordum-api-gateway`)
- Safety kernel server: `core/controlplane/safetykernel/` (thin binary: `cmd/cordum-safety-kernel/main.go`, ships as `cordum-safety-kernel`)
- Scheduler/job store: `core/controlplane/scheduler/`, `core/infra/memory/job_store.go`

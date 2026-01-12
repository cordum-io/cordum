# Cordum System Overview (current code)

This document describes the current architecture and runtime behavior of Cordum as implemented
in this repository. It is intended to be code-accurate and is the primary reference for how the
control plane and external workers interact.

## High-level architecture

```
Clients/UI
  |
  v
API Gateway (HTTP/WS + gRPC)
  | writes ctx/res/artifact pointers
  v
Redis (ctx/res/artifacts, job meta, workflows, config, DLQ, schemas, locks)
  |
  v
NATS bus (sys.* + job.* + worker.<id>.jobs)
  |
  +--> Scheduler (safety gate + routing + job state)
  |       |
  |       +--> Safety Kernel (gRPC policy check)
  |
  +--> External Workers (user-provided)
  |
  +--> Workflow Engine (run orchestration)
```

## Core components

- API gateway (`core/controlplane/gateway`, `cmd/cordum-api-gateway`; binary `cordum-api-gateway`)
  - HTTP/WS endpoints for jobs, workflows/runs, approvals, config, policy (bundles + publish/rollback/audit), DLQ, schemas, locks, artifacts, workers, traces, packs.
  - gRPC service (`CordumApi`) for job submit/status.
  - Streams `BusPacket` events over `/api/v1/stream` (protojson).
  - Enforces API key and CORS allowlist if configured (HTTP `X-API-Key`, gRPC metadata `x-api-key`, WS `Sec-WebSocket-Protocol: cordum-api-key, <base64url>`).
  - OSS auth uses a flat API key allowlist (`CORDUM_API_KEYS` or `CORDUM_API_KEY`) and a single tenant (`TENANT_ID`, default `default`).
  - Multi-tenant API keys and RBAC enforcement are provided by the enterprise auth provider (enterprise repo).
  - Enterprise add-ons are delivered from the enterprise repo; this repo stays platform-only.

- Dashboard (`dashboard/`)
  - React UI served via Nginx; connects to `/api/v1` and `/api/v1/stream`.
  - Runtime config via `/config.json` (API base URL, API key, optional tenant/principal for enterprise auth).

- Scheduler (`core/controlplane/scheduler`, `cmd/cordum-scheduler`; binary `cordum-scheduler`)
  - Subscribes to `sys.job.submit`, `sys.job.result`, `sys.job.cancel`, `sys.heartbeat`.
  - Calls Safety Kernel before dispatch (allow/deny/approve/throttle/constraints).
  - Routes jobs using pool mapping + least-loaded strategy, labels, and requires-based pool eligibility.
  - Persists job state in Redis and emits DLQ for non-success results.
  - Reconciler marks stale `DISPATCHED`/`RUNNING` jobs as `TIMEOUT`.
  - Pending replayer retries `PENDING` jobs past the dispatch timeout to avoid stuck runs.

- Safety Kernel (`core/controlplane/safetykernel`, `cmd/cordum-safety-kernel`; binary `cordum-safety-kernel`)
  - gRPC `Check`, `Evaluate`, `Explain`, `Simulate`; uses `config/safety.yaml`.
  - Deny/allow by tenant/topic, plus MCP allow/deny lists and constraints.
  - Loads policy bundles from file/URL plus config-service fragments (supports bundle `enabled=false`), with snapshot hashing and hot reload.
  - Applies effective config embedded in job env.
  - Returns optional remediations; gateway can apply them to create a new job with safer topic/capability/labels.
  - Optional decision cache (`SAFETY_DECISION_CACHE_TTL`) keeps latency low for repeated checks.

- Workflow Engine (`core/workflow`, `core/controlplane/workflowengine`, `cmd/cordum-workflow-engine`; binary `cordum-workflow-engine`)
  - Stores workflow definitions and runs in Redis; maintains run timeline.
  - Dispatches ready steps as jobs (`sys.job.submit`).
  - Supports condition, delay, notify, for_each fan-out, retries/backoff, approvals, run cancel.
  - `depends_on` enables DAG execution: independent steps run in parallel; steps wait for all deps to succeed.
  - Failed/cancelled/timed-out deps block downstream steps (no implicit continue-on-error).
  - Supports rerun-from-step and dry-run mode.
  - Validates workflow input and step input/output schemas.
  - Subscribes to `sys.job.result` to advance runs; reconciler retries stuck runs.

- Context Engine (`core/context/engine`, `cmd/cordum-context-engine`; binary `cordum-context-engine`)
  - gRPC service for `BuildWindow` and `UpdateMemory`.
  - Maintains chat history and generic memory under `mem:<memory_id>:*`.

- External workers (not in this repo)
  - Subscribe to job topics or direct subjects; honor `sys.job.cancel`.
  - Write results to Redis and publish `sys.job.result`.
  - Use the CAP runtime in `sdk/runtime` for consistent heartbeats/progress/cancel.

## Job lifecycle (single job)

1) Client or gateway writes input JSON to Redis at `ctx:<job_id>`.
2) Publish `BusPacket{JobRequest}` to `sys.job.submit` with `context_ptr`.
3) Scheduler:
   - Sets job state `PENDING`, resolves effective config, runs safety check.
   - Picks a subject (`worker.<id>.jobs` or `job.*`) and dispatches.
   - Pending replayer replays old `PENDING` jobs past the dispatch timeout.
   - If approval is required, state becomes `APPROVAL_REQUIRED`; approvals are bound to the policy snapshot + job hash before requeueing.
   - If remediations are returned, the gateway can apply one via `POST /api/v1/jobs/{id}/remediate` (creates a new job).
4) Worker:
   - Loads context from `context_ptr`, runs work, writes `res:<job_id>`.
   - Publishes `BusPacket{JobResult}` to `sys.job.result`.
5) Scheduler:
   - Updates terminal state and stores `result_ptr`.
   - Emits DLQ entry if status != `SUCCEEDED`.
6) Reconciler marks stale jobs `TIMEOUT` based on `config/timeouts.yaml`.
7) Cancellation: API or workflow engine publishes `BusPacket{JobCancel}` to `sys.job.cancel`; workers cancel in-flight jobs.

## Workflow runs

- Workflows are defined in Redis (`core/workflow`).
- A run is created via `/api/v1/workflows/{id}/runs`.
- Steps are dispatched as jobs using job IDs `run_id:step_id@attempt`.
- Step input supports simple expressions (`core/workflow/eval.go`) and template expansion.
- for_each steps fan out child jobs with `foreach_index` and `foreach_item` env fields.
- Approval steps pause the run until `/approve` is called.
- Runs and workflows can be deleted via `DELETE /api/v1/workflow-runs/{id}` and `DELETE /api/v1/workflows/{id}`.
- Runs support idempotency keys via `Idempotency-Key` header on run creation.

## Protocols

- Bus and safety messages are CAP v2 types (no local duplicates):
  - `BusPacket`, `JobRequest`, `JobResult`, `Heartbeat`, `PolicyCheck*`
  - See `github.com/cordum-io/cap/v2/cordum/agent/v1`.
- Local gRPC APIs:
  - `CordumApi` (submit job, get status) in `core/protocol/proto/v1/api.proto` (gRPC service name).
  - `ContextEngine` in `core/protocol/proto/v1/context.proto`
- Generated Go types live in `core/protocol/pb/v1` and `sdk/gen/go/cordum/v1`, exposed via the `sdk` module.

## Bus subjects and delivery

Subjects:
- `sys.job.submit`, `sys.job.result`, `sys.job.progress`, `sys.job.dlq`, `sys.job.cancel`, `sys.heartbeat`, `sys.workflow.event`
- `job.*` pool subjects
- `worker.<id>.jobs` direct worker subjects

JetStream (optional):
- Enable with `NATS_USE_JETSTREAM=1`.
- Durable subjects: `sys.job.submit`, `sys.job.result`, `sys.job.dlq`, `job.*`, `worker.<id>.jobs`.
- Best-effort: `sys.heartbeat`, `sys.job.cancel`.
- Handlers are idempotent via Redis locks and retryable error NAKs.

## Redis key map (selected)

- Context/result:
  - `ctx:<job_id>` -> input payload
  - `res:<job_id>` -> result payload
  - `art:<id>` -> artifact payload
- Job store:
  - `job:meta:<job_id>` (state + metadata)
  - `job:state:<job_id>` (state)
  - `job:recent` (sorted set)
  - `job:index:<state>` (sorted sets for reconciliation)
  - `job:deadline` (sorted set of deadlines)
  - `job:events:<job_id>` (state transition log)
  - `trace:<trace_id>` (set of job ids)
- Context engine:
  - `mem:<memory_id>:events`, `mem:<memory_id>:chunks`, `mem:<memory_id>:summary`
- Workflow engine:
  - `wf:def:<workflow_id>` (definitions)
  - `wf:run:<run_id>` plus run indexes (`wf:runs:*`)
  - `wf:run:timeline:<run_id>` (append-only timeline)
  - `wf:run:idempotency:<key>` (idempotency mapping)
- DLQ:
  - `dlq:entry:<job_id>`, `dlq:index`
- Config service:
  - `cfg:<scope>:<id>`
  - `cfg:system:policy` (policy fragments bundle)
  - `cfg:system:packs` (installed pack registry)
- Schema registry:
  - `schema:<id>`, `schema:index`
- Locks:
  - `lock:<key>` (plus owner/ttl metadata)

## Binaries (cmd)

- `cordum-api-gateway`
- `cordum-scheduler`
- `cordum-safety-kernel`
- `cordum-workflow-engine`
- `cordum-context-engine`
- `cordumctl` (CLI)

## Repo layout

- `core/` control plane, infra, protocols, workflow engine.
- `cmd/` platform binaries.

## Topics -> pools

See `config/pools.yaml` for the full map. Topics are config-driven; no core topics are enforced.

## Observability

- Scheduler metrics: `:9090/metrics`
- API gateway metrics: `:9092/metrics`
- Workflow engine health: `:9093/health`

## Testing

- Run `go test ./...` (use `GOCACHE=$(pwd)/.cache/go-build` if needed).
- If modifying `.proto`, run `make proto`.
- Platform smoke: `./tools/scripts/platform_smoke.sh`.

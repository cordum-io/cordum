# Cordum Agent Protocol (NATS + Redis pointers)

This document describes how control-plane components and external workers communicate on the bus, what goes into `context_ptr` / `result_ptr`, and how job state is tracked.

## Actors
- **API Gateway:** writes context to Redis, publishes `BusPacket{JobRequest}` to `sys.job.submit`, exposes HTTP/WS/gRPC, and streams bus events.
- **Scheduler:** subscribes to `sys.job.submit`, `sys.job.result`, `sys.heartbeat`; gates with Safety Kernel, selects a pool/worker subject, publishes to `job.*` (pool) or `worker.<id>.jobs` (direct), and persists job state/result in `JobStore`.
- **Safety Kernel:** gRPC `Check` service; allows/denies topics per tenant (see `config/safety.yaml`).
- **Workflow Engine:** creates runs, publishes job steps to `sys.job.submit`, and advances runs based on results.
- **External Workers:** subscribe to `job.*` subjects in queue groups, fetch context/result from Redis pointers, emit `BusPacket{JobResult}` to `sys.job.result`, and send heartbeats.
- **Context Engine:** gRPC helper that builds context windows and maintains memory in Redis (not on the NATS bus).

## Bus Subjects
- `sys.job.submit` – inbound jobs to the scheduler.
- `sys.job.result` – job completions from workers.
- `sys.job.progress` – progress updates from workers.
- `sys.job.dlq` – dead-letter events (non-success results; used for debugging/retry workflows).
- `sys.job.cancel` – cancellation notifications (workers cancel matching in-flight job IDs).
- `sys.heartbeat` – worker heartbeats (fan-out, no queue group).
- `sys.workflow.event` – workflow engine event emissions (SystemAlert).
- `job.*` – worker pools (map lives in `config/pools.yaml`, e.g., `job.default`, `job.batch`).
- `worker.<worker_id>.jobs` – direct, worker-targeted delivery (used by the scheduler for least-loaded dispatch).
Default subject constants are defined in `core/protocol/capsdk` (mirrors CAP v2.0.9 spec for Go).

## Delivery Semantics (JetStream)

By default this system is plain NATS pub/sub (at-most-once). When JetStream is enabled (`NATS_USE_JETSTREAM=1`), the bus switches the durable subjects to explicit ack/nak semantics (at-least-once):

- **Durable (JetStream):** `sys.job.submit`, `sys.job.result`, `sys.job.dlq`, `job.*`, `worker.<id>.jobs`
- **Best-effort (plain NATS):** `sys.heartbeat` (fan-out), `sys.job.cancel` (best-effort cancellation)

Because at-least-once delivery can redeliver, handlers must be idempotent:
- Scheduler uses a per-job Redis lock before mutating state/dispatching.
- Workers should use a per-job lock and cache the published `JobResult` metadata so a redelivery can republish without re-running work.
- Retryable handler errors are returned as “retry after …” and translated into a NAK-with-delay; non-retryable errors are ACKed (won’t redeliver).

## Wire Contracts (CAP – `github.com/cordum-io/cap/v2/cordum/agent/v1`)
CAP is the canonical contract; Cordum does not duplicate these protos.
- **Envelope: `BusPacket`**
  - `trace_id`, `sender_id`, `created_at`, `protocol_version` (current: `1`)
  - `payload` oneof: `JobRequest`, `JobResult`, `Heartbeat`, `SystemAlert`, `JobProgress`, `JobCancel`.
  - `signature` is part of CAP but not enforced by Cordum yet.
- **JobRequest**
  - `job_id` (UUID string), `topic` (e.g., `job.default`), `priority` (`INTERACTIVE|BATCH|CRITICAL`).
  - `context_ptr` (Redis URL, e.g., `redis://ctx:<job_id>`). `result_ptr` is carried on `JobResult`.
  - `memory_id` (long-lived memory namespace), `tenant_id`, `principal_id`, `labels` (routing + observability).
  - `adapter_id` (optional worker mode), `env` map (tenant fallback), workflow metadata (e.g. `parent_job_id`, `workflow_id`), plus `context_hints` and `budget` (token + deadline hints).

Priority semantics:
- The scheduler treats `priority` as metadata only (no preemption or queue ordering today).
- Workers may choose to use it for local ordering, but core does not enforce it.
- **JobResult**
  - `job_id`, `status` (`PENDING|SCHEDULED|DISPATCHED|RUNNING|SUCCEEDED|FAILED|CANCELLED|DENIED|TIMEOUT`), `result_ptr`, `worker_id`, `execution_ms`, optional `error_code`/`error_message`.
- **JobProgress**
  - `job_id`, `percent`, `message`, optional `result_ptr`/`artifact_ptrs`, optional status hint.
- **JobCancel**
  - `job_id`, `reason`, `requested_by`.
- **Heartbeat**
  - `worker_id`, `region`, `type`, `cpu_load`, `gpu_utilization`, `active_jobs`, `capabilities`, `pool`, `max_parallel_jobs`.
- **PolicyCheckResponse**
  - `decision`, `reason`, `policy_snapshot`, `constraints`, `approval_required`, `approval_ref`.
  - `remediations` (optional suggestions with `replacement_topic`, `replacement_capability`, label add/remove).

## Pointer Scheme (Redis)
- Contexts live at `ctx:<job_id>` (or a derived key) with pointer `redis://ctx:<job_id>`.
- Results live at `res:<job_id>` with pointer `redis://res:<job_id>`.
- Artifacts live at `art:<id>` with pointer `redis://art:<id>`.
- Job metadata/state lives under `job:meta:<job_id>`; per-state indices are maintained for reconciliation; recent jobs are kept in `job:recent`.
- Context-engine memory is namespaced under `mem:<memory_id>:*` (e.g., `mem:<memory_id>:events`, `mem:<memory_id>:summary`).
- Scheduler writes a worker snapshot JSON to `sys:workers:snapshot` for observability and control-plane consumers.
- Gateway exposes a pointer reader for debugging/UI: `GET /api/v1/memory?ptr=<urlencoded redis://...>`.

## Lifecycle
1. Client (gateway or script) writes context JSON to Redis and sets `context_ptr` in `JobRequest`.
2. Publish `BusPacket{JobRequest}` to `sys.job.submit`.
3. Scheduler:
   - Records state `PENDING` in JobStore and adds job to trace.
   - Calls Safety Kernel; on deny → state `DENIED`.
   - Uses pool map + `LeastLoadedStrategy` to choose a subject (`worker.<id>.jobs` when possible; otherwise `job.*`); publishes job and moves state to `SCHEDULED → DISPATCHED → RUNNING`.
4. Worker consumes `job.*` or `worker.<id>.jobs`, fetches `context_ptr`, performs work, writes result to `res:<job_id>`, and publishes `BusPacket{JobResult}` with `result_ptr`.
5. Scheduler updates JobStore with terminal state from `JobResult` and stores `result_ptr`.
6. Reconciler periodically marks old `DISPATCHED`/`RUNNING` jobs as `TIMEOUT` based on `config/timeouts.yaml`.
7. Cancellation: gateway or scheduler publishes `BusPacket{JobCancel}` to `sys.job.cancel`; workers cancel the matching in-flight job context and publish a terminal `JobResult` (`CANCELLED` or `TIMEOUT`).

## Safety & Tenancy
- Safety policy file (`config/safety.yaml`) provides per-tenant `allow_topics` / `deny_topics`.
- Gateway sets `JobRequest.tenant_id` and also includes an `env["tenant_id"]` fallback; scheduler writes decision/reason into JobStore for observability.
- MCP calls should set `JobRequest.labels` (`mcp.server`, `mcp.tool`, `mcp.resource`, `mcp.action`) so the Safety Kernel can enforce MCP allow/deny rules.
- Jobs may include `JobMetadata` (`capability`, `risk_tags`, `requires`, `pack_id`) for policy and routing enforcement.
- Safety decisions may include `remediations`; the gateway can apply one via `POST /api/v1/jobs/{id}/remediate` to create a new job with safer topic/capability/labels.

## Context Engine (non-bus)
- gRPC service `ContextEngine` (`cmd/cordum-context-engine`, binary `cordum-context-engine`) with RPCs:
  - `BuildWindow(memory_id, mode, logical_payload, max_input_tokens, max_output_tokens)` → list of `ModelMessage`.
  - `UpdateMemory(memory_id, logical_payload, model_response, mode)` → appends chat history or summaries.
- Uses the same Redis instance; keys are namespaced under `mem:<memory_id>:*`.

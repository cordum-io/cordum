# coretexOS Agent Protocol (NATS + Redis pointers)

This document describes how control-plane components and workers communicate on the bus, what goes into `context_ptr` / `result_ptr`, and how job state is tracked.

## Actors
- **API Gateway:** writes context to Redis, publishes `BusPacket{JobRequest}` to `sys.job.submit`, exposes HTTP/WS/gRPC, and streams bus events.
- **Scheduler:** subscribes to `sys.job.submit`, `sys.job.result`, `sys.heartbeat`; gates with Safety Kernel, selects a pool subject, publishes to `job.*`, and persists job state/result in `JobStore`.
- **Safety Kernel:** gRPC `Check` service; allows/denies topics per tenant (see `config/safety.yaml`).
- **Workers / Orchestrators:** subscribe to `job.*` subjects in queue groups, fetch context/result from Redis pointers, emit `BusPacket{JobResult}` to `sys.job.result`, and send heartbeats.
- **Context Engine:** gRPC helper that builds chat/RAG windows and maintains memory in Redis (not on the NATS bus).

## Bus Subjects
- `sys.job.submit` – inbound jobs to the scheduler.
- `sys.job.result` – job completions from workers.
- `sys.job.cancel` – cancellation notifications (workers cancel matching in-flight job IDs).
- `sys.heartbeat` – worker heartbeats (fan-out, no queue group).
- `job.*` – worker pools (map lives in `config/pools.yaml`, e.g., `job.echo`, `job.repo.scan`, `job.workflow.repo.code_review`).

## Wire Contracts (CAP – `github.com/coretexos/cap/v2/go/coretex/agent/v1`)
- **Envelope: `BusPacket`**
  - `trace_id`, `sender_id`, `created_at`, `protocol_version` (current: `1`)
  - `payload` oneof: `JobRequest`, `JobResult`, `Heartbeat`, `SystemAlert`.
- **JobRequest**
  - `job_id` (UUID string), `topic` (e.g., `job.repo.scan`), `priority` (`INTERACTIVE|BATCH|CRITICAL`).
  - `context_ptr` (Redis URL, e.g., `redis://ctx:<job_id>`), `result_ptr` is set by workers.
  - `adapter_id` (optional worker mode), `env` map (scheduler uses `tenant_id`), workflow metadata: `parent_job_id`, `workflow_id`, `step_index`.
- **JobResult**
  - `job_id`, `status` (`PENDING|SCHEDULED|DISPATCHED|RUNNING|SUCCEEDED|FAILED|CANCELLED|DENIED|TIMEOUT`), `result_ptr`, `worker_id`, `execution_ms`, optional `error_code`/`error_message`.
- **Heartbeat**
  - `worker_id`, `region`, `type`, `cpu_load`, `gpu_utilization`, `active_jobs`, `capabilities`, `pool`, `max_parallel_jobs`.

## Pointer Scheme (Redis)
- Contexts live at `ctx:<job_id>` (or a derived key) with pointer `redis://ctx:<job_id>`.
- Results live at `res:<job_id>` with pointer `redis://res:<job_id>`.
- Job metadata/state lives under `job:meta:<job_id>`; per-state indices are maintained for reconciliation; recent jobs are kept in `job:recent`.

## Lifecycle
1. Client (gateway or script) writes context JSON to Redis and sets `context_ptr` in `JobRequest`.
2. Publish `BusPacket{JobRequest}` to `sys.job.submit`.
3. Scheduler:
   - Records state `PENDING` in JobStore and adds job to trace.
   - Calls Safety Kernel; on deny → state `DENIED`.
   - Uses pool map + `LeastLoadedStrategy` to choose a subject; publishes job to that `job.*` subject and moves state to `SCHEDULED → DISPATCHED → RUNNING`.
4. Worker consumes `job.*`, fetches `context_ptr`, performs work, writes result to `res:<job_id>`, and publishes `BusPacket{JobResult}` with `result_ptr`.
5. Scheduler updates JobStore with terminal state from `JobResult` and stores `result_ptr`.
6. Reconciler periodically marks old `DISPATCHED`/`RUNNING` jobs as `TIMEOUT` based on `config/timeouts.yaml`.
7. Cancellation: gateway or scheduler publishes `BusPacket{JobRequest(topic=sys.job.cancel, job_id=<id>)}` to `sys.job.cancel`; workers cancel the matching in-flight job context and publish a terminal `JobResult` (`CANCELLED` or `TIMEOUT`).

## Safety & Tenancy
- Safety policy file (`config/safety.yaml`) provides per-tenant `allow_topics` / `deny_topics`.
- Gateway can inject `TENANT_ID` into `JobRequest.env["tenant_id"]`; scheduler writes decision/reason into JobStore for dashboards.

## Context Engine (non-bus)
- gRPC service `ContextEngine` (`cmd/coretex-context-engine`) with RPCs:
  - `BuildWindow(memory_id, mode, logical_payload, max_input_tokens, max_output_tokens)` → list of `ModelMessage`.
  - `UpdateMemory(memory_id, logical_payload, model_response, mode)` → appends chat history.
  - `IngestRepo(memory_id, repo_root, scan_result)` → stores repo chunks for RAG lookups.
- Uses the same Redis instance; keys are namespaced under `mem:<memory_id>:*`.

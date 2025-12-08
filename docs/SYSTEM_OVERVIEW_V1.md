# CortexOS – System Overview (V1)

## Mission & Vision
- AI Control Plane (“AI Motherboard”) that schedules, routes, constrains, and observes AI/tool workloads with deterministic guarantees.
- Planes: Control (Go), Compute (LLM/Tools), Bus (NATS), Memory (Redis today, vector DB later).
- Principle: Everything speaks `BusPacket` over NATS; large data travels by pointer (Redis context/result).

## Core Components (current)
- **API Gateway (`cortex-api-gateway`)**
  - gRPC/HTTP: `SubmitJob`, `GetJobStatus`.
  - Writes context to Redis, publishes `BusPacket{JobRequest}` to `sys.job.submit`.
  - Reads job state/result_ptr from JobStore; API key via `X-API-Key`.
- **Scheduler (`cortex-scheduler`)**
  - Listens on `sys.job.submit`, `sys.job.result`, `sys.heartbeat.>`.
  - Uses Safety Kernel gRPC to ALLOW/DENY; `LeastLoadedStrategy` with pool mappings from `config/pools.yaml`.
  - Job states: PENDING → SCHEDULED → DISPATCHED → RUNNING → SUCCEEDED/FAILED/DENIED.
  - JobStore in Redis for state/result_ptr/trace/topic metadata.
  - Reconciler: timeouts for DISPATCHED/RUNNING (config/timeouts.yaml). Metrics on `:9090/metrics`.
- **Safety Kernel (`cortex-safety-kernel`)**
  - gRPC `Check(PolicyCheckRequest) → PolicyCheckResponse`.
  - Currently allows most topics except hard-coded denies (e.g., `sys.destroy`); foundation for RBAC/budget/policy.
- **JobStore / Memory**
  - Redis: contexts (`ctx:<id>`), results (`res:<id>`), job state, events, result_ptrs, trace mappings.
  - Pointer scheme: `context_ptr`/`result_ptr` are Redis URLs.

## Current Workers & Subjects
- **Echo** (`cortex-worker-echo`) — `job.echo`, pool `echo`; echoes payload.
- **Chat (simple)** (`cortex-worker-chat`) — `job.chat.simple`, pool `chat-simple`; simple echo/LLM-lite response.
- **Chat (advanced)** (`cortex-worker-chat-advanced`) — `job.chat.advanced`, pool `chat-advanced`; uses Ollama (`OLLAMA_URL`, model `OLLAMA_MODEL`).
- **Code LLM** (`cortex-worker-code-llm`) — `job.code.llm`, pool `code-llm`; returns structured patch:
  ```json
  { "file_path": "...", "original_code": "...", "instruction": "...", "patch": { "type": "unified_diff", "content": "..." } }
  ```
  - Reliability: retries Ollama up to 3x on retryable errors; 4m HTTP timeout; stores error text in result on failure.
- **Planner** (`cortex-worker-planner`) — `job.workflow.plan`, pool `workflow`; produces plan JSON (used when `USE_PLANNER=true`).
- **Orchestrator** (`cortex-worker-orchestrator`) — `job.workflow.demo`, pool `workflow`; deterministic multi-step workflow (code-llm → chat-simple).
  - Parent result shape:
    ```json
    { "file_path": "...", "original_code": "...", "instruction": "...", "patch": {...}, "explanation": "...", "workflow_id": "..." }
    ```

## Current Workflows
- **Echo test**: submit `job.echo`; result in Redis.
- **Chat simple**: submit `job.chat.simple` with `prompt`.
- **Code LLM single-step**: submit `job.code.llm`; get structured patch.
- **Code review + explanation** (`job.workflow.demo` via orchestrator):
  - Step 1: `job.code.llm` → patch.
  - Step 2: `job.chat.simple` → explanation.
  - Result stored at parent `res:<job_id>`.
  - Planner optionally engaged via `job.workflow.plan` (now mapped to `workflow` pool).

## Topics → Pools (compose default)
- `job.echo`: echo
- `job.chat.simple`: chat-simple
- `job.chat.advanced`: chat-advanced
- `job.code.llm`: code-llm
- `job.workflow.plan`: workflow
- `job.workflow.demo`: workflow

## Bus Protocol (wire contracts)
- Envelope: `BusPacket` (trace_id, sender_id, created_at, protocol_version, payload oneof).
- Payloads:
  - `JobRequest` (job_id, topic, priority, context_ptr, adapter_id, workflow_id, step_index, metadata).
  - `JobResult` (job_id, status, result_ptr, worker_id, execution_ms, error optional).
  - `Heartbeat` (worker_id, pool, type, cpu_load, gpu_utilization, active_jobs, capabilities, max_parallel_jobs).
  - `SystemAlert` (reserved).
- Large blobs move via Redis pointers; NATS subjects: `sys.job.submit`, `sys.job.result`, `sys.heartbeat.*`, `job.*`.

## Deployment (docker-compose snapshot)
- Services: nats, redis, ollama, cortex-{scheduler, safety-kernel, api-gateway, worker-*}, planner, orchestrator.
- Config mounts: `config/pools.yaml`, `config/timeouts.yaml`.
- Ports: NATS 4222, Redis 6379, API 8080/8081, Scheduler metrics 9090, Orchestrator metrics 9091, Gateway metrics 9092, Ollama 11434.

## Roadmap (planned workers/workflows)
- **Workers**: repo ingest (`job.repo.ingest`), lint (`job.repo.lint`), tests (`job.repo.tests`), static analysis (`job.repo.static`), math/solver, k8s ops (guarded), git/MR automation (guarded).
- **Workflows**:
  - Repo improver (`job.workflow.repo.improve`): ingest → lint → tests → static → code-llm on hotspots → tests → optional MR.
  - SRE outage responder (`job.workflow.sre.outage`): log analysis → root-cause → config/patch suggestion → (optional) k8s ops.
  - Planner-driven orchestration: orchestrator executes planner steps with allow-list topics/limits.

## Immediate Next Steps (stability & polish)
- Add explicit error payloads/result_ptrs in orchestrator failures; propagate child failure reasons.
- Expand metrics for gateway/orchestrator; health precheck for Ollama in code-llm worker.
- Tighten timeouts/retries per topic (timeouts.yaml) as loads grow.
- (Later) Enforce Safety Kernel policies (RBAC/budget/topic allow-lists) and add vector memory.

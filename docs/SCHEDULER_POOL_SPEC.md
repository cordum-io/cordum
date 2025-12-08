# CortexOS Scheduler + Pool Routing Spec (Current)

This document captures the current behavior of the control-plane scheduler and worker pools as implemented in the repository and exercised via `docker-compose`.

## Bus subjects
- `sys.job.submit` – inbound jobs to the scheduler.
- `sys.job.result` – job completions from workers.
- `sys.heartbeat.*` – worker heartbeats.
- Worker pools:
  - `job.echo` → pool `echo`
  - `job.chat.simple` → pool `chat-simple`
  - `job.chat.advanced` → pool `chat-advanced`

## Heartbeat contract (`api/proto/v1/heartbeat.proto`)
Fields in use:
- `worker_id`, `region`, `type`
- `cpu_load`, `gpu_utilization`, `active_jobs`
- `capabilities`
- `pool` (pool name)
- `max_parallel_jobs` (capacity hint)

Workers publish on `sys.heartbeat.<something>`; scheduler subscribes to `sys.heartbeat.>`.

## Worker pools (current)
- Echo:
  - Subject: `job.echo`
  - Pool: `echo`
  - Queue group: `workers-echo`
  - Behavior: reads context_ptr, echoes payload, writes result_ptr.
- Chat (simple):
  - Subject: `job.chat.simple`
  - Pool: `chat-simple`
  - Queue group: `workers-chat`
  - Behavior: simple “Echo: …” response.
- Chat (advanced):
  - Subject: `job.chat.advanced`
  - Pool: `chat-advanced`
  - Queue group: `workers-chat-advanced`
  - Behavior: calls Ollama (`OLLAMA_URL`, `OLLAMA_MODEL`) via `/api/generate`.
- Code LLM:
  - Subject: `job.code.llm`
  - Pool: `code-llm`
  - Queue group: `workers-code-llm`
  - Behavior: calls Ollama to produce structured patch suggestions (writes diff JSON with `{file_path, original_code, instruction, patch{type,content}}` to Redis, publishes result_ptr).
- Orchestrator (demo):
  - Subject: `job.workflow.demo`
  - Pool: `workflow`
  - Queue group: `workers-orchestrator`
  - Behavior: dispatches child jobs (`job.code.llm` then `job.chat.simple`) and aggregates results via pointers and JobStore.

## Scheduler routing
- Topic→pool map now comes from `config/pools.yaml` (override with `POOL_CONFIG_PATH`):
  - `job.echo` → `echo`
  - `job.chat.simple` → `chat-simple`
  - `job.chat.advanced` → `chat-advanced`
  - `job.code.llm` → `code-llm`
  - `job.workflow.demo` → `workflow`
- Strategy: `LeastLoadedStrategy` uses load score
  - `score = active_jobs + cpu_load/100 + gpu_utilization/100`
  - Chooses the lowest-score worker in the pool; publishes to the topic (pool subject).
- Registry: maintains latest heartbeat per worker; `WorkersForPool` helper filters by pool.
- Safety: `cortex-safety-kernel` denies `topic=sys.destroy`, otherwise allows.

## Result and context pointers
- Context key: `ctx:<job_id>` stored in Redis, pointer `redis://ctx:<job_id>`.
- Result key: `res:<job_id>` stored in Redis, pointer `redis://res:<job_id>`.

## Job state machine
- Canonical states: `PENDING → SCHEDULED → DISPATCHED → RUNNING → SUCCEEDED | FAILED | CANCELLED | TIMEOUT` (plus `DENIED` for safety failures).
- Transitions are enforced and recorded in Redis (state + event log). Backwards/non-monotonic moves are rejected.
- Scheduler sets:
  - `PENDING` on receipt
  - `SCHEDULED` after safety allow/subject selection
  - `DISPATCHED` then `RUNNING` after successful publish
  - Terminal states on results (`SUCCEEDED`/`FAILED`) or safety denial (`DENIED`)
- Reconciler loop (inside scheduler):
  - Periodically scans `DISPATCHED`/`RUNNING` older than the timeout window and marks them `TIMEOUT`.
  - Uses per-state sorted sets (`job:index:<state>`) for efficient scans.

## Env vars (relevant)
- `NATS_URL` (default `nats://localhost:4222` or compose service `nats`)
- `REDIS_URL` (default `redis://localhost:6379` or compose service `redis`)
- `SAFETY_KERNEL_ADDR` (default `localhost:50051` or compose service `cortex-safety-kernel:50051`)
- `OLLAMA_URL` (advanced worker, default `http://ollama:11434`)
- `OLLAMA_MODEL` (advanced worker, default `llama3`)
- `POOL_CONFIG_PATH` (scheduler; default `config/pools.yaml`)
- `API_KEY` (gateway HTTP; optional)
- Metrics: scheduler exports Prometheus on `:9090/metrics` (internal).
- Timeouts: orchestrator reads `TIMEOUT_CONFIG_PATH` (default `config/timeouts.yaml`) for child/total workflow timeouts and retries.

## Run and test (local/compose)
- Bring up stack: `docker-compose up --build -d`
  - Includes scheduler, safety kernel, API gateway, echo, chat-simple, chat-advanced, NATS, Redis.
- Smoke scripts:
  - Echo: `go run ./tools/scripts/send_echo_job.go`
  - Chat simple: `go run ./tools/scripts/chat/send_chat_job.go`
  - Chat advanced: `go run ./tools/scripts/chat_advanced/send_chat_advanced_job.go`
  - Use `GOMODCACHE=$(pwd)/.gomodcache GOCACHE=$(pwd)/.gocache` if you want in-repo caches.
- Inspect results:
  - `docker exec cortex-redis-1 redis-cli get res:<job_id>`
  - Scheduler logs show job receipt, pool selection, and chosen worker score.

## Notes / gaps
- Scheduler container must be rebuilt/restarted to pick up topic→pool changes; ensure `docker-compose up --build -d` or `docker-compose up -d --no-deps cortex-scheduler` is run after changes.
- If `OLLAMA_URL` is unreachable, advanced worker returns a stub response prefixed with `[fallback]`.

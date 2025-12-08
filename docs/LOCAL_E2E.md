# CortexOS Local E2E Snapshot (Docker)

This captures the current, validated end-to-end flow running via `docker-compose` with NATS + Redis + control-plane binaries + echo/chat workers.

## Stack composition
- NATS (`4222`)
- Redis (`6379`)
- `cortex-safety-kernel` (gRPC on `50051` inside the network)
- `cortex-scheduler`
- `cortex-api-gateway` (`:8080` gRPC, `:8081/health`)
- `cortex-worker-echo` (subject `job.echo`, pool `echo`)
- `cortex-worker-chat` (subject `job.chat.simple`, pool `chat-simple`)
- `cortex-worker-chat-advanced` (subject `job.chat.advanced`, pool `chat-advanced`, uses `OLLAMA_URL`)
- `cortex-worker-code-llm` (subject `job.code.llm`, pool `code-llm`, uses `OLLAMA_URL`)
- `cortex-worker-orchestrator` (subject `job.workflow.demo`, pool `workflow`)
- `ollama` (LLM runtime, port `11434`, model pulled separately)
- Scheduler pool routing is configured via `config/pools.yaml` (mounted into the scheduler container; override with `POOL_CONFIG_PATH`).
- Planner worker available on `job.workflow.plan`; orchestrator uses it when `USE_PLANNER=true` (enabled by default in compose).

## Bring-up
```bash
docker-compose up --build -d
docker-compose ps
# First run only: pull the model inside the Ollama container
docker-compose exec ollama ollama pull llama3
```
Expected: all services in `State=Up` with ports exposed for NATS/Redis/API.

## Tested flows

### Echo job
- Submit: `GOMODCACHE=$(pwd)/.gomodcache GOCACHE=$(pwd)/.gocache /usr/local/go/bin/go run ./tools/scripts/send_echo_job.go`
- Scheduler logs: job received → dispatch to `job.echo` → completion with `result_ptr`.
- Worker logs: context payload printed, completion logged.
- Result (example):
  ```bash
  docker exec cortex-redis-1 redis-cli get res:<job_id>
  {"completed_at_utc":"2025-12-07T00:28:29Z","job_id":"...","processed_by":"worker-echo-1","received_ctx":{"created_at":"...","prompt":"hello from send_echo_job"}}
  ```

### Chat job
- Submit: `GOMODCACHE=$(pwd)/.gomodcache GOCACHE=$(pwd)/.gocache /usr/local/go/bin/go run ./tools/scripts/chat/send_chat_job.go`
- Scheduler logs: job received on `job.chat.simple` → dispatched → completed with `result_ptr`.
- Worker logs: completion logged.
- Result (example):
  ```bash
  docker exec cortex-redis-1 redis-cli get res:<job_id>
  {"completed_at":"2025-12-07T00:30:12Z","job_id":"...","processed_by":"worker-chat-1","prompt":"hello from chat job","response":"Echo: hello from chat job"}
  ```

### Chat advanced job (optional, uses Ollama if reachable)
- Submit: `GOMODCACHE=$(pwd)/.gomodcache GOCACHE=$(pwd)/.gocache /usr/local/go/bin/go run ./tools/scripts/chat_advanced/send_chat_advanced_job.go`
- Scheduler logs: job received on `job.chat.advanced` → dispatched to pool `chat-advanced`.
- Result pointer: `docker exec cortex-redis-1 redis-cli get res:<job_id>` (will contain model name and Ollama output). Requires `ollama` service healthy and `ollama pull llama3` completed.

### Code LLM job
- Submit: `GOMODCACHE=$(pwd)/.gomodcache GOCACHE=$(pwd)/.gocache /usr/local/go/bin/go run ./tools/scripts/code/send_code_job.go`
- Scheduler logs: job received on `job.code.llm` → dispatched to pool `code-llm`.
- Result pointer: `docker exec cortex-redis-1 redis-cli get res:<job_id>` (contains patch suggestion from Ollama).
- Requires `ollama` service healthy and `ollama pull llama3`; worker performs a health check on startup and will retry transient timeouts, but will mark failed and include an error payload if Ollama stays unreachable.

### Workflow orchestration (demo)
- Submit: `GOMODCACHE=$(pwd)/.gomodcache GOCACHE=$(pwd)/.gocache /usr/local/go/bin/go run ./tools/scripts/workflow/send_workflow_job.go`
- Flow: orchestrator on `job.workflow.demo` spawns `job.code.llm` then `job.chat.simple`, aggregates results via Redis/JobStore.
- Result pointer: `docker exec cortex-redis-1 redis-cli get res:<parent_job_id>` (combined patch + explanation JSON). Retries/timeouts are handled internally if a child is slow or fails.

### Code review workflow (real file)
- Submit: `GOMODCACHE=$(pwd)/.gomodcache GOCACHE=$(pwd)/.gocache /usr/local/go/bin/go run ./tools/scripts/workflow/code_review/send_code_review_job.go -file path/to/file -instruction "improve and add logging"`
- Flow: same orchestrator (`job.workflow.demo`) but using real file content/instruction; code-LLM returns structured patch (`type`, `content`, `original_code`, `instruction`), chat-simple explains it.
- Result pointer: `docker exec cortex-redis-1 redis-cli get res:<parent_job_id>` will contain `{file_path, original_code, instruction, patch{type,content}, explanation, workflow_id}`.
- If planner is enabled, orchestrator will execute the plan returned from `job.workflow.plan`, validating topics before running child jobs.

## Notes
- Safety kernel currently allows all topics except the hardcoded deny in code (`sys.destroy`).
- Heartbeats flow to the scheduler on `sys.heartbeat.*` and inform pool/capacity.
  - Scripts use localhost ports; when running inside the repo against compose, they talk to the mapped NATS/Redis ports. Use the provided `GOMODCACHE`/`GOCACHE` envs to avoid global cache writes.
- Metrics: scheduler exposes Prometheus `/metrics` on `:9090` inside the compose network. Gateway supports optional API key via `API_KEY` env + `X-API-Key` header.
- Gateway exposes `/metrics` on `:9092`; orchestrator exposes `/metrics` on `:9091`.
- Timeouts: orchestrator reads `config/timeouts.yaml` (mounted via `TIMEOUT_CONFIG_PATH`) for child/total workflow timeouts and retries.
- Dashboard: run `cd web/dashboard && npm install && npm run dev` with `.env.local` values:
  - `VITE_API_BASE=http://localhost:8081`
  - `VITE_WS_BASE=ws://localhost:8081/api/v1/stream`
  - `VITE_API_KEY=<same as API_KEY>` if gateway auth is enabled (WebSocket passes `api_key` query param).

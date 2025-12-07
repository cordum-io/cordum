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
- `cortex-worker-chat-advanced` (subject `job.chat.advanced`, pool `chat-advanced`, uses `OLLAMA_URL` if reachable; otherwise stub response)
- `cortex-worker-code-llm` (subject `job.code.llm`, pool `code-llm`)
- `cortex-worker-orchestrator` (subject `job.workflow.demo`, pool `workflow`)

## Bring-up
```bash
docker-compose up --build -d
docker-compose ps
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
- Result pointer: `docker exec cortex-redis-1 redis-cli get res:<job_id>` (will contain model name and either Ollama output or a fallback stub).

### Code LLM job
- Submit: `GOMODCACHE=$(pwd)/.gomodcache GOCACHE=$(pwd)/.gocache /usr/local/go/bin/go run ./tools/scripts/code/send_code_job.go`
- Scheduler logs: job received on `job.code.llm` → dispatched to pool `code-llm`.
- Result pointer: `docker exec cortex-redis-1 redis-cli get res:<job_id>` (contains stub patch suggestion JSON).

## Notes
- Safety kernel currently allows all topics except the hardcoded deny in code (`sys.destroy`).
- Heartbeats flow to the scheduler on `sys.heartbeat.*` and inform pool/capacity.
- Scripts use localhost ports; when running inside the repo against compose, they talk to the mapped NATS/Redis ports. Use the provided `GOMODCACHE`/`GOCACHE` envs to avoid global cache writes.

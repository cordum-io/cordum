# CortexOS Docker Quickstart

This repo now ships a simple Docker setup to run the control plane, workers, and dependencies together for local testing.

## What's included
- `nats` (bus) on `4222`
- `redis` (memory fabric) on `6379`
- `ollama` (LLM runtime) on `11434` with a persistent volume
- `cortex-safety-kernel`
- `cortex-scheduler`
- `cortex-api-gateway` (`:8080` gRPC, `:8081/health`)
- `cortex-worker-echo`
- `cortex-worker-chat`
- `cortex-worker-chat-advanced` (calls Ollama at `OLLAMA_URL`)
- `cortex-worker-code-llm`
- `cortex-worker-orchestrator`
- `cortex-worker-planner` (optional plan generator)
- Scheduler reads pool routing from `config/pools.yaml` (mounted into the container); override with `POOL_CONFIG_PATH`.

## Build and run
```bash
docker compose build
docker compose up
# First time only: pull the LLM model into Ollama
docker compose exec ollama ollama pull llama3
```

Environment defaults:
- `NATS_URL` – `nats://nats:4222`
- `REDIS_URL` – `redis://redis:6379`
- `SAFETY_KERNEL_ADDR` – `cortex-safety-kernel:50051`
- `OLLAMA_URL` – `http://ollama:11434` (used by advanced chat + code-LLM workers)
- `OLLAMA_MODEL` – `llama3`
  - Also used by code-LLM worker; ensure Ollama is running or reachable.
- `POOL_CONFIG_PATH` – `/etc/cortex/pools.yaml` (set in compose; edit `config/pools.yaml` to change pool routing)
- `API_KEY` – optional API key for the gateway HTTP endpoints (header `X-API-Key`)
- `TIMEOUT_CONFIG_PATH` – `/etc/cortex/timeouts.yaml` (per-workflow/topic timeouts)
- `TENANT_ID` – optional tenant label injected by gateway
- `USE_PLANNER` / `PLANNER_TOPIC` – orchestrator planner flag (defaults off; planner listens on `job.workflow.plan`)
- Metrics endpoints: scheduler exposes `/metrics` on `:9090` inside the compose network (Prometheus format).
  Gateway exposes `/metrics` on `:9092`.

### Dashboard (optional)
- Build UI: `cd web/dashboard && npm install && npm run dev`
- Env (via `.env.local`):
  - `VITE_API_BASE=http://localhost:8081`
  - `VITE_WS_BASE=ws://localhost:8081/api/v1/stream`
  - `VITE_API_KEY=<same as API_KEY>` (if set; WS will pass `api_key` query param)
  - `VITE_WS_BASE` can be omitted to auto-derive from `VITE_API_BASE`.
The UI consumes `/api/v1/workers`, `/api/v1/jobs`, `/api/v1/traces/:id`, and `/api/v1/stream`.

## Smoke test
With the stack running, send a job to NATS:
```bash
NATS_URL=nats://localhost:4222 \
REDIS_URL=redis://localhost:6379 \
go run ./tools/scripts/send_echo_job.go

# Code LLM job
NATS_URL=nats://localhost:4222 \
REDIS_URL=redis://localhost:6379 \
go run ./tools/scripts/code/send_code_job.go

# Workflow demo job (triggers code-llm then chat-simple)
NATS_URL=nats://localhost:4222 \
REDIS_URL=redis://localhost:6379 \
go run ./tools/scripts/workflow/send_workflow_job.go

# Code review workflow on a real file
NATS_URL=nats://localhost:4222 \
REDIS_URL=redis://localhost:6379 \
go run ./tools/scripts/workflow/code_review/send_code_review_job.go -file path/to/file -instruction "improve and add logging"

# Planner (optional): enable in compose by setting USE_PLANNER=true on orchestrator
```

You should see:
- Scheduler logging a received job and dispatching it to `job.echo`.
- Echo worker logging the received job and publishing a result pointer.
- Optional: `api-gateway` reports status via `GetJobStatus` if you query it (gRPC on `localhost:8080`).

Stop everything with `docker compose down`.

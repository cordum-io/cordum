# CortexOS Docker Quickstart

This repo now ships a simple Docker setup to run the control plane, workers, and dependencies together for local testing.

## What's included
- `nats` (bus) on `4222`
- `redis` (memory fabric) on `6379`
- `cortex-safety-kernel`
- `cortex-scheduler`
- `cortex-api-gateway` (`:8080` gRPC, `:8081/health`)
- `cortex-worker-echo`
- `cortex-worker-chat`
- `cortex-worker-chat-advanced` (calls Ollama at `OLLAMA_URL` if available; otherwise falls back to a local stub)
- `cortex-worker-code-llm`
- `cortex-worker-orchestrator`

## Build and run
```bash
docker compose build
docker compose up
```

Environment defaults:
- `NATS_URL` – `nats://nats:4222`
- `REDIS_URL` – `redis://redis:6379`
- `SAFETY_KERNEL_ADDR` – `cortex-safety-kernel:50051`
- `OLLAMA_URL` – `http://ollama:11434` (used by advanced chat worker; if unreachable, worker falls back to a stub response)
- `OLLAMA_MODEL` – `llama3`
  - Also used by code-LLM worker; ensure Ollama is running or reachable.

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
```

You should see:
- Scheduler logging a received job and dispatching it to `job.echo`.
- Echo worker logging the received job and publishing a result pointer.
- Optional: `api-gateway` reports status via `GetJobStatus` if you query it (gRPC on `localhost:8080`).

Stop everything with `docker compose down`.

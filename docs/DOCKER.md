# Docker Compose Quickstart

Run the full coretexOS stack locally (bus, Redis, control plane, workers, Ollama). Compose builds binaries from `cmd/` and mounts config files.

## Services in `docker-compose.yml`
- Infra: `nats`, `redis`, `ollama` (model runtime, persistent volume).
- Control plane: `coretex-safety-kernel`, `coretex-scheduler`, `coretex-api-gateway`.
- Workers & workflows: echo, chat, chat-advanced, code-llm, planner, demo orchestrator, repo pipeline (`repo-scan`, `repo-sast`, `repo-partition`, `repo-lint`, `repo-tests`, `repo-report`, repo orchestrator).
- Config mounts: `config/pools.yaml`, `config/timeouts.yaml`, `config/safety.yaml`.
- Metrics: scheduler `:9090/metrics`, orchestrator `:9091/metrics`, gateway `:9092/metrics` (inside compose network; gateway metrics also exposed on host).

## Bring-up
```bash
docker compose build
docker compose up -d
# First run only: pull an LLM for chat/code workers
docker compose exec ollama ollama pull llama3
docker compose ps
```

## Smoke Jobs
Use repo-local caches to avoid writing outside the project:
```bash
GOCACHE=$(pwd)/.cache/go-build go run ./tools/scripts/send_echo_job.go
GOCACHE=$(pwd)/.cache/go-build go run ./tools/scripts/chat/send_chat_job.go
GOCACHE=$(pwd)/.cache/go-build go run ./tools/scripts/code/send_code_job.go
GOCACHE=$(pwd)/.cache/go-build go run ./tools/scripts/workflow/send_workflow_job.go
```

Repo review (via gateway HTTP):
```bash
curl -X POST http://localhost:8081/api/v1/repo-review \
  -H 'Content-Type: application/json' \
  -d '{"repo_url":"https://github.com/example/repo.git","branch":"main","max_files":200,"run_tests":false}'
```

Inspect results:
```bash
docker exec coretex-redis-1 redis-cli get res:<job_id>
```

## Environment Defaults (override in compose if needed)
- `NATS_URL=nats://nats:4222`, `REDIS_URL=redis://redis:6379`
- `SAFETY_KERNEL_ADDR=coretex-safety-kernel:50051`, `SAFETY_POLICY_PATH=/etc/coretex/safety.yaml`
- `POOL_CONFIG_PATH=/etc/coretex/pools.yaml`, `TIMEOUT_CONFIG_PATH=/etc/coretex/timeouts.yaml`
- `API_KEY` (gateway HTTP/WS), `TENANT_ID` (gateway injects into `JobRequest.env`)
- `OLLAMA_URL=http://ollama:11434`, `OLLAMA_MODEL=llama3`
- Orchestrator: `USE_PLANNER=true` (enabled in compose), `PLANNER_TOPIC=job.workflow.plan`

Stop everything with `docker compose down`.

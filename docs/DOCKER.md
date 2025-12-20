# Docker Compose Quickstart

Run the full coretexOS stack locally (bus, Redis, control plane, workers, Ollama). Compose builds binaries from `cmd/` and mounts config files.

## Services in `docker-compose.yml`
- Infra: `nats`, `redis`, `ollama` (model runtime, persistent volume).
- Control plane: `coretex-safety-kernel`, `coretex-scheduler`, `coretex-api-gateway`, `coretex-workflow-engine`, `coretex-context-engine`.
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

If you see `permission denied while trying to connect to the Docker daemon socket`, run the commands with `sudo` (both build and up), e.g.:
```bash
sudo docker compose build
sudo docker compose up -d
```

## Dashboard

The dashboard UI lives under `web/dashboard/` and runs as the `coretex-dashboard` service.

- Open: `http://localhost:3000/`
- Auth: gateway expects `CORETEX_API_KEY` (alias: `CORETEX_SUPER_SECRET_API_TOKEN`); compose default is `super-secret-key` (change it for real deployments). The dashboard container uses `CORETEX_DASHBOARD_API_KEY` to prefill the UI.

To use your own key in compose:
```bash
cp .env.example .env
# edit CORETEX_API_KEY
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
export CORETEX_API_KEY=super-secret-key
curl -X POST http://localhost:8081/api/v1/repo-review \
  -H 'Content-Type: application/json' \
  -H "X-API-Key: $CORETEX_API_KEY" \
  -d '{"repo_url":"https://github.com/example/repo.git","branch":"main","max_files":200,"run_tests":false}'
```

Inspect results:
```bash
docker exec coretex-redis-1 redis-cli get res:<job_id>
```

## Environment Defaults (override in compose if needed)
- `NATS_URL=nats://nats:4222`, `REDIS_URL=redis://redis:6379`
- `SAFETY_KERNEL_ADDR=coretex-safety-kernel:50051`, `SAFETY_POLICY_PATH=/etc/coretex/safety.yaml`
- Safety Kernel service must bind `SAFETY_KERNEL_ADDR=:50051` in compose so other containers can reach it.
- `CONTEXT_ENGINE_ADDR` (context engine gRPC; workers point at `coretex-context-engine:50070`)
- `POOL_CONFIG_PATH=/etc/coretex/pools.yaml`, `TIMEOUT_CONFIG_PATH=/etc/coretex/timeouts.yaml`
- `CORETEX_API_KEY` / `API_KEY` (gateway HTTP/WS), `TENANT_ID` (gateway injects into `JobRequest.env`)
- Workflow engine: `WORKFLOW_ENGINE_HTTP_ADDR=:9093`, `WORKFLOW_ENGINE_SCAN_INTERVAL=5s`, `WORKFLOW_ENGINE_RUN_SCAN_LIMIT=200`
- `OLLAMA_URL=http://ollama:11434`, `OLLAMA_MODEL=llama3`
- Orchestrator: `USE_PLANNER=true` (enabled in compose), `PLANNER_TOPIC=job.workflow.plan`

Stop everything with `docker compose down`.

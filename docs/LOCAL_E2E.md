# Local E2E (Compose) – What Works Today

This captures the end-to-end flows validated with `docker compose up -d` (NATS + Redis + control plane + workers + Ollama).

## Stack (compose)
- Infra: NATS `4222`, Redis `6379`, Ollama `11434` (persistent volume).
- Control plane: scheduler, safety kernel, API gateway (HTTP `:8081`, gRPC `:8080`, metrics `:9092`).
- Workers/pools: echo, chat-simple, chat-advanced, code-llm, planner, demo orchestrator, repo pipeline (`repo-scan`, `repo-sast`, `repo-partition`, `repo-lint`, `repo-tests`, `repo-report`, repo orchestrator).
- Config: pools/timeouts/safety mounted from `config/`.

## Flows

### Echo
```bash
GOCACHE=$(pwd)/.cache/go-build go run ./tools/scripts/send_echo_job.go
docker exec cortex-redis-1 redis-cli get res:<job_id>
```

### Chat (simple)
```bash
GOCACHE=$(pwd)/.cache/go-build go run ./tools/scripts/chat/send_chat_job.go
docker exec cortex-redis-1 redis-cli get res:<job_id>
```

### Code LLM
Requires `ollama pull llama3`.
```bash
GOCACHE=$(pwd)/.cache/go-build go run ./tools/scripts/code/send_code_job.go
docker exec cortex-redis-1 redis-cli get res:<job_id>
```

### Demo Workflow (code patch → explanation)
```bash
GOCACHE=$(pwd)/.cache/go-build go run ./tools/scripts/workflow/send_workflow_job.go
docker exec cortex-redis-1 redis-cli get res:<parent_job_id>
```

### Repo Code Review Pipeline
Entry topic: `job.workflow.repo.code_review` (repo orchestrator). Submit via gateway HTTP:
```bash
curl -X POST http://localhost:8081/api/v1/repo-review \
  -H 'Content-Type: application/json' \
  -d '{"repo_url":"https://github.com/example/repo.git","branch":"main","max_files":200,"run_tests":false}'
```
The orchestrator drives `job.repo.scan` → `job.repo.sast` → `job.repo.partition` → `job.repo.lint` → `job.code.llm` + `job.chat.simple` per file → optional `job.repo.tests` → `job.repo.report`. Results are stored in Redis at `res:<workflow_job_id>` and child job pointers are recorded in JobStore.

## Notes
- Safety policy (`config/safety.yaml`) denies `sys.*` and allows `job.*` for the default tenant.
- Scheduler state machine and timeouts come from `config/timeouts.yaml`; reconciler marks stale jobs `TIMEOUT`.
- Use repo-local caches when running scripts (`GOCACHE=.cache/go-build`) to avoid permission issues.
- Metrics endpoints: scheduler `:9090/metrics`, orchestrator `:9091/metrics`, gateway `:9092/metrics`.

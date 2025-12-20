# Local E2E (Compose) – What Works Today

This captures the end-to-end flows validated with `docker compose up -d` (NATS + Redis + control plane + workers + Ollama).

## Stack (compose)
- Infra: NATS `4222`, Redis `6379`, Ollama `11434` (persistent volume).
- Control plane: scheduler, safety kernel, API gateway (HTTP `:8081`, gRPC `:8080`, metrics `:9092`), workflow engine (`:9093/health`).
- Workers/pools: echo, chat-simple, chat-advanced, code-llm, planner, demo orchestrator, repo pipeline (`repo-scan`, `repo-sast`, `repo-partition`, `repo-lint`, `repo-tests`, `repo-report`, repo orchestrator).
- Config: pools/timeouts/safety mounted from `config/`.

## Flows

### Automated Smoke (Echo via Gateway)
Runs a minimal end-to-end stack (Redis + NATS containers; control-plane + echo worker as local Go processes), submits `job.echo` via the API gateway, and asserts:
- job result payload
- job detail `trace_id` + `context_ptr` + context JSON
- `/api/v1/jobs` includes `trace_id`
- `/api/v1/traces/:id` includes the job
- WS stream includes heartbeats + the job result (requires Node.js)

```bash
./tools/scripts/e2e/run_echo_gateway_e2e.sh
```

### Automated Full (Gateway + Mock Ollama)
Runs a fuller local stack (Redis + NATS containers; control-plane + context engine + echo/chat/chat-advanced/code-llm workers as local Go processes) with a mock Ollama HTTP server (no model downloads), submits:
- `job.echo`
- `job.chat.simple`
- `job.chat.advanced`
- `job.code.llm`
and validates a Workflow Engine run with an approval gate (`POST /api/v1/workflows`, `POST /runs`, approve step, then `job.echo`).

and asserts REST + trace + WS stream events for each job.

```bash
./tools/scripts/e2e/run_gateway_full_e2e.sh
```

### Automated Compose Smoke (No Local Go Required)
Runs the compose stack (Redis + NATS + control-plane + workers) and points Ollama-dependent workers at a tiny host-run mock server (no model downloads, no `ollama pull`), then validates:
- `job.echo` dispatch with Studio-style labels (`workflow_id`, `run_id`, `node_id`)
- `job.chat.advanced` multi-message memory growth (reads `redis://mem:<memory_id>:events` via `GET /api/v1/memory`)
- a Workflow Engine run (draft → refine → summarize) using `job.chat.advanced`

```bash
./tools/scripts/e2e/run_compose_gateway_smoke.sh
```

### Echo
```bash
GOCACHE=$(pwd)/.cache/go-build go run ./tools/scripts/send_echo_job.go
docker exec coretex-redis-1 redis-cli get res:<job_id>
```

### Chat (simple)
Requires `ollama pull llama3`.
```bash
GOCACHE=$(pwd)/.cache/go-build go run ./tools/scripts/chat/send_chat_job.go
docker exec coretex-redis-1 redis-cli get res:<job_id>
```

### Code LLM
Requires `ollama pull llama3`.
```bash
GOCACHE=$(pwd)/.cache/go-build go run ./tools/scripts/code/send_code_job.go
docker exec coretex-redis-1 redis-cli get res:<job_id>
```

### Demo Workflow (code patch → explanation)
```bash
GOCACHE=$(pwd)/.cache/go-build go run ./tools/scripts/workflow/send_workflow_job.go
docker exec coretex-redis-1 redis-cli get res:<parent_job_id>
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
- Cancellation is broadcast on `sys.job.cancel`; workers built on `core/agent/runtime` honor cancel requests via context cancellation.
- Use repo-local caches when running scripts (`GOCACHE=.cache/go-build`) to avoid permission issues.
- Metrics endpoints: scheduler `:9090/metrics`, orchestrator `:9091/metrics`, gateway `:9092/metrics`.

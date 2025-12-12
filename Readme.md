# CortexOS — AI Control Plane (Go + NATS)

CortexOS is a Go control plane that routes, schedules, and safeguards AI/agent workloads over a NATS bus. Jobs move as protobuf envelopes, large payloads live in Redis via pointers, safety gates every dispatch, and workers live in `packages/` with thin binaries under `cmd/`.

## Architecture Snapshot
- **Bus:** NATS subjects `sys.job.submit`, `sys.job.result`, `sys.heartbeat`, and worker subjects `job.*` (pool map in `config/pools.yaml`).
- **Control Plane:** Scheduler (`core/controlplane/scheduler`) with safety check, least-loaded strategy, Redis-backed `JobStore`, and a reconciler that marks stale jobs `TIMEOUT`.
- **Safety Kernel:** gRPC `Check` service; policy file at `config/safety.yaml` (deny/allow per tenant).
- **API Gateway:** gRPC + HTTP/WS; writes contexts to Redis, publishes to `sys.job.submit`, tracks state/result via JobStore, streams bus events, and exposes metrics on `:9092/metrics`. Repo review helper endpoint: `POST /api/v1/repo-review`.
- **Context Engine:** gRPC service (`cmd/cortex-context-engine`) that builds model windows, maintains chat history, and ingests repo scan data in Redis.
- **Workers/Workflows (packages/):** echo, chat, chat-advanced (Ollama), code-llm (patch generator), planner, demo orchestrator, repo pipeline (scan → SAST → partition → lint → tests → report) driven by `job.workflow.repo.code_review`.
- **Memory:** Redis for contexts/results and job metadata (`job:*` keys, per-state indices, recent jobs, trace mappings).

## Layout
- `core/` – bus/config/memory/metrics, scheduler, agent runtime, context engine, protocol (`core/protocol/proto/v1` + generated `core/protocol/pb/v1`).
- `packages/` – workers (`packages/workers/*`) and workflows (`packages/workflows/*`), providers under `packages/providers`.
- `cmd/` – binaries wiring config to core/packages (scheduler, safety kernel, API gateway, context engine, each worker).
- `config/` – pools, timeouts, safety policy.
- `tools/scripts/` – smoke/send-job helpers for chat/code/workflows.
- `docs/` – protocol, scheduler, docker/local guides; `spec/` contains CAP spec notes.

## Topics → Pools (from `config/pools.yaml`)
| Topic | Pool | Notes |
| --- | --- | --- |
| `job.echo` | `echo` | simple echo |
| `job.chat.simple` | `chat-simple` | lightweight chat |
| `job.chat.advanced` | `chat-advanced` | Ollama-backed |
| `job.code.llm` | `code-llm` | structured code patch |
| `job.workflow.plan` | `workflow` | planner |
| `job.workflow.demo` | `workflow` | demo orchestrator (code patch → explanation) |
| `job.workflow.repo.code_review` | `workflow` | repo pipeline orchestrator |
| `job.repo.scan` | `repo-scan` | repo index + archive |
| `job.repo.partition` | `repo-partition` | batch files |
| `job.repo.lint` | `repo-lint` | lint batch |
| `job.repo.sast` | `repo-sast` | heuristic SAST |
| `job.repo.tests` | `repo-tests` | run tests |
| `job.repo.report` | `repo-report` | aggregate findings |

## Quickstart (Docker)
Requirements: Docker/Compose, Go 1.24+ (if running scripts locally).

```bash
docker compose build
docker compose up -d
# First run: pull an LLM for advanced/chat/code workers
docker compose exec ollama ollama pull llama3
```

Smoke a job (uses repo-local caches to avoid writing to global Go cache):
```bash
GOCACHE=$(pwd)/.cache/go-build go run ./tools/scripts/send_echo_job.go
GOCACHE=$(pwd)/.cache/go-build go run ./tools/scripts/chat/send_chat_job.go
GOCACHE=$(pwd)/.cache/go-build go run ./tools/scripts/code/send_code_job.go
# Workflow demo (code patch + explanation)
GOCACHE=$(pwd)/.cache/go-build go run ./tools/scripts/workflow/send_workflow_job.go
# Repo code review via gateway HTTP
curl -X POST http://localhost:8081/api/v1/repo-review \
  -H 'Content-Type: application/json' \
  -d '{"repo_url":"https://github.com/example/repo.git","branch":"main","max_files":200,"run_tests":false}'
```

Inspect results:
```bash
docker exec cortex-redis-1 redis-cli get res:<job_id>
```

## Development & Testing
- Go toolchain: `go 1.24` (module-specified). To avoid permission errors, pin caches locally: `GOCACHE=$(pwd)/.cache/go-build go test ./...`.
- Proto changes: edit files under `core/protocol/proto/v1`, then run `make proto`.
- Docker images: `docker compose build` rebuilds all services; adjust `config/pools.yaml` / `config/timeouts.yaml` / `config/safety.yaml` as needed.

## Configuration (env defaults)
- `NATS_URL` (`nats://localhost:4222`), `REDIS_URL` (`redis://localhost:6379`)
- `SAFETY_KERNEL_ADDR` (`localhost:50051`), `SAFETY_POLICY_PATH` (`config/safety.yaml`)
- `POOL_CONFIG_PATH` (`config/pools.yaml`), `TIMEOUT_CONFIG_PATH` (`config/timeouts.yaml`)
- `CONTEXT_ENGINE_ADDR` (`:50070`)
- `API_KEY` (optional gateway HTTP/WS key), `TENANT_ID` (gateway injects into `JobRequest.env["tenant_id"]`)
- Ollama workers: `OLLAMA_URL` (`http://ollama:11434`), `OLLAMA_MODEL` (`llama3`)
- Orchestrators: `USE_PLANNER` (default false), `PLANNER_TOPIC` (`job.workflow.plan`)

## Observability & Safety
- Metrics: scheduler `:9090/metrics`, orchestrator `:9091/metrics`, API gateway `:9092/metrics`.
- Safety kernel denies/permits per `config/safety.yaml`; scheduler records decisions in JobStore for dashboards/trace queries.
- Job lifecycle/state, traces, and pointers are stored in Redis (`core/infra/memory.RedisJobStore`).

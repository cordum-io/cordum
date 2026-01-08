# coretexOS - AI Control Plane (Go + NATS)

coretexOS is a platform-only control plane for AI workloads. It uses NATS for the bus,
Redis for state and payload pointers, and CAP v2 wire contracts for jobs, results, and heartbeats.
Worker implementations and product packs live outside this repo.

Key ideas:
- Bus-first: publish/subscribe with optional JetStream durability.
- Control plane: scheduler + safety kernel + workflow engine + API gateway (+ optional context engine).
- State: Redis for job metadata, workflow runs, config, DLQ, and payload pointers.
- External workers connect via job topics; no built-in workers ship here.

Docs:
- `docs/system_overview.md` for the full architecture.
- `docs/AGENT_PROTOCOL.md` for bus and pointer semantics.
- `docs/backend_capabilities.md` for feature coverage.
- `docs/pack.md` for pack format and install/uninstall flows.
- `docs/techinal_plan.md` for current + roadmap status.

## Architecture (current code)

Components:
- API gateway: HTTP/WS + gRPC for jobs, workflows/runs, approvals, config, policy, DLQ, schemas, locks, artifacts, traces, packs.
- Scheduler: safety gate, config-driven routing (pool + labels + requires), job state in Redis, reconciler timeouts.
- Safety kernel: gRPC policy check using `config/safety.yaml` plus config-service fragments; effective config snapshot passed via job env; explain/simulate.
- Workflow engine: Redis-backed workflows/runs with for_each fan-out, retries/backoff, approvals, delay/notify/condition steps, reruns, timeline.
- Context engine (optional): gRPC helper for context windows and memory in Redis.
- Dashboard (optional): React UI served via Nginx; connects to `/api/v1` and `/api/v1/stream`.
- External workers: subscribe to `job.*` or `worker.<id>.jobs`, publish results on `sys.job.result`.

Bus subjects:
- `sys.job.submit`, `sys.job.result`, `sys.job.progress`, `sys.job.dlq`, `sys.job.cancel`, `sys.heartbeat`, `sys.workflow.event`
- `job.*` (pool subjects), `worker.<id>.jobs` (direct)

Pointers and state:
- Contexts: `ctx:<job_id>` with pointer `redis://ctx:<job_id>`
- Results: `res:<job_id>` with pointer `redis://res:<job_id>`
- Artifacts: `art:<id>` with pointer `redis://art:<id>`
- Job metadata: `job:meta:<id>` plus per-state indices and `job:recent`
- Context engine memory: `mem:<memory_id>:*`
- Workflow runs: `wf:run:<run_id>` plus run indexes and timelines

Protocol:
- Bus and safety types are CAP v2 (`github.com/coretexos/cap/v2`, pinned in `go.mod`) via aliases in `core/protocol/pb/v1`.
- API/Context protos live in `core/protocol/proto/v1`; generated Go types live in `core/protocol/pb/v1` and `sdk/gen/go/coretex/v1`.

SDK:
- Public Go SDK lives under `sdk/` (module `github.com/yaront1111/coretex-os/sdk`), including generated protos, a minimal gateway client, and a CAP worker runtime (`sdk/runtime`).

## Topics -> pools (`config/pools.yaml`)

| Topic | Pool | Notes |
| --- | --- | --- |
| `job.default` | `default` | baseline pool for external workers |

Notes:
- Topics are config-driven; no core topics are enforced in code.

## Quickstart (Docker)

Requirements: Docker/Compose, curl, jq.

```bash
docker compose build
docker compose up -d
```

Dashboard (optional, part of compose): `http://localhost:8082` (uses `CORETEX_API_KEY`).

Platform smoke (create workflow + run + approve + delete):
```bash
./tools/scripts/platform_smoke.sh
```

CLI smoke (coretexctl):
```bash
./tools/scripts/coretexctl_smoke.sh
```

Inspect results:
```bash
docker exec coretex-redis-1 redis-cli get res:<job_id>
```

Pointer inspection via gateway:
- `GET /api/v1/memory?ptr=<urlencoded redis://...>`

## Development and tests

- Go toolchain: `go 1.24` (module-specified). Use local cache to avoid permission issues:
  `GOCACHE=$(pwd)/.cache/go-build go test ./...`
- Proto changes: edit `core/protocol/proto/v1`, then run `make proto`.
- Docker images: `docker compose build` rebuilds all services.
- Binaries: `make build` (all), or `make build SERVICE=coretex-scheduler` (one).
- Container image: `make docker SERVICE=coretex-scheduler` (uses the root Dockerfile).
- Smoke: `make smoke` (runs `tools/scripts/platform_smoke.sh`).
- Integration tests: `make test-integration` (opt-in, tagged).

## Config (selected env)

Core:
- `NATS_URL`, `REDIS_URL`
- `NATS_USE_JETSTREAM` (`0|1`), `NATS_JS_ACK_WAIT`, `NATS_JS_MAX_AGE`
- `POOL_CONFIG_PATH`, `TIMEOUT_CONFIG_PATH`, `CONTEXT_ENGINE_ADDR`
- `SAFETY_KERNEL_ADDR`, `SAFETY_POLICY_PATH`
- `JOB_META_TTL` / `JOB_META_TTL_SECONDS` (job meta retention)
- `REDIS_DATA_TTL` / `REDIS_DATA_TTL_SECONDS` (ctx/res retention)
- `WORKER_SNAPSHOT_INTERVAL`
- `CORETEX_LOG_FORMAT=json` (stdlib log, JSON output)

Gateway:
- `GATEWAY_GRPC_ADDR`, `GATEWAY_HTTP_ADDR`, `GATEWAY_METRICS_ADDR`
- `API_RATE_LIMIT_RPS`, `API_RATE_LIMIT_BURST`
- `TENANT_ID`
- API keys: `CORETEX_SUPER_SECRET_API_TOKEN`, `CORETEX_API_KEY`, or `API_KEY`
- CORS/WS: `CORETEX_ALLOWED_ORIGINS`, `CORETEX_CORS_ALLOW_ORIGINS`, `CORS_ALLOW_ORIGINS`
- TLS: `GRPC_TLS_CERT`, `GRPC_TLS_KEY`

Safety kernel:
- `SAFETY_KERNEL_ADDR`, `SAFETY_POLICY_PATH`
- TLS server: `SAFETY_KERNEL_TLS_CERT`, `SAFETY_KERNEL_TLS_KEY`
- TLS client: `SAFETY_KERNEL_TLS_CA`, `SAFETY_KERNEL_INSECURE`

Workflow engine:
- `WORKFLOW_ENGINE_HTTP_ADDR`, `WORKFLOW_ENGINE_SCAN_INTERVAL`, `WORKFLOW_ENGINE_RUN_SCAN_LIMIT`

## Observability

- Scheduler: `:9090/metrics`
- API gateway: `:9092/metrics`
- Workflow engine health: `:9093/health`

## Reset state (local)

- Wipe Redis (jobs + ctx/res + memory + workflows + config + DLQ):
  `docker compose exec redis redis-cli FLUSHALL`
- Wipe JetStream state too: `docker compose down -v` (removes `nats_data`) and then `docker compose up -d`

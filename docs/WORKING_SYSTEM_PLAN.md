# CortexOS – Working System V1 Plan

Target: move from MVP to a usable system with a real API, real memory fabric, a safety service, pool-aware scheduling, observability, and at least one non-echo worker. Preserve existing behavior while layering these capabilities.

## Definition of Working System V1
- Front door: API gateway accepts real prompts (HTTP/gRPC).
- Memory: `context_ptr` / `result_ptr` backed by Redis.
- Safety: external Safety Kernel service (gRPC) gates jobs.
- Scheduling: pool-aware, least-loaded dispatch using heartbeats.
- Observability: basic metrics and structured logs.
- Workers: at least one “real” worker beyond echo (e.g., LLM or log analyzer).

## Phased Work (in order)

### 1) Memory Fabric (Redis) and pointer usage
- Add `internal/infrastructure/memory` with a Redis-backed `Store`:
  - `PutContext/GetContext`, `PutResult/GetResult` with `context.Context`.
  - Config: `REDIS_URL` via existing config loader.
- Sender (`tools/scripts/send_echo_job.go`): write JSON payload to Redis, set `context_ptr` to that key.
- Worker (`cmd/cortex-worker-echo`): read `context_ptr` from Redis, log/process payload, write result to Redis, set `result_ptr` in `JobResult`.
- Tests: unit test for the store (can use a fake Redis or in-memory stub).

### 2) API Gateway (real front door)
- New proto: `api/proto/v1/api.proto` with `SubmitJob` and `GetJobStatus`.
- New binary: `cmd/cortex-api-gateway`:
  - `SubmitJob`: generate `job_id/trace_id`, store prompt in Redis (`context_ptr`), publish `JobRequest` to `sys.job.submit`, return IDs.
  - `GetJobStatus`: stub ok initially (optionally read status/result ptr from Redis).
- Build wiring: add Makefile target, keep `go build ./...` green.

### 3) Safety Kernel as a service
- New proto: `api/proto/v1/safety.proto` with `SafetyKernel.Check` returning `ALLOW`/`DENY` + reason.
- New binary: `cmd/cortex-safety-kernel` (gRPC server) that denies `topic=sys.destroy`, allows others.
- Scheduler: replace `SafetyStub` with `SafetyClient` (gRPC). Config: `SAFETY_KERNEL_ADDR`. On timeout/failure, deny or fail closed. Add a small client test with a fake server.

### 4) Pool-aware, least-loaded scheduling
- Extend `Heartbeat` proto (append fields): `string pool = 8;`, `int32 max_parallel_jobs = 9;` (rerun `make proto`).
- Echo worker: populate `pool="echo"`, `max_parallel_jobs=1`.
- Registry: support grouping by pool; add helper `WorkersForTopic(topic)` if needed.
- Strategy: add `LeastLoadedStrategy` (pick worker with lowest `active_jobs`/`cpu_load` in the pool); swap scheduler wiring from `NaiveStrategy` to this.
- Tests: cover strategy selection and registry helpers.

### 5) Observability (metrics + structured logs)
- Define scheduler `Metrics` interface: `IncJobsReceived/Dispatched/Completed`, `IncSafetyBlocked`.
- Provide no-op impl now; optional Prometheus impl under `internal/infrastructure/metrics`.
- Wire metrics calls into `HandlePacket` and `processJob`.
- Add a small logging helper for structured fields (wrap stdlib `log`).

### 6) Add one “real” worker
- Pick a target (e.g., `job.chat.llm` or log analyzer).
- Worker reads `context_ptr` from Redis, performs real work (LLM call or log parse), writes output to `result_ptr`, publishes `JobResult`.
- Heartbeats set the appropriate `pool` and capacity.

## Validation Checklist
- `make proto` whenever proto files change.
- `GOMODCACHE=.gomodcache GOCACHE=.gocache /usr/local/go/bin/go test ./...`
- Local flow smoke test:
  1) `docker run --rm -p 4222:4222 nats:latest`
  2) start Redis locally (`redis-server`) or via Docker.
  3) run scheduler, safety kernel, API gateway, worker(s).
  4) `curl`/`grpcurl` to `SubmitJob`; verify context/result stored in Redis and `JobResult` logs/metrics emitted.

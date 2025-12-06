# CortexOS MVP – Current State

This document captures the control-plane/worker MVP as it exists in the repo today. It is grounded in the checked-in code and the MVP delivery plan.

## Snapshot
- Bus: NATS (plain subjects, no JetStream yet) via `internal/infrastructure/bus`.
- Contracts: `api/proto/v1` with generated Go in `pkg/pb/v1/api/proto/v1` and a thin alias package `pkg/pb/v1`.
- Scheduler: `cmd/cortex-scheduler` using `internal/scheduler` (safety stub + naive strategy + memory registry).
- Worker: Echo worker in `cmd/cortex-worker-echo` consuming `job.echo` and emitting results/heartbeats.
- Sender script: `tools/scripts/send_echo_job.go` publishes a `JobRequest` to `sys.job.submit`.

## Core Flow
1. Sender publishes `BusPacket{JobRequest}` to `sys.job.submit`.
2. Scheduler (queue `cortex-scheduler`) handles:
   - Heartbeats on `sys.heartbeat.>` → registry update.
   - Job requests on `sys.job.submit` → safety check (`SafetyStub` blocks `sys.destroy`), naive subject pick (`req.Topic`), then republishes `JobRequest` to that subject.
   - Job results on `sys.job.result` → logs completion.
3. Echo worker (queue `workers-echo`) consumes `job.echo`, simulates work, and publishes `JobResult` to `sys.job.result`.
4. Echo worker sends a heartbeat every 5s to `sys.heartbeat.echo` until shutdown (cancellable loop).

## NATS Subjects (current)
- `sys.job.submit` – inbound job requests to the scheduler.
- `sys.job.result` – job completions.
- `sys.heartbeat.>` – worker heartbeats (echo uses `sys.heartbeat.echo`).
- `job.echo` – echo worker queue subject.

## Components and Behavior
- Scheduler (`internal/scheduler`):
  - Validates `JobRequest` has `job_id` and `topic` before scheduling.
  - Safety: `SafetyStub` denies only `topic=sys.destroy`.
  - Strategy: `NaiveStrategy` returns `req.Topic`.
  - Registry: in-memory map of latest heartbeats per worker.
  - Logging uses stdlib `log`; no panics in library code.
- Bus (`internal/infrastructure/bus`):
  - Protobuf payloads over NATS.
  - Reconnect/backoff options and lifecycle logging enabled.
- Worker (`cmd/cortex-worker-echo`):
  - Cancellable heartbeat goroutine (context + waitgroup).
  - Job handler sleeps 100–500ms, returns `JobResult` with execution time.

## Build and Run Locally
Prereqs: Go 1.22+, Docker (for NATS).

```bash
# 1) Start NATS
docker run --rm -p 4222:4222 nats:latest

# 2) Build
make build-scheduler
make build-worker-echo

# 3) Run scheduler (in one shell)
./bin/cortex-scheduler

# 4) Run echo worker (in another shell)
./bin/cortex-worker-echo

# 5) Send a test job (third shell)
go run ./tools/scripts/send_echo_job.go
```

Expected log shape:
- Scheduler: job received → dispatch to `job.echo` → job result logged; heartbeats logged.
- Worker: job received → result published → heartbeat logs until exit.

## Testing
All packages have passing tests. To run locally without writing outside the repo, set local caches:

```bash
GOMODCACHE=.gomodcache GOCACHE=.gocache /usr/local/go/bin/go test ./...
```

## Known Limitations (MVP)
- Strategy is naive (no load/region awareness).
- Safety is a stub (topic allowlist/deny only).
- No persistence/JetStream; registry is in-memory only.
- No auth/RBAC on the bus.
- No structured logging or metrics yet.

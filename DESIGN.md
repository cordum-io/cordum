# Cordum Control Plane Design (CAP v2)

Date: 2026-01-23
Status: Public technical disclosure (non-legal)

This document describes the Cordum control plane and its wire contracts to
establish clear prior art for the protocol and orchestration design. It is
derived from the public code in this repository and the CAP v2 protocol
definitions used by Cordum (`github.com/cordum-io/cap/v2`, pinned by `go.mod`).

## 1) Abstract

Cordum is a governance-first control plane for autonomous agents and external
workers. It separates governance (policy, approval, constraints) from execution
(workers) using a durable bus and a stable wire protocol (CAP v2). The core
idea is that every unit of work is a **contract** that includes budget and
policy constraints, and every worker continuously advertises its live capacity
so the scheduler can route without a persistent worker database.

## 2) System Components

- **API Gateway**: Accepts requests, writes input context to Redis, emits
  `BusPacket{JobRequest}` to the bus, exposes HTTP/WS/gRPC APIs.
- **Safety Kernel**: gRPC policy service returning allow/deny/approval plus
  structured constraints (budgets, sandboxing, toolchain limits, diffs).
- **Scheduler**: Subscribes to bus subjects, checks policy, routes jobs to
  pools or direct workers, tracks job state, and enforces timeouts.
- **Workflow Engine**: Emits job steps as `JobRequest` and advances runs.
- **Workers**: Subscribe to job subjects, read context pointers, execute, emit
  `JobResult` with result/artifact pointers, and send heartbeats.
- **Redis**: Stores workflow state, job metadata, and pointer payloads.
- **NATS**: Transport for CAP v2 packets (with optional JetStream durability).

## 3) CAP v2 Wire Contracts (selected fields)

CAP v2 is the canonical protocol; Cordum does not duplicate these definitions.

### 3.1 BusPacket (Envelope)

`BusPacket` carries all bus traffic:
- `trace_id`, `sender_id`, `created_at`, `protocol_version`
- `payload` oneof: `JobRequest`, `JobResult`, `Heartbeat`, `SystemAlert`,
  `JobProgress`, `JobCancel`, `Handshake`
- `signature` (reserved for packet signing; present in the schema)

### 3.2 Heartbeat (Capacity Signal)

Workers broadcast `Heartbeat` on `sys.heartbeat`, including live load and
placement hints:
- `worker_id`, `region`, `type`
- `cpu_load`, `memory_load`, `gpu_utilization`
- `active_jobs`, `max_parallel_jobs`
- `capabilities` (freeform strings)
- `pool` (routing pool name)
- `labels` (placement metadata)

The scheduler keeps an **in-memory registry with TTL** (default 30s). This
avoids a persistent worker database while still enabling capacity-aware routing.

### 3.3 JobRequest (Budgeted Contracts)

`JobRequest` is the schedulable unit of work:
- Identity + routing: `job_id`, `topic`, `priority`
- Pointers: `context_ptr` (input pointer), `memory_id`
- Budgets: `budget` (`max_input_tokens`, `max_output_tokens`,
  `max_total_tokens`, `deadline_ms`)
- Tenant + principal: `tenant_id`, `principal_id`, `labels`
- Metadata: `meta` (`capability`, `risk_tags`, `requires`, `pack_id`, etc.)

### 3.4 Policy Constraints (Safety Kernel)

The Safety Kernel returns `PolicyCheckResponse` with `PolicyConstraints`:
- `BudgetConstraints`: `max_runtime_ms`, `max_retries`,
  `max_artifact_bytes`, `max_concurrent_jobs`
- `SandboxProfile`: filesystem + network allow/deny hints
- `ToolchainConstraints`: allowed tools/commands
- `DiffConstraints`: max files/lines + path filters
- `remediations`: optional safer alternatives with replacement topic/capability

The scheduler merges these constraints into dispatch behavior (timeouts,
retry limits, concurrency caps).

### 3.5 JobResult / JobProgress / JobCancel

- `JobResult`: `job_id`, `status`, `result_ptr`, `worker_id`, `execution_ms`,
  optional `error_code`/`error_message`, `error_code_enum` (structured `ErrorCode`), `artifact_ptrs`
- `JobProgress`: `percent`, `message`, optional `result_ptr`/`artifact_ptrs`
- `JobCancel`: `job_id`, `reason`, `requested_by`

### 3.6 Handshake (CAP v2.5.2)

Services publish `BusPacket{Handshake}` on `sys.handshake` at startup:
- `component_id`, `role` (`ComponentRole` enum: GATEWAY, SCHEDULER, WORKER, ORCHESTRATOR, CONTROLLER)
- `supported_versions` (protocol versions), `capabilities` (bool map), `sdk_version`

The scheduler uses Handshake messages to maintain a component registry alongside the heartbeat-based worker registry.

### 3.7 ErrorCode Enum (CAP v2.5.2)

Structured error classification replacing ad-hoc string codes:
- Protocol errors (100-105): version mismatch, malformed packet, signature issues
- Job errors (200-206): not found, timeout, permission denied, resource exhausted
- Safety errors (300-302): denied, policy violation, risk tag blocked
- Transport errors (400-402): publish failed, connection lost

### 3.8 Enhanced SystemAlert (CAP v2.5.2)

`SystemAlert` now includes structured fields: `severity` (enum), `error_code_enum`,
`source_component`, `details` (map), `trace_id`. Deprecated string fields (`level`,
`component`, `code`) remain populated for backward compatibility.

## 4) Pointer-Based State Separation

The bus carries **pointers**, not large payloads. Input, output, and artifacts
live in Redis and are referenced by pointers:

- `context_ptr` -> `redis://ctx:<job_id>`
- `result_ptr`  -> `redis://res:<job_id>`
- `artifact_ptrs` -> `redis://art:<id>`

This keeps bus payloads small, preserves durability, and allows audit tooling
to dereference pointers when needed (`GET /api/v1/memory?ptr=...`).

## 5) Control Plane Flow (High Level)

1. Gateway writes context JSON to Redis, sets `context_ptr`, publishes
   `BusPacket{JobRequest}` to `sys.job.submit`.
2. Scheduler receives `JobRequest`, records state, calls Safety Kernel, and
   applies `PolicyConstraints`.
3. Scheduler selects a pool and/or direct worker subject using heartbeats
   and publishes to `job.*` or `worker.<id>.jobs`.
4. Worker fetches `context_ptr`, executes, writes result to Redis, publishes
   `BusPacket{JobResult}` with `result_ptr` and `artifact_ptrs`.
5. Scheduler updates state to terminal and stores pointers.

Time-based enforcement uses reconciler timeouts (e.g., `config/timeouts.yaml`)
and cancellation uses `BusPacket{JobCancel}` on `sys.job.cancel`.

## 6) What Is Novel Here (From the Implementation)

1. **Budget-aware contracts at the wire level**: budgets are embedded in
   `JobRequest` and policy-enforced via `BudgetConstraints` returned by the
   Safety Kernel. This moves cost and runtime limits into the protocol itself.
2. **Capacity-aware routing without a persistent registry**: the scheduler
   relies on live `Heartbeat` packets and an in-memory TTL registry for worker
   state, enabling distributed routing decisions without a backing DB.
3. **Pointer-first bus design**: large context, results, and artifacts stay in
   Redis and are referenced by pointers in the wire protocol.

## 7) Related Implementation References

- CAP v2 proto definitions (module pinned by `go.mod`)
- `docs/AGENT_PROTOCOL.md` for bus subjects and pointer semantics
- `core/controlplane/scheduler/registry_memory.go` for heartbeat registry
- `core/controlplane/safetykernel` for policy evaluation and constraints


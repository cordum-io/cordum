# ADR-001: Policy-Before-Dispatch Guarantee

- Status: Accepted
- Date: 2026-01-10

## Context

Cordum orchestrates AI agent actions across untrusted worker pools. Without a
hard gate, jobs could reach workers before policy evaluation completes, creating
a window where unsafe actions execute unchecked.

We need an enforcement point that:
- cannot be bypassed by fast job submission
- adds minimal latency to the dispatch hot path
- provides deterministic allow/deny/require_approval/throttle outcomes

## Decision

The Safety Kernel evaluates every job **synchronously** before the scheduler
dispatches it. This is the "policy-before-dispatch" guarantee.

Implementation:
1. Gateway submits job to scheduler via NATS.
2. Scheduler calls `SafetyKernel.Evaluate()` over gRPC before dispatch.
3. If evaluation fails or times out, the circuit breaker trips (see [ADR-006](006-circuit-breaker-safety.md)).
4. Only `ALLOW` or `ALLOW_WITH_CONSTRAINTS` results trigger dispatch.
5. `REQUIRE_APPROVAL` places the job in a human approval queue.
6. `DENY` and `THROTTLE` are terminal — job never reaches a worker.

Performance target: **< 5 ms p99** for policy evaluation. Achieved by:
- In-memory policy cache in the Safety Kernel (no Redis/disk on hot path)
- Policy reloaded asynchronously on NATS events or file change
- Matcher uses indexed capability/tag sets, not linear scans

Key source files:
- `core/controlplane/safetykernel/kernel.go` — Evaluate/Explain/Simulate
- `core/controlplane/scheduler/engine.go` — pre-dispatch evaluation call
- `core/infra/config/safety_policy.go` — policy loading and parsing

## Consequences

Positive:
- Zero-trust guarantee: no agent action executes without policy approval
- Deterministic audit trail — every job has a recorded safety decision
- Policy changes take effect immediately via cache reload

Tradeoffs:
- Scheduler throughput is bounded by Safety Kernel latency
- Safety Kernel is a single point of failure (mitigated by circuit breaker)
- Complex policies may challenge the 5 ms budget (require profiling)

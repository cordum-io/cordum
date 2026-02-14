# ADR-006: Circuit Breaker on Safety Kernel Client

- Status: Accepted
- Date: 2026-01-15

## Context

The Safety Kernel is a synchronous dependency on the scheduler hot path (see
[ADR-001](001-safety-before-dispatch.md)). If the Safety Kernel becomes
unavailable or slow, the scheduler cannot dispatch any jobs. Without a fallback
mechanism, a Safety Kernel outage causes total platform unavailability.

## Decision

The scheduler's Safety Kernel client uses a **circuit breaker** pattern with
configurable thresholds.

### States

```
CLOSED (normal) → OPEN (tripped) → HALF-OPEN (probe)
        ↑                                    │
        └────────────────────────────────────┘
```

- **Closed**: All requests forwarded to Safety Kernel normally.
- **Open**: After N consecutive failures, the circuit trips. Requests are
  short-circuited with a fallback decision (configurable: deny-all or allow-all).
- **Half-Open**: After a cooldown period, one probe request is sent. If it
  succeeds, the circuit closes. If it fails, the circuit reopens.

### Configuration

| Parameter | Default | Description |
|-----------|---------|-------------|
| Failure threshold | 5 | Consecutive failures before tripping |
| Cooldown period | 30s | Time in open state before probing |
| Timeout per call | 100ms | gRPC deadline for Evaluate calls |
| Fallback mode | `deny` | Decision when circuit is open (`deny` or `allow`) |

### Fallback Behavior

- **deny** (default, production): All jobs are denied while circuit is open.
  Safest option — no unchecked actions execute.
- **allow** (opt-in, dev): Jobs dispatch without policy check while circuit is
  open. Used only for local development where availability matters more than
  safety.

### Observability

- `cordum_safety_circuit_state` gauge (0=closed, 1=open, 2=half-open)
- `cordum_safety_circuit_trips_total` counter
- `cordum_safety_evaluate_errors_total` counter with `reason` label

Key source files:
- `core/controlplane/scheduler/engine.go` — circuit breaker integration
- `core/infra/metrics/metrics.go` — circuit breaker metrics

## Consequences

Positive:
- Platform degrades gracefully instead of hanging on Safety Kernel outage
- Configurable fallback lets operators choose safety vs availability tradeoff
- Automatic recovery when Safety Kernel comes back (no manual intervention)
- Observability signals enable alerting on circuit state changes

Tradeoffs:
- deny-all fallback means platform stops processing during outage
- allow-all fallback means unchecked actions can execute (risk)
- Circuit breaker adds complexity to the evaluation path
- Probe requests during half-open may see stale results

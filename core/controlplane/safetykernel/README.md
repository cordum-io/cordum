# Safety Kernel

The Safety Kernel evaluates every `PolicyCheckRequest` against the active policy
bundle and returns an allow/deny/require_approval/throttle decision to the
caller. It runs as a standalone gRPC service (`cmd/cordum-safety-kernel`) with
a small admin HTTP surface for health probes.

This README covers the env knobs that are not obvious from reading the code.
For architectural background see `docs/system_overview.md`.

## Phase-2 shadow evaluation

Every active policy can have a **shadow bundle** attached — a candidate policy
whose results are evaluated in parallel with the active policy but whose
outcomes are emitted as `shadow_eval` audit events and never affect the
actual decision.

### Configuration

| Env var | Default | Purpose |
|---------|---------|---------|
| `CORDUM_SHADOW_EVAL_DISABLED` | `false` | Set to `true` / `1` / `yes` / `on` to disable dual evaluation entirely. The kernel still boots and serves active-policy decisions; the shadow loader and worker pool are never constructed. Use this as an emergency kill-switch when a shadow policy is poisoning a kernel worker or saturating the audit bus. |

Runtime state:
- `cordum_shadow_eval_total{decision,diff}` — number of shadow events emitted, labelled by diff class.
- `cordum_shadow_eval_dropped_total{reason}` — dropped submissions (queue full, closed evaluator).
- `cordum_shadow_eval_queue_depth` — live queue depth gauge.
- `cordum_shadow_eval_duration_seconds` — shadow-evaluate latency histogram.

### How it works

1. The kernel bootstrap (`RunWithEntitlements` in `kernel.go`) constructs a
   `ShadowLoader` backed by `policyshadow.Store` and a `ShadowEvaluator` with
   a bounded worker pool (64 workers, 1000-slot queue, 30 s per-submission
   timeout).
2. On every `PolicyCheckRequest`, after the active decision is finalised,
   `evaluate()` calls `shadowEvaluator.Submit(activeDecision, input, tenant,
   jobID)` — **non-blocking**. The queue drops overflow and increments the
   dropped counter.
3. Workers compile each tenant's shadow bundles (refreshed every 15 s) and
   run `shadow.Evaluate(input)` under panic recovery. The diff between active
   and shadow (`escalated | relaxed | approval_differ | unchanged`) is
   computed and emitted as an `shadow_eval` audit event via the NATS audit
   publisher; the gateway's audit chain picks it up like any other event.

### Invariants

- **The active decision is NEVER affected by shadow evaluation.** The integration
  test `shadow_integration_test.go` pins this by asserting that toggling the
  shadow on and off leaves the active response byte-identical.
- A malformed shadow YAML is logged + skipped — other shadows for the same
  tenant remain visible.
- Queue overflow drops are observable via `cordum_shadow_eval_dropped_total`.
- Kernel shutdown drains the worker pool before returning.

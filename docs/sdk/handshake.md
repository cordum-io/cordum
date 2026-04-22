# SDK Worker Handshake

> Phase-2 boundary hardening. Related: heartbeat demotion (`docs/architecture/heartbeat-demotion.md`), audit chain (task-2497391e), topic registry (task-436f67e1).

## 1. Why this exists

Before Phase-2 the scheduler had no cryptographic trust anchor for workers. Every packet a worker published was accepted on the strength of its NATS-subject alone — if the bus would deliver it, the scheduler would process it. Memory-surfaced audit of the live fleet found that **zero workers** were sending the legacy cap `Handshake` packet; the dispatch pipeline had been running without authoritative worker identity for as long as production has existed.

That is not a theoretical risk. Without handshake + session tokens:

- A compromised NATS account could impersonate any worker.
- Revoking a worker required tearing down the whole identity — there was no per-session kill switch.
- Audit events could not reliably tie a `JobResult` to a specific worker boot session.

The Phase-2 handshake closes this gap. On `Agent.Start()` the worker asserts its identity, the scheduler mints a short-lived Ed25519-signed session token, and every subsequent outbound packet carries that token. The scheduler's middleware verifies the signature + expiry + revocation state on every packet.

## 2. What changes from the worker's point of view

**Nothing, if the SDK is upgraded.** The handshake is implicit in `Agent.Start()`. Workers set a few agent config fields and the runtime does the rest:

```go
agent := &runtime.Agent{
    SenderID:      "my-worker",
    Tenant:        "my-tenant",
    SDKVersion:    "cap-go/v2.9.1",
    HandshakeMode: runtime.HandshakeModeEnforce,
}
// Register handlers...
if err := agent.Start(); err != nil {
    log.Fatal(err) // handshake rejection, network partition, etc.
}
```

Workers that do not upgrade keep working under the operator's rollout mode (see §3). Only `enforce` mode refuses pre-handshake packets.

## 3. Operator rollout

The scheduler-side enforcement is controlled by `CORDUM_SDK_HANDSHAKE`. Three phases:

| Mode | Scheduler behaviour | When to use |
|---|---|---|
| `off` | Middleware is a no-op. Every packet passes regardless of token state. | Initial release — validates the dashboards + Prometheus gauges are working without touching traffic. |
| `warn` (default) | Tokens are verified when present. Handshakeless packets log a single ERROR per worker per hour and pass through. | Main migration phase. Watch the `handshake missing` error rate drop to zero as the fleet upgrades. |
| `enforce` | Every inbound packet must carry a valid + non-revoked session token. | Target state once the fleet has upgraded. |

The Agent-side `HandshakeMode` field is a parallel control on the worker side: `off` = skip handshake entirely; `warn` = attempt but tolerate failure; `enforce` = fail `Start()` on persistent handshake failure.

### Suggested rollout timeline

| Week | Scheduler mode | Worker SDK mode | Expected outcome |
|---|---|---|---|
| 0 | `off` | `off` | Observe metrics + dashboards; no behaviour change. |
| 1–2 | `warn` | `off` | Handshakeless pass-through; scheduler-side rate-limited logs appear. |
| 3–4 | `warn` | `warn` | Adapters upgrade; handshake attempts flow; failures visible. |
| 5+ | `enforce` | `enforce` | Full trust enforcement; any misconfig is a boot-time error. |

## 4. Session-token lifecycle

### Issue

On `Agent.Start()` the worker builds a `HandshakeRequest` and sends it via NATS request/reply on `sys.worker.handshake`. The scheduler:

1. Validates the request shape (all fields present, nonce ≥ 16 bytes).
2. Verifies the clock skew is within `WorkerHandshakeMaxSkew` (60s).
3. Claims the nonce in Redis (`session:nonce:<tenant>:<nonce>` SETNX with TTL ≥ 2 × skew) to prevent replay.
4. Looks up the agent in `AgentIdentityStore`.
5. Confirms the request tenant matches the identity record.
6. Mints an Ed25519-signed session token via `SessionTokenIssuer.Issue`.
7. Returns a `HandshakeResponse` with `SessionToken` + `TokenExp`.

The worker stores the token on its `Agent.session` state and attaches it to every outbound packet via unknown field 18 on `BusPacket`.

### Renew

A background goroutine on the Agent calls `performRenew` at `exp - lifetime/2` (~30 min for the default 1h token). The renew subject is `sys.worker.handshake.renew`; the payload is a fresh `HandshakeRequest`. On success the Agent rotates its stored token + exp; on rejection it falls back to a fresh handshake.

### Revoke

An admin calls `POST /api/v1/workers/<id>/revoke-session`. The gateway handler:

1. Enforces the `admin` role via `s.auth.RequireRole`.
2. Resolves the tenant from the auth context + `X-Tenant-ID`.
3. Invokes `SessionTokenIssuer.RevokeByAgent` which writes `session:revoked:<tenant>:<jti>` in Redis with TTL matching exp.
4. Emits two SIEMEvents: `worker_handshake{outcome=revoked}` + `worker_trust_change{reason=session_revoked}`.

Subsequent packets from that worker fail `SessionTokenMiddleware.Verify` with `RejectInvalid`. In `enforce` mode the packet is dropped.

## 5. Debugging a rejected handshake

The scheduler emits a structured log + SIEMEvent on every rejection. The `reason` field is one of the `HandshakeReject*` constants from `cap/sdk/go/handshake.go`:

| Reason | Cause | Remediation |
|---|---|---|
| `unknown_agent` | `agent_id` not in `AgentIdentityStore` | Register the agent via the dashboard or `POST /api/v1/agents`. |
| `tenant_mismatch` | Request `tenant` does not match the identity's `Owner` field. | Fix the worker config so `Tenant` matches the registered owner. |
| `replay_detected` | Nonce already claimed in the Redis nonce store. | Confirm the worker is not replaying a cached request (clock moved backwards, cached outbound packet). |
| `clock_skew` | Request timestamp is > `WorkerHandshakeMaxSkew` (60s) from scheduler clock. | Check NTP on the worker host. |
| `capability_denied` | Identity status is `suspended` or `revoked`. | Re-activate the identity via the dashboard. |
| `sdk_too_old` | Scheduler refuses a deprecated SDK version. | Upgrade the adapter / SDK to the minimum version below. |
| `malformed_request` | Parse error or missing required field. | Upgrade the SDK — this indicates a version-mismatch bug. |
| `invalid_signature` | Token signature failed verification. | Rotate signing keys; check trust store config. |
| `internal_error` | Identity lookup / nonce store transient failure. | Check scheduler logs; usually a Redis hiccup. |

The dashboard's Agent Registry page surfaces the reason on the agent row so operators can diagnose without grepping logs.

## 6. Minimum SDK versions

| SDK | Minimum version | Handshake field default |
|---|---|---|
| `cap-go` (Go) | v2.9.1 | `HandshakeMode = off` |
| `cap-py` (Python) | v2.9.1 | `handshake_mode = "off"` |
| `cap-node` (TypeScript) | v2.9.1 | `handshakeMode: "off"` |
| `cordum-adapters` (Python framework) | v0.3.0 | Inherits cap-py default |

Operators flipping to `enforce` on the scheduler side should verify the fleet's min version meets these thresholds first. The `/api/v1/workers` response carries `session_state` per worker — any row with `session_state == "trust_store_unready"` means the gateway isn't wired to verify tokens; a row with `session_state == "no_session"` means the worker hasn't upgraded yet.

## 7. Further reading

- `cap/sdk/go/handshake.go` — wire-format types.
- `cap/sdk/go/runtime/handshake.go` — Agent-side handshake driver.
- `cap/sdk/go/runtime/renew.go` — auto-renew loop.
- `core/controlplane/scheduler/handshake_handler.go` — scheduler-side handler.
- `core/controlplane/scheduler/session_token.go` — JWS-like token issuer.
- `core/controlplane/scheduler/token_middleware.go` — inbound-packet verifier.
- `core/controlplane/gateway/handlers_workers.go` — revoke endpoint.
- `docs/architecture/heartbeat-demotion.md` — the sibling rollout that pairs with this.

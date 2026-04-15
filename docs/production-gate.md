# Production Gate

`tools/scripts/production_gate.sh` is the release-candidate gate runner for local and CI validation.
It executes 19 production-readiness gates classified as **blocking** or **advisory**, and exits non-zero if any blocking gate fails.

## What It Tests

1. `Gate 1 Deploy`: clean rebuild, health readiness, and smoke workflow.
2. `Gate 2 Auth/Tenant`: API key/JWT auth checks and tenant isolation.
3. `Gate 3 Workflow Matrix`: auto, approval, deny+DLQ, cancel, and rerun flows.
4. `Gate 4 Policy`: evaluate/simulate/explain endpoints and remediation traceability.
5. `Gate 5 Reliability`: scheduler and gateway restart recovery plus idempotency.
6. `Gate 6 Performance`: latency/error-rate SLO checks under sustained load.
7. `Gate 7 Security`: rate limiting, redaction, headers, malformed input, and payload-size limits.
8. `Gate 8 Extensions`: MCP server, config merging, resource/tool listing.
9. `Gate 9 Identity/Access`: user CRUD, session tokens, API key management.
10. `Gate 10 Data Lifecycle`: DLQ entries, artifact storage, schema validation.
11. `Gate 11 Streaming`: WebSocket connection, SSE events, real-time decisions.
12. `Gate 12 Adv Workflows`: chat workflows, approval flows, job cancellation.
13. `Gate 13 Config Hierarchy`: config overrides, org/team budget inheritance.
14. `Gate 14 Policy Lifecycle`: bundle management, snapshots, simulation.
15. `Gate 15 Pack Management`: pack install, upgrade, marketplace listing.
16. `Gate 16 Degradation`: graceful degradation under partial failures, gRPC fallback.
17. `Gate 17 Dashboard`: HTML serving, static assets, CSP headers, SPA fallback.
18. `Gate 20 Connection Metrics`: WebSocket metric export and `/status` uptime validation.
19. `Gate 21 Infrastructure Health`: Live `/health` endpoint checks (NATS connected, Redis ok).

## Gate Classification

Gates are classified as **blocking** (security/correctness fundamentals) or **advisory** (optional features, non-critical paths). Only blocking gate failures cause a non-zero exit code in the default mode.

| Gate | Name | Class | What It Tests | Common Failure Causes | Remediation |
|------|------|-------|---------------|----------------------|-------------|
| 1 | Deploy | **BLOCKING** | Docker Compose build, health readiness, smoke workflow | Services fail to start, port conflicts, missing images | Check `docker compose logs`, verify all images build, confirm ports 8080-8081 are free |
| 2 | Auth/Tenant | **BLOCKING** | API key validation, JWT auth, tenant isolation | Missing `CORDUM_API_KEY`, JWT secret not configured, tenant middleware misconfigured | Set `CORDUM_API_KEY`, verify auth middleware chain, check tenant header handling |
| 3 | Workflow Matrix | **BLOCKING** | Auto/approval/deny/cancel/rerun workflow paths | Mock-bank pack not installed, worker not registered, policy not ready | Run `cordumctl pack install`, verify worker pool config in `pools.yaml`, check policy bundle |
| 4 | Policy | **BLOCKING** | Policy evaluate/simulate/explain, audit trail | Demo guardrails pack missing, safety kernel unreachable | Install demo-guardrails pack, verify safety-kernel is running on port 50051 |
| 5 | Reliability | **BLOCKING** | Scheduler/gateway restart recovery, idempotency | Services don't recover after restart, idempotency keys not honored | Check Redis persistence, verify scheduler lock TTLs, review heartbeat config |
| 6 | Performance | ADVISORY | Latency P95 and error rate under load | High latency, resource exhaustion | Tune `PERF_CONCURRENCY`, check resource limits, review connection pool settings |
| 7 | Security | **BLOCKING** | Rate limiting, redaction, headers, input validation | Rate limiter not configured, security headers missing, oversized payloads accepted | Configure rate limits in `system.yaml`, verify middleware chain order, check `maxBodyMiddleware` |
| 8 | Extensions | ADVISORY | MCP server, config merging, resource listing | MCP server not running, config merge conflicts | Start cordum-mcp service, verify MCP routes in gateway |
| 9 | Identity/Access | **BLOCKING** | User CRUD, session tokens, API key lifecycle | User store not initialized, weak session token generation | Check user store Redis keys, verify `crypto/rand` usage for tokens |
| 10 | Data Lifecycle | ADVISORY | DLQ entries, artifact storage, schemas | DLQ stream not created, artifact store misconfigured | Verify NATS JetStream streams, check Redis artifact keys |
| 11 | Streaming | ADVISORY | WebSocket, SSE, real-time decisions | WebSocket upgrade fails, SSE endpoint not responding | Check gateway WebSocket handler, verify `/api/v1/stream` route |
| 12 | Adv Workflows | ADVISORY | Chat workflows, approval, cancellation | Workflow engine not running, chat endpoint missing | Verify workflow-engine service, check chat handler registration |
| 13 | Config Hierarchy | ADVISORY | Config overrides, org/team budgets | Config service cache stale, Redis key `cfg:system:default` corrupted | Delete config cache key, restart config service |
| 14 | Policy Lifecycle | ADVISORY | Bundle CRUD, snapshots, simulation | Bundle store empty, snapshot endpoint not wired | Install policy bundles, check policy bundle handler routes |
| 15 | Pack Management | ADVISORY | Pack install/upgrade, marketplace | Marketplace URL unreachable, pack validation failing | Check `validateMarketplaceURL`, verify pack store |
| 16 | Degradation | ADVISORY | Graceful degradation, gRPC fallback | gRPC services unreachable, fallback not implemented | Verify gRPC health checks, check circuit breaker config |
| 17 | Dashboard | ADVISORY | HTML, static assets, CSP, SPA fallback | Dashboard not built, static file serving misconfigured | Run `npm run build` in dashboard, verify nginx/gateway static routes |
| 20 | Connection Metrics | ADVISORY | WebSocket `cordum_gateway_ws_clients_active` metric exported, `/status` returns valid `uptime_seconds` | Metrics endpoint unreachable, WS metrics not registered | Verify gateway is running with `/metrics` on port 9092, check Prometheus metric registration |
| 21 | Infrastructure Health | **BLOCKING** | `/health` returns 200 with `nats=connected` and `redis=ok` | NATS or Redis down, health handler not wired | Check NATS/Redis containers, verify health handler registration in gateway |

## Prerequisites

- Docker + Docker Compose plugin.
- `curl`, `jq`, `go`, and `openssl`.
- A valid `CORDUM_API_KEY`.
- Gateway reachable at `CORDUM_API_BASE` (defaults to `http://localhost:8081`).

## Usage

Run all gates:

```bash
CORDUM_API_KEY=${CORDUM_API_KEY:?set CORDUM_API_KEY} \
./tools/scripts/production_gate.sh
```

Run one gate:

```bash
CORDUM_API_KEY=${CORDUM_API_KEY:?set CORDUM_API_KEY} \
./tools/scripts/production_gate.sh --gate 3
```

Skip full rebuild in Gate 1:

```bash
CORDUM_API_KEY=${CORDUM_API_KEY:?set CORDUM_API_KEY} \
./tools/scripts/production_gate.sh --skip-rebuild
```

Run in strict mode (all gates blocking, for release pipelines):

```bash
CORDUM_API_KEY=${CORDUM_API_KEY:?set CORDUM_API_KEY} \
./tools/scripts/production_gate.sh --strict
```

Strict mode can also be enabled via environment variable:

```bash
STRICT_MODE=1 ./tools/scripts/production_gate.sh
```

## Environment Variables

Core:

- `CORDUM_API_KEY`: required.
- `CORDUM_API_BASE`: default `http://localhost:8081`.
- `CORDUM_TENANT_ID`: default `default`.
- `CORDUM_ORG_ID`: default `CORDUM_TENANT_ID`.
- `REDIS_URL`: default `redis://:${REDIS_PASSWORD}@localhost:6379` (`REDIS_PASSWORD` is required).
- `NATS_URL`: default `nats://localhost:4222`.
- `STRICT_MODE`: set to `1` to make all gates blocking (same as `--strict` flag).

Performance gate tuning:

- `PERF_CONCURRENCY`: default `20`.
- `PERF_TIMEOUT_SEC`: default `180`.
- `PERF_P95_MS`: default `20000`.
- `PERF_ERROR_RATE_MAX_PERCENT`: default `5`.

Security gate tuning:

- `RATE_LIMIT_BURST_REQUESTS`: default `5000`.
- `RATE_LIMIT_PARALLEL`: default `200`.

Results:

- `RESULTS_FILE`: output file for JSON summary (default `production_gate_results.json`).

## Output

The script prints a summary table with classification and writes JSON:

- `production_gate_results.json`

JSON structure:

```json
{
  "generated_at": "2026-02-13T00:00:00Z",
  "api_base": "http://localhost:8081",
  "tenant_id": "default",
  "selected_gate": "all",
  "all_passed": true,
  "blocking_passed": true,
  "strict_mode": false,
  "gates": [
    {
      "gate": 1,
      "name": "Gate 1 Deploy",
      "status": "PASS",
      "duration_ms": 12345,
      "message": "quickstart/smoke/health checks passed",
      "class": "BLOCKING"
    }
  ]
}
```

Key fields:
- `all_passed`: `true` if every gate (blocking and advisory) passed.
- `blocking_passed`: `true` if all blocking gates passed. This is the release-readiness signal.
- `strict_mode`: `true` if `--strict` was used (all gates treated as blocking).
- `class`: per-gate classification (`BLOCKING` or `ADVISORY`).

## CI Integration

The nightly workflow runs all 17 gates in default mode (advisory failures logged but don't block).
A separate **release-gate** job runs with `--strict` on manual dispatch, making all gates blocking.

Use the script as a release gate in CI and archive `production_gate_results.json` as a build artifact.
For faster debug jobs, run specific gates via `--gate`.

## Dashboard Integration Note

`production_gate_results.json` is intentionally machine-readable so it can be surfaced in the dashboard
System Health settings page in a follow-up dashboard task.

## Latest Validation Run

Run date (UTC): `2026-02-13T11:07:19Z`
Command:

```bash
RATE_LIMIT_BURST_REQUESTS=5000 RATE_LIMIT_PARALLEL=300 \
CORDUM_API_KEY=${CORDUM_API_KEY:?set CORDUM_API_KEY} \
./tools/scripts/production_gate.sh
```

Result artifact: `production_gate_results.json`
Overall result: `all_passed=true`

Gate results:

| Gate | Status | Duration (ms) |
|---|---|---:|
| 1 Deploy | PASS | 69410 |
| 2 Auth/Tenant | PASS | 497 |
| 3 Workflow Matrix | PASS | 17360 |
| 4 Policy | PASS | 1575 |
| 5 Reliability | PASS | 7803 |
| 6 Performance | PASS | 20983 |
| 7 Security | PASS | 257261 |

## Extending Gates

1. Add or modify the gate function in `tools/scripts/production_gate.sh`.
2. Wire the function into the `run_gate` dispatch block.
3. Classify the gate as blocking or advisory in the `BLOCKING_GATES` / `ADVISORY_GATES` arrays.
4. Keep pass/fail semantics strict: non-zero return must indicate gate failure.
5. Keep output deterministic and preserve `production_gate_results.json` fields for downstream consumers.

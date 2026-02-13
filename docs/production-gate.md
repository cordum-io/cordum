# Production Gate

`tools/scripts/production_gate.sh` is the release-candidate gate runner for local and CI validation.
It executes seven production-readiness gates and exits non-zero if any selected gate fails.

## What It Tests

1. `Gate 1 Deploy`: clean rebuild, health readiness, and smoke workflow.
2. `Gate 2 Auth/Tenant`: API key/JWT auth checks and tenant isolation.
3. `Gate 3 Workflow Matrix`: auto, approval, deny+DLQ, cancel, and rerun flows.
4. `Gate 4 Policy`: evaluate/simulate/explain endpoints and remediation traceability.
5. `Gate 5 Reliability`: scheduler and gateway restart recovery plus idempotency.
6. `Gate 6 Performance`: latency/error-rate SLO checks under sustained load.
7. `Gate 7 Security`: rate limiting, redaction, headers, malformed input, and payload-size limits.

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

## Environment Variables

Core:

- `CORDUM_API_KEY`: required.
- `CORDUM_API_BASE`: default `http://localhost:8081`.
- `CORDUM_TENANT_ID`: default `default`.
- `CORDUM_ORG_ID`: default `CORDUM_TENANT_ID`.
- `REDIS_URL`: default `redis://:${REDIS_PASSWORD:-cordum-dev}@localhost:6379`.
- `NATS_URL`: default `nats://localhost:4222`.

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

The script prints a summary table and writes JSON:

- `production_gate_results.json`

JSON structure:

```json
{
  "generated_at": "2026-02-13T00:00:00Z",
  "api_base": "http://localhost:8081",
  "tenant_id": "default",
  "selected_gate": "all",
  "all_passed": true,
  "gates": [
    {
      "gate": 1,
      "name": "Gate 1 Deploy",
      "status": "PASS",
      "duration_ms": 12345,
      "message": "quickstart/smoke/health checks passed"
    }
  ]
}
```

## CI Integration

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
3. Keep pass/fail semantics strict: non-zero return must indicate gate failure.
4. Keep output deterministic and preserve `production_gate_results.json` fields for downstream consumers.

# HA Validation Suite

End-to-end acceptance tests that verify Cordum runs correctly with 2 replicas of each service.

## Prerequisites

- Docker and docker compose v2
- `curl` with TLS support
- `CORDUM_API_KEY` environment variable set (generate with `openssl rand -hex 32`)
- TLS certificates in `./certs/` (see main README for generation)

## Running

1. Start the HA stack (2 replicas of gateway, scheduler, workflow-engine):

```bash
export CORDUM_API_KEY=$(openssl rand -hex 32)
docker compose -f docker-compose.yml -f docker-compose.ha.yaml up -d --build
```

2. Run the validation suite:

```bash
bash tests/e2e/ha_validation.sh
```

3. Tear down:

```bash
docker compose -f docker-compose.yml -f docker-compose.ha.yaml down -v
```

## Scenarios

| # | Scenario | What It Verifies |
|---|----------|-----------------|
| 1 | Duplicate dispatch guard | 50 jobs submitted round-robin across gateways; each reaches terminal state exactly once |
| 2 | Global rate limit | Burst requests across both gateways; distributed rate limiter enforces shared limit |
| 3 | Worker snapshot consistency | `GET /workers` from both gateways returns identical worker sets after warm-up |
| 4 | Config propagation | Config read from both gateways matches (shared Redis backing store) |
| 5 | Lock-holder failover | Scheduler-1 stopped mid-flight; scheduler-2 takes over without duplicate processing |

## Expected Output

```
HA Validation Results:
  [PASS] Duplicate dispatch guard (50 jobs, 50 checked, 0 mismatches)
  [PASS] Global rate limit (38/40 accepted, 2 rejected)
  [PASS] Worker snapshot consistency (0 workers match across gateways)
  [PASS] Config propagation (configs match across gateways)
  [PASS] Lock-holder failover (job abc123 -> TIMEOUT, consistent after scheduler restart)

  Total: 5 scenarios, 5 passed, 0 failed
```

## Runtime

Approximately 3-5 minutes. The failover scenario accounts for most of the time (waits up to 90s for lock expiry and takeover).

## CI Integration

The script exits 0 on all-pass, 1 on any failure. Add to CI pipeline:

```yaml
- name: HA validation
  run: |
    docker compose -f docker-compose.yml -f docker-compose.ha.yaml up -d --build --wait
    bash tests/e2e/ha_validation.sh
```

## Customization

| Variable | Default | Purpose |
|----------|---------|---------|
| `CORDUM_API_KEY` | (required) | API key for authentication |
| `CERT_DIR` | `./certs` | Path to TLS certificate directory |
| `GW1` / `GW2` | Configured in script | Gateway endpoints (override for remote testing) |

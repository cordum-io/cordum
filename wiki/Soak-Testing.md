# Soak Testing

Long-running stability tests that detect issues only visible over time: retry storms, log floods, memory drift, and container restart loops.

## What It Validates

| Check | Description | Default Threshold |
|-------|-------------|-------------------|
| HTTP error rate | % of requests returning 4xx/5xx | < 1% |
| Retry storms | Sustained 4xx on a single endpoint | < 50% of endpoint requests |
| Load evidence | Request batches sent and HTTP requests recorded | > 0 of each |
| Memory drift | Median container memory (MiB), first vs last window of post-warm-up snapshots | fails only above 50% **and** 32 MiB growth |
| Log storms | Identical log line repetition per window | < 100 per 5 minutes |
| Container restarts | Any container restart (or a container gone at the end) during soak | 0 restarts |

## Running Locally (Docker Compose)

Prerequisites: Docker Compose services running, `CORDUM_API_KEY` set, `docker` on `PATH` (the memory and restart checks read `docker stats`).

```bash
# Shortest evaluable run: 20 minutes (see "Memory drift evaluation")
CORDUM_API_KEY=<key> SOAK_DURATION_MINUTES=20 bash tools/scripts/soak_test.sh

# Full 60-minute run (default)
CORDUM_API_KEY=<key> bash tools/scripts/soak_test.sh
```

## Running Against Helm/Kubernetes

Prerequisites: `kubectl` configured, gateway pods running, access to the `cordum-api-key` secret.

```bash
# Uses port-forward and resolves API key from K8s secret automatically
bash tools/scripts/soak_test_helm.sh

# With overrides
CORDUM_NAMESPACE=staging SOAK_DURATION_MINUTES=30 bash tools/scripts/soak_test_helm.sh

# If API key is already known
CORDUM_API_KEY=<key> bash tools/scripts/soak_test_helm.sh
```

## Configuration

All settings are controlled via environment variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `CORDUM_API_KEY` | (required) | Gateway API key |
| `CORDUM_API_BASE` | `http://localhost:8081/api/v1` | Gateway base URL |
| `SOAK_DURATION_MINUTES` | `60` | Test duration |
| `SOAK_INTERVAL_SECONDS` | `5` | Seconds between request batches |
| `SOAK_MEMORY_DRIFT_PCT` | `50` | Max allowed memory growth % |
| `SOAK_MEMORY_DRIFT_MIN_MIB` | `32` | Absolute growth floor in MiB; growth past the % limit but under this floor only warns |
| `SOAK_WARMUP_SECONDS` | `300` | Load time before the memory-drift baseline window opens (capped at half the run) |
| `SOAK_MEMORY_WINDOW_SAMPLES` | `5` | K: the drift check compares the median of the first K post-warm-up snapshots with the median of the last K. Integer, minimum `3` |
| `SOAK_METRICS_SOURCE` | `docker` | `docker`: per-container memory from `docker stats`; a missing, failing or empty `docker stats` fails the run. `none`: no container metrics (the Helm wrapper default); drift and restarts are reported as not evaluated. Any other value aborts |
| `SOAK_LOG_STORM_THRESHOLD` | `100` | Max identical log lines per window |
| `SOAK_ERROR_RATE_PCT` | `1` | Max allowed 4xx/5xx rate % |
| `SOAK_RESULTS_FILE` | `soak_results.json` | Output JSON results file |
| `SOAK_METRICS_FILE` | `soak_metrics.log` | Docker stats snapshots |
| `SOAK_HTTP_LOG` | `soak_http.log` | Raw HTTP status log |

Helm-specific:

| Variable | Default | Description |
|----------|---------|-------------|
| `CORDUM_NAMESPACE` | `cordum` | K8s namespace |
| `CORDUM_GATEWAY_SVC` | `cordum-api-gateway` | Gateway service name |
| `CORDUM_GATEWAY_PORT` | `8081` | Gateway service port |
| `CORDUM_SECRET_NAME` | `cordum-api-key` | K8s secret for API key |
| `SOAK_LOCAL_PORT` | `18081` | Local port for port-forward |

## Memory drift evaluation

The check never trusts a single sample. `docker stats` is sampled once a minute per container; the reported usage (B, kB, KiB, MB, MiB, GB, GiB, TB or TiB, at most 15 digits) is converted to MiB with locale-independent decimal arithmetic, never truncated to integer percent. After the warm-up, one `# drift-baseline` marker opens the baseline window. The check compares, per container, the **median** of the first K snapshots from that marker with the median of the last K snapshots (K = `SOAK_MEMORY_WINDOW_SAMPLES`, default 5, minimum 3). One spike in either window does not change the verdict; sustained growth does. The cold pre-load sample is diagnostic only and is never the baseline.

- **FAIL**: median growth above both `SOAK_MEMORY_DRIFT_PCT` (50%) and `SOAK_MEMORY_DRIFT_MIN_MIB` (32 MiB).
- **WARN**: growth above the relative limit only, under the MiB floor.

**An evaluable run needs the warm-up plus at least 2K snapshot-minutes.** With the defaults that is 5 min of warm-up plus 10 snapshots, so `SOAK_DURATION_MINUTES` must be at least about 15; use 20 for a margin. A shorter run fails as "not evaluated"; it never passes.

These are hard FAILs, each with its own message, never a WARN or a skip:

- fewer than 2K valid post-warm-up snapshots for a container, or no `# drift-baseline` marker (`Memory drift not evaluated`);
- a container in the baseline window that is absent at the end, or an end container with no baseline;
- a container restart, or a container that was running at the start and is gone at the end;
- a malformed metric record (bad JSON, non-numeric or empty memory fields, unknown units);
- zero request batches or zero HTTP requests;
- `docker stats` unavailable, failing or empty when `SOAK_METRICS_SOURCE=docker` (an empty mid-run sample is only a gap, which the snapshot-count check then catches).

With `SOAK_METRICS_SOURCE=none` (set by `soak_test_helm.sh` for Kubernetes, which has no `docker stats`) the run records a WARN, `memory_drift_evaluated` is `false`, and restarts are not checked. That is "not evaluated", never a passing leak check.

`soak_results.json` reports `memory_drift_evaluated`, `memory_window_samples` and `memory_baseline_source`.

The analysis lives in `tools/scripts/soak_test_lib.sh` and uses POSIX awk only, so mawk and gawk must agree. Its contract tests run in CI and on a developer machine, including Windows with Git Bash (`bash tools/scripts/soak_test_lib.test.sh`); there, run them under both awks where available (for example `mawk` through WSL), because Git Bash ships only gawk.

## Interpreting Failures

### HTTP error rate exceeded

**Meaning**: More than the threshold % of HTTP requests returned 4xx or 5xx status codes.

**Common causes**:
- Service configuration missing (config not loaded into Redis)
- Auth misconfiguration (wrong API key, missing tenant)
- Service crash or restart during soak

**Remediation**: Check `soak_http.log` for which endpoints are failing. Cross-reference with Docker/K8s logs for the corresponding timestamps.

### 4xx retry storm

**Meaning**: A single endpoint has >50% of its total requests returning 4xx, indicating the client is retrying a permanently failing request.

**Common causes**:
- Missing resource (config, workflow) that the test expects
- Endpoint returning 404 repeatedly due to stale cache
- Rate limiting kicking in without backoff

**Remediation**: Identify the endpoint from the failure message. Check if the resource exists. Review rate limit configuration.

### Memory drift exceeded

**Meaning**: A container's median memory in the last K snapshots is more than the threshold % and the MiB floor above its median in the first K post-warm-up snapshots. For "not evaluated" failures see "Memory drift evaluation" above.

**Common causes**:
- Memory leak in long-lived connections (WebSocket, gRPC streams)
- Unbounded cache growth (Redis client buffers, in-memory caches)
- Goroutine leak from unfinished context propagation

**Remediation**: Profile the specific service with `go tool pprof`. Check goroutine counts. Review connection pool settings.

### Log storm detected

**Meaning**: A single log line appeared more than the threshold times in a 5-minute window.

**Common causes**:
- Retry loop logging the same error every iteration
- Health check failure logging on every poll
- Missing dependency logging connection errors repeatedly

**Remediation**: Identify the repeated log line. Add backoff to the retry loop, or reduce log level for expected transient errors.

### Container restart detected

**Meaning**: A container's restart count increased during the soak, indicating an OOM kill, crash, or health check failure.

**Common causes**:
- OOM kill due to memory leak (check memory drift)
- Panic in Go code (check logs for stack traces)
- Health check timeout due to blocked event loop

**Remediation**: Check Docker/K8s events (`docker events`, `kubectl describe pod`). Review container logs around the restart timestamp.

## CI Integration

The soak test runs nightly in GitHub Actions after the production gate passes.

**Workflow**: `.github/workflows/nightly.yml` -> `soak-test` job

**Artifacts**: On completion (pass or fail), the following are uploaded:
- `soak_results.json` — structured pass/fail results
- `soak_metrics.log` — periodic Docker stats snapshots
- `soak_http.log` — raw HTTP status codes for every request

On failure, Docker Compose logs are also uploaded as `soak-docker-logs`.

**Ad-hoc runs**: Trigger via GitHub Actions `workflow_dispatch` with a custom `soak_duration` input.

## Example Output

### Passing result (`soak_results.json`)

```json
{
  "soak_id": "soak-1739600000",
  "duration_minutes": 60,
  "batches": 720,
  "total_requests": 5760,
  "error_requests": 12,
  "memory_drift_evaluated": true,
  "memory_window_samples": 5,
  "memory_baseline_source": "post-warm-up",
  "pass": 1,
  "failures": [],
  "warnings": [],
  "timestamp": "2026-02-15T04:00:00Z"
}
```

### Failing result

```json
{
  "soak_id": "soak-1739600000",
  "duration_minutes": 60,
  "batches": 720,
  "total_requests": 5760,
  "error_requests": 890,
  "memory_drift_evaluated": true,
  "memory_window_samples": 5,
  "memory_baseline_source": "post-warm-up",
  "pass": 0,
  "failures": [
    "HTTP error rate 15.45% exceeds threshold 1% (890/5760 requests)",
    "4xx retry storm on GET:/config: 445/720 requests returned 4xx",
    "Log storm: 'config get failed error=redis.Nil' repeated 445 times in 5 minutes (limit 100)"
  ],
  "warnings": [],
  "timestamp": "2026-02-15T04:00:00Z"
}
```

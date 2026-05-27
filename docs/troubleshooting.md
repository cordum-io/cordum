# Troubleshooting Guide

Common issues and debugging procedures for Cordum. Each entry includes symptoms, likely causes, diagnostic commands, and fixes.

> For production operational runbooks, see [production.md](production.md).
> For configuration reference, see [configuration-reference.md](configuration-reference.md).
> For Docker-specific issues, see [DOCKER.md](DOCKER.md).

---

## 1. Installation Issues

### Docker Compose fails to start

**Symptoms**: `docker compose up` exits immediately or services crash-loop.

**Likely causes**:

| Cause | Fix |
|-------|-----|
| Missing `CORDUM_API_KEY` | Run `./tools/scripts/quickstart.sh` (auto-generates) or `export CORDUM_API_KEY="$(openssl rand -hex 32)"` |
| Port conflicts (8081, 4222, 6379) | Check with `lsof -i :8081` or `netstat -tlnp \| grep 8081` |
| Image pull errors | Verify Docker login: `docker login ghcr.io` |
| Stale containers | `docker compose down -v && docker compose up` |

**Diagnostic**:
```bash
# Check which services are failing
docker compose ps

# View logs for a specific service
docker compose logs api-gateway --tail=50

# Verify env vars loaded
docker compose config | grep API_KEY
```

### Dashboard Docker build fails

**Symptoms**: `docker build` for dashboard fails with `COPY failed` or runs out of memory.

**Likely cause**: `node_modules` is being copied into the Docker build context, bloating it and breaking the build.

**Fix**: Ensure `dashboard/.dockerignore` exists and excludes `node_modules`:

```
node_modules
dist
.env.local
```

Also ensure the root `.dockerignore` excludes `dashboard/node_modules` for Go service builds:

```
dashboard/node_modules
```

### TypeScript compilation errors

**Symptoms**: Dashboard build fails with type errors.

**Fix**: Use the correct TypeScript invocation (avoid `npx tsc` which may resolve to a different version):

```bash
cd dashboard
node ./node_modules/typescript/bin/tsc --noEmit
```

### MSYS/Windows path mangling

**Symptoms**: `docker exec` commands fail with mangled paths on Git Bash / MSYS.

**Likely cause**: MSYS automatically converts Unix-style paths to Windows paths.

**Fix**: Prefix commands with `MSYS_NO_PATHCONV=1`:

```bash
MSYS_NO_PATHCONV=1 docker exec cordum-redis redis-cli PING
```

### `go test -race` fails on Windows

**Symptoms**: Race detector reports `CGO_ENABLED=0` is incompatible.

**Fix**: The race detector requires CGO, which is disabled on MSYS. Use `-count=3` for repeated runs instead:

```bash
go test -count=3 ./core/...
```

### Can't login to dashboard

**Symptoms**: Dashboard login page rejects credentials.

**Fix**: The admin user is `CORDUM_ADMIN_USERNAME` (default `admin`) with the
password in `CORDUM_ADMIN_PASSWORD` — quickstart auto-generates a compliant one
and prints it (it's also in `.env`). Two common causes of login failure:

1. **User auth is disabled.** Password login only works when
   `CORDUM_USER_AUTH_ENABLED=true`. The published-images path (`docker compose
   up -d` without quickstart) defaults it to `false` — sign in with your
   `CORDUM_API_KEY` instead, or enable it.
2. **The password violates the policy**, so the gateway silently failed to seed
   the admin (look for `seed admin user failed` in `docker compose logs
   api-gateway`). Policy: **≥ 12 characters, with at least one uppercase letter,
   one digit, and one special character.** `admin123` does NOT satisfy it.

To set/rotate the password, edit `.env` and restart:

```bash
# Edit .env: set a compliant CORDUM_ADMIN_PASSWORD (>=12 chars, upper+digit+special)
docker compose restart api-gateway
```

### `openssl` not found

**Symptoms**: API key generation fails because `openssl` is not installed.

**Fix**: `quickstart.sh` handles this automatically with fallbacks (`/dev/urandom` → Python `secrets`). For manual generation without openssl:

```bash
# Using /dev/urandom
head -c 32 /dev/urandom | xxd -p | tr -d '\n'

# Using Python
python -c "import secrets; print(secrets.token_hex(32))"
```

### Go build fails

**Symptoms**: `go build` or `make build` fails with syntax or module errors.

**Fix**: Cordum requires **Go 1.24+**. Check your version:

```bash
go version
# If below 1.24, upgrade: https://go.dev/dl/
```

### Dashboard `npm install` fails

**Symptoms**: `npm install` or `npm run build` fails in the `dashboard/` directory.

**Fix**: Requires **Node 18+**. If you use nvm, the dashboard includes `.nvmrc`:

```bash
cd dashboard
nvm use    # reads .nvmrc (Node 20)
npm install
```

### 502 Bad Gateway from dashboard

**Symptoms**: Dashboard loads but API calls return 502.

**Fix**: The API gateway container may still be starting or has crashed. Check its logs:

```bash
docker compose logs api-gateway --tail=50

# If the gateway is not running, restart it
docker compose restart api-gateway
```

If the issue persists after a gateway rebuild, the dashboard nginx may have cached a stale IP. Restart nginx:

```bash
docker compose restart dashboard
```

---

## 2. Jobs Stuck in PENDING

**Symptoms**: Jobs submitted but never dispatched. Status stays `pending`.

### No pool mapping for topic

**Cause**: The job's topic does not have a mapping in `config/pools.yaml`.

**Diagnostic**:
```bash
# Check pools.yaml mapping
cat config/pools.yaml
# Should have: topics: { "job.default": "default" }

# Check the job's topic
curl -s -H "X-API-Key: $CORDUM_API_KEY" -H "X-Tenant-ID: default" \
  http://localhost:8081/api/v1/jobs/JOB_ID | jq .topic
```

**Fix**: Add the topic-to-pool mapping in `config/pools.yaml`:
```yaml
topics:
  job.default: default
  job.my-topic: my-pool
```

Then delete the cached config in Redis to force reload:
```bash
redis-cli DEL cfg:system:default
```

> **Note**: `cfg:system:default` is write-once (`bootstrapConfig`). The system reads it once at startup and caches it. You must delete the key and restart to apply changes.

### No workers in pool

**Cause**: No workers are connected to the target pool, or workers are not sending heartbeats.

**Diagnostic**:
```bash
# Check worker heartbeats
curl -s -H "X-API-Key: $CORDUM_API_KEY" -H "X-Tenant-ID: default" \
  http://localhost:8081/api/v1/workers | jq '.items[] | {id, pool, last_heartbeat}'

# Check scheduler logs for "no workers" errors
docker compose logs scheduler --tail=100 | grep "no_workers\|no workers"
```

**Fix**: Ensure at least one worker is running, connected to NATS, and heartbeating with the correct pool name. The pool name in the worker config must match the pool name in `pools.yaml`.

### Scheduler errors: `maxSchedulingRetries` exceeded

**Cause**: After 50 failed scheduling attempts (exponential backoff over ~25 minutes), a job is moved to FAILED and sent to DLQ.

**Diagnostic**:
```bash
# Check DLQ for failed jobs
curl -s -H "X-API-Key: $CORDUM_API_KEY" -H "X-Tenant-ID: default" \
  http://localhost:8081/api/v1/dlq?limit=10 | jq '.[] | {id, error}'
```

**Fix**: Address the root cause (no workers, no pool mapping, overloaded pool) and retry from DLQ:
```bash
curl -X POST -H "X-API-Key: $CORDUM_API_KEY" -H "X-Tenant-ID: default" \
  http://localhost:8081/api/v1/dlq/JOB_ID/retry
```

---

## Boundary Hardening Errors

These checks are part of the control-plane boundary hardening rollout. See
[configuration-reference.md](configuration-reference.md) for the feature flags and
[production.md](production.md) for rollout guidance.

### `unknown_topic`

**Error surface**: `POST /api/v1/jobs` returns `400` with `error_code=unknown_topic`.

**Cause**: The canonical topic registry is populated, but the submitted topic is not
registered there. This usually means the pack was not installed, the topic was never
created with `cordumctl topic create`, or it was deleted from the registry.

**Diagnostic**:
```bash
# Check whether the topic exists in the canonical registry
cordumctl topic list

# Optional: inspect the raw registry API response
curl -s -H "X-API-Key: $CORDUM_API_KEY" -H "X-Tenant-ID: default" \
  http://localhost:8081/api/v1/topics | jq .
```

**Fix**:
- Install or reinstall the pack that owns the topic: `cordumctl pack install ./my-pack`
- Or register the topic explicitly:

```bash
cordumctl topic create job.my-pack.process --pool my-pack
```

If you are migrating from legacy `pools.yaml` routing, populate the canonical topic
registry before moving `SCHEMA_ENFORCEMENT` or worker hardening flags to stricter modes.

### `schema_validation_failed`

**Error surface**: `POST /api/v1/jobs` returns `400` with `error_code=schema_validation_failed`
plus a `violations` array.

**Cause**: The topic is registered with an `input_schema_id`, the request payload does
not match that schema, and `SCHEMA_ENFORCEMENT=enforce` is enabled. In `warn` mode the
same mismatch is logged instead of rejected.

**Diagnostic**:
```bash
# Identify the topic's bound input schema
cordumctl topic list

# Fetch the schema document
curl -s -H "X-API-Key: $CORDUM_API_KEY" -H "X-Tenant-ID: default" \
  http://localhost:8081/api/v1/schemas/SCHEMA_ID | jq .
```

**Fix**:
- Compare the request body with the schema referenced by the topic registration
- Update the caller payload so required fields, types, and enums match
- If the schema binding is wrong, update the topic registration or pack manifest and redeploy

When rolling out enforcement in production, keep `SCHEMA_ENFORCEMENT=warn` first so you
can inventory real violations before switching to `enforce`.

### `attestation_failed`

**Error surface**: scheduler logs show `worker heartbeat rejected: attestation failed`
when `WORKER_ATTESTATION=enforce`, or `worker heartbeat accepted without attestation`
when `WORKER_ATTESTATION=warn`.

**Cause**: The worker heartbeat is missing a valid credential token, uses a mismatched
`sender_id`, or is attempting to join a pool not allowed by its credential record.

**Diagnostic**:
```bash
# Check scheduler logs for the exact failure reason
docker compose logs scheduler --tail=100 | grep "attestation failed\|without attestation"

# Review issued worker credentials
cordumctl worker credential list
```

Common reasons in logs include `auth_token_missing`, `credential_invalid`,
`sender_id_mismatch`, and `pool_not_allowed`.

**Fix**:
- Issue or rotate a credential:

```bash
cordumctl worker credential create --worker-id external-worker-01 --allowed-pools my-pack
```

- Store the returned token securely and configure the worker to send it on heartbeats
- Ensure the worker heartbeat `worker_id`, bus `sender_id`, and credential `worker_id` are identical
- Ensure the worker is heartbeating into a pool present in `allowed_pools`

Use `WORKER_ATTESTATION=warn` during migration if you need audit visibility before
hard-failing older workers.

### `worker_not_ready`

**Error surface**: inferred scheduling condition when `WORKER_READINESS_REQUIRED=true`.
Cordum does not currently emit a dedicated `worker_not_ready` API payload; operators
usually observe jobs remaining pending or falling back to `no_workers` despite recent
heartbeats.

**Cause**: The worker has not sent a recent handshake with matching `ready_topics`, or
its readiness record expired after `WORKER_READINESS_TTL`.

**Diagnostic**:
```bash
# Check for scheduler warnings and no-worker symptoms
docker compose logs scheduler --tail=100 | grep "no workers\|ready"

# Verify the topic is valid and currently mapped
cordumctl topic list
```

**Fix**:
- Ensure the worker fully starts its CAP/Cordum runtime and sends the initial handshake
  (for SDK-based workers, this usually means calling `Agent.Start()` before expecting dispatch)
- Ensure the handshake advertises the target topic in `ready_topics`
- If the worker is healthy but handshakes infrequently, increase `WORKER_READINESS_TTL`
- Keep `WORKER_READINESS_REQUIRED=false` during migration if you still have legacy workers
  that only heartbeat and do not yet send readiness metadata

---

## 3. Safety Kernel Unavailable

**Symptoms**: `cordum_safety_unavailable_total` rising, jobs stuck, scheduler logs show `safety_unavailable`.

### TLS misconfiguration

**Cause**: In production mode, the gateway requires TLS to the safety kernel. If certificates are missing or wrong, the gRPC connection fails.

**Diagnostic**:
```bash
# Check env vars
env | grep SAFETY_KERNEL

# Test gRPC health (from gateway container)
grpcurl -plaintext localhost:50051 grpc.health.v1.Health/Check

# With TLS
grpcurl -cacert /path/to/ca.crt \
  cordum-safety-kernel:50051 grpc.health.v1.Health/Check
```

**Fix**: Ensure these env vars are set correctly:
- `SAFETY_KERNEL_ADDR` — gRPC address (e.g., `cordum-safety-kernel:50051`)
- `SAFETY_KERNEL_TLS_CA` — CA certificate path
- `SAFETY_KERNEL_TLS_REQUIRED` — Set to `true` in production

For development, if you don't need TLS:
```bash
unset SAFETY_KERNEL_TLS_REQUIRED
# Or set CORDUM_ENV=development
```

### Safety kernel crashlooping

**Cause**: Invalid safety policy YAML, missing signature key in production mode, or malformed rules.

**Diagnostic**:
```bash
docker compose logs safety-kernel --tail=50 | grep -i "error\|panic\|fatal"

# Check policy syntax
cat config/safety.yaml | python -c "import yaml, sys; yaml.safe_load(sys.stdin.read()); print('OK')"
```

**Fix**:
- Fix YAML syntax errors in `config/safety.yaml`
- If signature verification is required (`SAFETY_POLICY_SIGNATURE_REQUIRED=true`), ensure `SAFETY_POLICY_PUBLIC_KEY` is set
- In production, the safety kernel refuses to start without a valid policy

---

## 4. Output Quarantined

**Symptoms**: Jobs complete but status shows `output_quarantined` instead of `succeeded`.

**Cause**: The output policy service flagged the job result for containing sensitive data (secrets, PII, injection patterns).

**Diagnostic**:
```bash
# Check the job's quarantine reason
curl -s -H "X-API-Key: $CORDUM_API_KEY" -H "X-Tenant-ID: default" \
  http://localhost:8081/api/v1/jobs/JOB_ID | jq '{status, error_message, failure_reason}'

# Check output policy metrics
curl -s http://localhost:9090/metrics | grep output_policy_quarantined
```

**Fix**:
- Review the quarantined output in the dashboard's Quarantine tab
- If the quarantine was a false positive, adjust output policy rules in `config/output_scanners.yaml`
- Remediate the output (redact sensitive data) and re-release via the dashboard

---

## 5. Dashboard Issues

### Blank page after deploy

**Symptoms**: Dashboard loads but shows a white screen.

**Likely causes**:

| Cause | Diagnostic | Fix |
|-------|-----------|-----|
| API URL not configured | Check browser console for 404s | Set `VITE_API_URL` or configure runtime config |
| CORS blocking API calls | Browser console shows CORS errors | Set `CORDUM_ALLOWED_ORIGINS` to include dashboard origin |
| JavaScript build error | Browser console shows JS errors | Rebuild: `cd dashboard && npm run build` |

**Diagnostic**:
```bash
# Check CORS config
env | grep CORS
env | grep ALLOWED_ORIGINS

# Test API from dashboard origin
curl -H "Origin: http://localhost:5173" \
  -H "Access-Control-Request-Method: GET" \
  -X OPTIONS http://localhost:8081/api/v1/jobs
```

**Fix**: Set allowed origins (checked in order: `CORDUM_ALLOWED_ORIGINS`, `CORDUM_CORS_ALLOW_ORIGINS`, `CORS_ALLOW_ORIGINS`):
```bash
export CORDUM_ALLOWED_ORIGINS="http://localhost:5173,https://dashboard.example.com"
```

### Authentication failures

**Symptoms**: Dashboard shows 401/403 errors.

**Diagnostic**:
```bash
# Test API key
curl -H "X-API-Key: $CORDUM_API_KEY" -H "X-Tenant-ID: default" \
  http://localhost:8081/api/v1/jobs
# Should return 200, not 401

# If user auth is enabled, test login
curl -X POST http://localhost:8081/api/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"username":"admin","password":"your-password"}'
```

**Fix**:
- Ensure the dashboard is sending `X-API-Key` and `X-Tenant-ID` headers
- For user auth: verify `CORDUM_USER_AUTH_ENABLED=true` and `CORDUM_ADMIN_PASSWORD` is set
- Check that the API key matches what the gateway expects (`CORDUM_API_KEY` or `CORDUM_API_KEYS`)

### WebSocket stream disconnects

**Symptoms**: Live updates stop, dashboard shows stale data.

**Cause**: WebSocket connection drops due to proxy timeout, auth expiry, or slow client eviction (server buffer full at 100 events).

**Diagnostic**:
- Open browser DevTools → Network → WS tab
- Check for close frames and error codes
- The dashboard auto-reconnects with exponential backoff (1s to 30s)

**Fix**:
- If behind a reverse proxy (nginx), increase WebSocket timeout:
  ```nginx
  proxy_read_timeout 3600s;
  proxy_send_timeout 3600s;
  ```
- If the client is too slow to consume events, the server evicts it. This is by design — the client will reconnect.

### Stale data / cache issues

**Symptoms**: Dashboard shows outdated data that refreshes on manual page reload.

**Cause**: React Query cache not being invalidated properly.

**Fix**: Hard refresh: `Ctrl+Shift+R` (or `Cmd+Shift+R` on Mac). The WebSocket event stream automatically invalidates relevant query caches — if updates are not propagating, check that the WebSocket connection is active (browser DevTools → Network → WS).

---

## 6. Worker Not Receiving Jobs

**Symptoms**: Worker is running and sending heartbeats, but no jobs arrive.

### Topic mismatch

**Cause**: Worker is subscribed to a different NATS subject than what the scheduler publishes to.

**Diagnostic**:
```bash
# Check what topic the worker subscribes to
# Worker logs should show the subscribed subject on startup

# Check what the scheduler routes to
cat config/pools.yaml
# Verify topic → pool mapping matches the worker's pool name
```

**Fix**: The worker's pool name (in heartbeat) must match the pool in `pools.yaml`, and the pool must be mapped to the job's topic. Example:
```yaml
# config/pools.yaml
topics:
  job.hello-pack.echo: hello-pack  # Topic → pool mapping
pools:
  hello-pack:
    requires: []
```

The worker must heartbeat with `pool=hello-pack`.

### NATS connection failure

**Cause**: Worker cannot connect to NATS.

**Diagnostic**:
```bash
# Test NATS connectivity
nats pub test "hello" --server nats://localhost:4222

# Check NATS logs
docker compose logs nats --tail=50
```

**Fix**: Ensure `NATS_URL` or `natsUrl` points to the correct NATS server. For TLS, set `NATS_TLS_CA`, `NATS_TLS_CERT`, `NATS_TLS_KEY`.

---

## 7. Redis Connection Errors

**Symptoms**: Services fail to start or return 500 errors. Logs show `redis: connection refused` or `redis: nil`.

### Redis not running

**Diagnostic**:
```bash
# Check Redis
docker compose ps redis
redis-cli PING
# Should return PONG
```

**Fix**: Start Redis: `docker compose up redis -d`

### Redis TLS misconfigured

**Cause**: Production mode expects TLS for Redis connections.

**Fix**: Set TLS env vars:
```bash
export REDIS_TLS_CA=/path/to/ca.crt
export REDIS_TLS_CERT=/path/to/client.crt
export REDIS_TLS_KEY=/path/to/client.key
```

Or for development, use non-TLS Redis (set `CORDUM_ENV=development`).

### Redis Cluster not initialized

**Cause**: In K8s production overlay, the Redis Cluster requires an init job.

**Diagnostic**:
```bash
kubectl get pods -l job-name=cordum-redis-cluster-init -n cordum
kubectl logs job/cordum-redis-cluster-init -n cordum
```

**Fix**: Re-run the init job:
```bash
kubectl delete job cordum-redis-cluster-init -n cordum
kubectl apply -k deploy/k8s/production
```

### Config cache stale

**Cause**: Pool/timeout config is cached in Redis key `cfg:system:default` (write-once). Changes to `config/pools.yaml` or `config/timeouts.yaml` are not picked up until the key is deleted.

**Fix**:
```bash
redis-cli DEL cfg:system:default
# Then restart the gateway/scheduler
```

---

## 8. Performance Issues

### Safety kernel latency > 5ms p99

**Cause**: Too many policy rules, complex regex patterns, or kernel under-provisioned.

**Diagnostic**:
```bash
# Check p99 latency
curl -s http://localhost:9090/metrics | grep output_eval_duration
```

**Fix**:
- Reduce rule count or simplify regex patterns in safety policy
- Scale safety kernel replicas (stateless, scales freely)
- Verify no external I/O in the evaluation hot path

### Redis memory growing unbounded

**Cause**: Missing TTLs on job pointers or large payloads.

**Diagnostic**:
```bash
redis-cli INFO memory
redis-cli DBSIZE

# Check keys without TTL
redis-cli --scan --pattern "ctx:job:*" | head -5 | xargs -I{} redis-cli TTL {}
```

**Fix**:
- Set `REDIS_DATA_TTL` (default: 24h) for job data
- Set `JOB_META_TTL` (default: 168h / 7 days) for job metadata
- For large payloads, use the blob store instead of inline context

### Dispatch latency high

**Cause**: Job lock contention, Redis slowness, or scheduler overloaded.

**Diagnostic**:
```bash
# Check lock wait time
curl -s http://localhost:9090/metrics | grep job_lock_wait

# Check Redis slowlog
redis-cli SLOWLOG GET 10

# Check scheduler goroutines
curl -s http://localhost:9090/metrics | grep active_goroutines
```

**Fix**:
- Scale scheduler replicas (safe — uses distributed Redis lock for reconciler)
- Check for hot topics consuming all capacity
- Increase Redis connection pool if connection-limited

---

## 9. DLQ Growing

**Symptoms**: Dead-letter queue accumulates entries.

**Diagnostic**:
```bash
# List DLQ entries
curl -s -H "X-API-Key: $CORDUM_API_KEY" -H "X-Tenant-ID: default" \
  http://localhost:8081/api/v1/dlq?limit=20 | jq '.[] | {id, error, created_at}'

# Group by error type
curl -s -H "X-API-Key: $CORDUM_API_KEY" -H "X-Tenant-ID: default" \
  http://localhost:8081/api/v1/dlq?limit=100 | jq '[.[].error] | group_by(.) | map({error: .[0], count: length})'
```

**Common DLQ reasons**:

| Error | Cause | Fix |
|-------|-------|-----|
| `no_pool_mapping` | Topic has no pool in pools.yaml | Add topic→pool mapping |
| `no_workers` | No workers in target pool | Start workers, check pool name |
| `timeout: dispatched exceeded` | Workers not picking up jobs | Check worker health |
| `timeout: running exceeded` | Workers hanging | Check worker logs, increase timeout |
| `safety: denied` | Policy blocked the job | Review safety rules |
| `output_quarantined` | Output scan flagged content | Review in dashboard |
| `max retries exceeded` | Persistent worker failures | Check worker error logs |

**Retry or delete**:
```bash
# Retry a job
curl -X POST -H "X-API-Key: $CORDUM_API_KEY" -H "X-Tenant-ID: default" \
  http://localhost:8081/api/v1/dlq/JOB_ID/retry

# Delete a job from DLQ
curl -X DELETE -H "X-API-Key: $CORDUM_API_KEY" -H "X-Tenant-ID: default" \
  http://localhost:8081/api/v1/dlq/JOB_ID
```

### DLQ cleanup leader election

In multi-replica deployments, only one replica runs DLQ index cleanup per interval using a distributed lock (`cordum:dlq:cleanup`). The lock TTL matches the cleanup interval (default 1 hour), so leadership rotates naturally if a replica goes down.

**Symptoms**: Stale DLQ index entries accumulate (index references expired data keys).

**Diagnostic**:
```bash
# Check if the cleanup lock exists (indicates an active cleaner)
redis-cli GET cordum:dlq:cleanup

# Check index size vs actual entries
redis-cli ZCARD dlq:index
redis-cli --scan --pattern "dlq:entry:*" | wc -l
# If ZCARD >> entry count, stale entries exist
```

**Fix**: If cleanup appears stalled, verify at least one replica is healthy and can reach Redis. The lock expires after one cleanup interval, so a new replica will take over automatically. To force immediate cleanup, delete the lock key:
```bash
redis-cli DEL cordum:dlq:cleanup
```

---

## 10. NATS Issues

### JetStream not enabled

**Symptoms**: Scheduler fails to create streams on startup.

**Diagnostic**:
```bash
nats server info --server nats://localhost:4222
# Look for: jetstream: true
```

**Fix**: Enable JetStream in NATS config:
```
jetstream {
  store_dir: /data/jetstream
  max_mem: 1G
  max_file: 10G
}
```

### Stream consumers out of sync

**Symptoms**: Jobs delivered multiple times or not at all.

**Diagnostic**:
```bash
nats consumer list CORDUM_JOBS --server nats://localhost:4222
nats consumer info CORDUM_JOBS cordum-scheduler --server nats://localhost:4222
```

**Fix**: If a consumer is stuck, delete and let the scheduler recreate it:
```bash
nats consumer rm CORDUM_JOBS cordum-scheduler --server nats://localhost:4222
# Restart the scheduler
```

---

## 11. Debug Commands Quick Reference

### Health checks

```bash
# Gateway HTTP health
curl http://localhost:8081/health

# Gateway gRPC health
grpcurl -plaintext localhost:50051 grpc.health.v1.Health/Check

# System health (authenticated)
curl -H "X-API-Key: $CORDUM_API_KEY" -H "X-Tenant-ID: default" \
  http://localhost:8081/api/v1/system/health
```

### Job state inspection

```bash
# Get job details
curl -s -H "X-API-Key: $CORDUM_API_KEY" -H "X-Tenant-ID: default" \
  http://localhost:8081/api/v1/jobs/JOB_ID | jq '{id, status, topic, error_message}'

# List recent jobs
curl -s -H "X-API-Key: $CORDUM_API_KEY" -H "X-Tenant-ID: default" \
  "http://localhost:8081/api/v1/jobs?limit=10&sort=created_at&order=desc" | jq '.items[] | {id, status, topic}'
```

### Redis inspection

```bash
# Check config cache
redis-cli GET cfg:system:default

# Check job state directly
redis-cli HGETALL job:JOB_ID

# Check Redis memory
redis-cli INFO memory | grep used_memory_human

# List all job keys
redis-cli --scan --pattern "job:*" | wc -l
```

### NATS inspection

```bash
# List streams
nats stream list --server nats://localhost:4222

# Check stream info
nats stream info CORDUM_JOBS --server nats://localhost:4222

# Monitor messages in real-time
nats sub "sys.>" --server nats://localhost:4222
```

### Metrics

```bash
# Gateway metrics
curl -s http://localhost:9092/metrics | grep cordum_http

# Scheduler metrics
curl -s http://localhost:9090/metrics | grep cordum_jobs

# Safety metrics
curl -s http://localhost:9090/metrics | grep safety
```

### Logs

```bash
# Docker Compose
docker compose logs api-gateway --tail=100 --follow
docker compose logs scheduler --tail=100 --follow
docker compose logs safety-kernel --tail=100 --follow

# Kubernetes
kubectl logs -l app=cordum-api-gateway -n cordum --tail=100
kubectl logs -l app=cordum-scheduler -n cordum --tail=100

# Filter structured logs (JSON)
docker compose logs scheduler 2>&1 | jq -r 'select(.level == "ERROR")'
```

---

## 12. CAP SDK Issues

For CAP SDK-level issues (worker connection, protocol errors, handler panics), see the [CAP Troubleshooting Guide](https://github.com/cordum-io/cap/blob/main/docs/troubleshooting.md).

---

## Rolling Restarts & Graceful Shutdown

All Cordum services shut down gracefully within **15 seconds** when receiving SIGTERM (the default signal K8s sends during rolling restarts). This is well under the default `terminationGracePeriodSeconds` of 30s.

| Service | Shutdown Behavior |
|---------|-------------------|
| API Gateway | Drains HTTP + gRPC, stops WebSocket taps, shuts down metrics (15s timeout) |
| Context Engine | `GracefulStop()` drains in-flight gRPC RPCs, fallback to forced `Stop()` after 15s |
| Safety Kernel | `GracefulStop()` drains in-flight gRPC RPCs, stops policy watcher, fallback to `Stop()` after 15s |
| Workflow Engine | Drains NATS subscriptions, waits for in-flight workflow step handlers to return (15s timeout) |
| Scheduler | Stops engine (drains NATS), releases job locks via context cancellation, stops snapshot writer (15s timeout) |

**If pods are killed during rolling restarts**:

1. Check `terminationGracePeriodSeconds` in the Deployment spec is >= 30s (the default). If it's too low, pods receive SIGKILL before graceful shutdown completes.
2. Check PodDisruptionBudgets are in place to prevent draining too many replicas simultaneously.
3. In-flight gRPC calls (to context-engine or safety-kernel) will receive a `CANCELLED` or `UNAVAILABLE` status code — clients should retry.
4. The scheduler's job locks have a 60s TTL. If a scheduler replica is killed without releasing locks, the surviving replica takes over within 60s.

---

## 13. Multi-Replica / Horizontal Scaling

> For a full HA deployment guide, see [horizontal-scaling.md](horizontal-scaling.md).

### Duplicate job dispatch

**Symptoms**: Same job processed multiple times, or job appears in multiple terminal states.

**Cause**: Job locks not being acquired (Redis connectivity issue) or lock TTL too short.

**Diagnostic**:
```bash
# Check if job lock exists
redis-cli GET "cordum:scheduler:job:JOB_ID"

# Check reconciler lock (should be held by one replica)
redis-cli GET "cordum:reconciler:default"

# Check pending replayer lock
redis-cli GET "cordum:replayer:pending"
```

**Fix**: Verify all scheduler replicas connect to the same Redis. The job lock TTL is 60s with renewal at 20s intervals — if jobs take longer than 60s without renewal, increase the timeout or investigate Redis latency.

### Rate limit bypass with multiple gateways

**Symptoms**: Combined request rate exceeds the configured limit. Each gateway allows the full limit independently.

**Cause**: Redis-backed rate limiting is disabled or Redis is unreachable (each replica falls back to in-memory token bucket).

**Diagnostic**:
```bash
# Check rate limit keys in Redis
redis-cli KEYS "cordum:rl:*"
# Should show keys like cordum:rl:default:1708444800

# Check gateway env
docker compose exec api-gateway env | grep REDIS_RATE_LIMIT
```

**Fix**: Ensure `REDIS_RATE_LIMIT` is not set to `false`/`0`/`no`. If Redis is unreachable, the effective rate limit = N × configured limit (where N = replica count).

### Worker snapshot mismatch across gateways

**Symptoms**: Different gateway replicas show different worker sets via `GET /api/v1/workers`.

**Cause**: Snapshot writer lock held by a crashed replica, or snapshot not yet written.

**Diagnostic**:
```bash
# Check snapshot writer lock
redis-cli GET "cordum:scheduler:snapshot:writer"

# Check snapshot freshness
redis-cli OBJECT IDLETIME "sys:workers:snapshot"
# Idle time > 15s indicates the writer may be stalled
```

**Fix**: The snapshot writer runs every 5s with a 10s lock TTL. If a replica crashes, the lock expires and another takes over within ~15s. If the lock appears stuck, delete it:
```bash
redis-cli DEL "cordum:scheduler:snapshot:writer"
```

### Config drift between replicas

**Symptoms**: Replicas behave differently after a config update (e.g., different routing rules).

**Cause**: NATS notification missed and polling interval hasn't elapsed yet.

**Diagnostic**:
```bash
# Check NATS connectivity for config notifications
docker compose logs scheduler 2>&1 | grep "config.changed"

# Force config reload by restarting
docker compose restart scheduler
```

**Fix**: Config changes propagate via `sys.config.changed` NATS broadcast (instant) with a 30s polling fallback. If NATS is partitioned, wait up to 30s for the polling cycle. All replicas always reload from Redis (not from the NATS message), so consistency is guaranteed once the poll fires.

### Circuit breaker stuck open across all replicas

**Symptoms**: All safety checks fail immediately with "circuit open" error, even though the safety kernel is healthy.

**Cause**: Redis-backed circuit breaker failure counter exceeded the threshold and hasn't reset.

**Diagnostic**:
```bash
# Check circuit breaker state
redis-cli GET "cordum:cb:safety:failures"
redis-cli GET "cordum:cb:safety:output:failures"

# Check safety kernel health
grpcurl -plaintext safety-kernel:50051 grpc.health.v1.Health/Check
```

**Fix**: The circuit breaker enters half-open state after 30s and allows probe requests. If the safety kernel is healthy, the circuit closes after 2 successful probes. To force-reset:
```bash
redis-cli DEL "cordum:cb:safety:failures"
redis-cli DEL "cordum:cb:safety:output:failures"
```

### Safety decision cache staleness

**Symptoms**: Policy changes not reflected in safety decisions across replicas.

**Cause**: Per-replica decision cache still serving old decisions within the TTL window.

**Diagnostic**: Check `SAFETY_DECISION_CACHE_TTL` — each replica caches decisions locally for this duration. Policy version changes invalidate the cache, but during a rolling deploy, new and old replicas may briefly disagree.

**Fix**: Reduce `SAFETY_DECISION_CACHE_TTL` for faster propagation (at the cost of more gRPC calls). Setting it to `0` disables caching entirely.

### Workflow run processed by multiple replicas

**Symptoms**: Workflow steps execute twice or produce inconsistent state.

**Cause**: Redis per-run lock (`cordum:wf:run:lock:<runID>`) not being acquired.

**Diagnostic**:
```bash
# Check run lock
redis-cli GET "cordum:wf:run:lock:RUN_ID"

# Check workflow reconciler lock
redis-cli GET "cordum:workflow-engine:reconciler:default"
```

**Fix**: Verify workflow engine replicas connect to the same Redis. The per-run lock has a 30s TTL with 10s renewal. If Redis is unreachable, the engine falls back to local-only locking (safe for single-replica but not cross-replica).

### Delay timers lost after restart

**Symptoms**: Workflow delay steps don't fire after a replica restart.

**Cause**: In-memory timers were lost, and the delay poller hasn't picked them up yet.

**Diagnostic**:
```bash
# Check durable delay timers in Redis
redis-cli ZRANGE "cordum:wf:delay:timers" 0 -1 WITHSCORES

# Check delay poller lock
redis-cli GET "cordum:wf:delay:poller"
```

**Fix**: Delays ≥10s are persisted to Redis sorted set. The delay poller runs every 5s and fires past-due timers. On restart, `recoverDelayTimers` fires all past-due entries immediately. If the poller lock is stuck, delete it:
```bash
redis-cli DEL "cordum:wf:delay:poller"
```

---

## 14. Orphan NATS Messages / "run lock busy" Retry Storm

**Symptoms**: Gateway logs flood with `run lock busy: <run_id>` errors. New workflow runs appear stuck even though workers report completion. `sys.job.>` subject has hundreds of stale messages.

**Likely causes**:

| Cause | Fix |
|-------|-----|
| Deleted/completed runs left orphan job result messages in NATS | Gateway now auto-detects and ACKs stale messages (see below) |
| Old gateway version without stale-result detection | Upgrade gateway — `isStaleJobResult` checks terminal run/step state on lock contention |

**Diagnostic commands**:
```bash
# Count pending messages in the CORDUM_SYS stream
nats stream info CORDUM_SYS

# List consumers and their pending counts
nats consumer ls CORDUM_SYS

# Check for specific orphan run IDs in gateway logs
grep "discarding stale job result" /var/log/cordum/gateway.log
```

**How the fix works**: When a job result message encounters lock contention, the gateway checks if the target run is missing (deleted) or in a terminal state (succeeded/failed/cancelled/timed_out), or if the target step is already terminal. If any condition is true, the message is ACKed immediately instead of retried — breaking the retry storm.

**Log lines to expect** (structured JSON when `CORDUM_LOG_FORMAT=json`):
- `"msg":"discarding stale job result: run not found"` — run was deleted
- `"msg":"discarding stale job result: run is terminal"` — run already completed
- `"msg":"discarding stale job result: step is terminal"` — step already finished

**Manual purge** (if queue has pre-fix stale data):
```bash
# Purge all stale sys.job messages (safe — active results will be re-published by workers)
nats stream purge CORDUM_SYS --subject "sys.job.>" --force
```

**JetStream consumer bounds** (env vars in `core/infra/bus/nats.go`):

| Setting | Default | Env Var | Purpose |
|---------|---------|---------|---------|
| MaxDeliver | 100 | — | Max redeliveries before message is terminated |
| AckWait | 10min | `NATS_JS_ACK_WAIT` | Time before NATS redelivers unacked message |
| MaxAckPending | 2048 | `NATS_MAX_ACK_PENDING` | Max in-flight messages per consumer |

---

## 15. Approval Shows Empty or Partial Business Context

**Symptoms**: The Approvals page shows a workflow approval with little or no business
context, or the card/detail drawer displays a warning such as `Approval context is
missing`, `malformed`, or `unavailable`.

**Likely causes**:

| Cause | Expected markers |
|-------|------------------|
| Workflow payload persisted and hydrated successfully | `decision_summary.source=workflow_payload`, `decision_summary.context_status=available`, `context_ptr` present, `job_input.decision.*` populated |
| Stored workflow payload missing | `decision_summary.context_status=missing`, `context_ptr` usually present, `job_input` absent |
| Gateway cannot access the memory/context store | `decision_summary.context_status=unavailable` |
| Stored workflow payload is invalid JSON or invalid approval envelope | `decision_summary.context_status=malformed` |
| Approval came from a legacy/non-workflow path | `decision_summary.source=policy_only`, `decision_summary.context_status=absent` |

**Diagnostic commands**:

```bash
# Inspect the approval record
curl -sS "http://localhost:8081/api/v1/approvals?include_resolved=false" \
  -H "X-API-Key: $CORDUM_API_KEY" \
  -H "X-Tenant-ID: $CORDUM_TENANT_ID" \
  | jq --arg job "$JOB_ID" '
      .items[]
      | select(.job.id == $job)
      | {
          context_ptr,
          decision_summary,
          job_input
        }
    '

# If this is a workflow approval, inspect the related run
curl -sS "http://localhost:8081/api/v1/workflow-runs/${RUN_ID}" \
  -H "X-API-Key: $CORDUM_API_KEY" \
  -H "X-Tenant-ID: $CORDUM_TENANT_ID" \
  | jq '{id, status, current_step, error}'

# Check gateway + workflow engine logs for context hydration or workflow errors
docker compose logs gateway workflow-engine --tail=200
```

**Fixes**:

- `missing`: verify the workflow engine persisted approval context before cleanup, and
  confirm the backing memory/context store was not reset between run creation and
  approval listing.
- `unavailable`: check Redis/context-store wiring, local dependency health, and gateway
  startup logs. The gateway cannot hydrate workflow payloads if the context store is
  unreachable.
- `malformed`: inspect the code or test data that wrote the approval payload. The stored
  JSON must deserialize into a valid workflow approval context envelope.
- `policy_only` / `absent`: confirm you are looking at the intended workflow approval.
  Legacy safety approvals are still supported and intentionally fall back to policy
  metadata without synthesized workflow payloads.

**Prevention**:

- Define approval steps with structured `input` fields such as `amount`, `currency`,
  `vendor`, `items`, `approval_reason`, and `next_effect`.
- Add `input_schema` so missing required decision fields fail early.
- Re-run `./tools/scripts/e2e_install_workflow.sh` or the manual flow in
  `docs/LOCAL_E2E.md` after changing workflow approval behavior.

---

## 16. Stuck or Zombie Approvals

**Symptoms**: The dashboard shows an approval that cannot be acted on, a job stays in
`APPROVAL_REQUIRED` after someone already approved/rejected it, or approve/reject calls
return `409` conflicts.

### Read the lifecycle fields first

Inspect the approval record before deciding whether this is an operator repair case or a
normal race:

```bash
curl -sS "http://localhost:8081/api/v1/approvals?include_resolved=true" \
  -H "X-API-Key: $CORDUM_API_KEY" \
  -H "X-Tenant-ID: $CORDUM_TENANT_ID" \
  | jq --arg job "$JOB_ID" '
      .items[]
      | select(.job.id == $job)
      | {
          approval_status,
          approval_actionability,
          approval_revision,
          approval_decision,
          policy_snapshot,
          job_hash,
          resolved_by,
          resolved_comment
        }
    '
```

Interpretation:

| Field | Meaning |
|-------|---------|
| `approval_status=pending` + `approval_actionability=actionable` | Normal active approval |
| `approval_status=approved/rejected` + job still `APPROVAL_REQUIRED` | Legacy zombie approval; repairable |
| `approval_status=invalidated` | Request/workflow drifted; refresh and re-evaluate |
| `approval_status=repaired` | Operator or reconciler repaired an inconsistent record |

### Understand structured `409` conflicts

Approve/reject/repair endpoints now return machine-readable conflict codes:

| Code | Meaning | Operator action |
|------|---------|-----------------|
| `approval_retryable_lock` | Another approval decision or repair is in progress | Wait a moment and retry |
| `approval_terminal_run` | Workflow run already moved past this approval | Refresh the run and inspect final state |
| `approval_stale_snapshot` | Policy snapshot drifted | Re-evaluate against the latest policy |
| `approval_stale_request` | Underlying job request changed | Refresh and review the latest request |
| `approval_not_actionable` | Approval is no longer actionable | Refresh dashboard state |
| `approval_already_resolved` | Decision already recorded | Review audit/history; no repair needed |

### Run an approval repair dry-run

Use dry-run first. It inspects the approval and returns the planned repair without
mutating Redis:

```bash
cordumctl approval repair "$JOB_ID"
```

Equivalent API call:

```bash
curl -sS -X POST "http://localhost:8081/api/v1/approvals/${JOB_ID}/repair" \
  -H "X-API-Key: $CORDUM_API_KEY" \
  -H "X-Tenant-ID: $CORDUM_TENANT_ID" \
  -H "Content-Type: application/json" \
  -d '{"apply":false}' | jq .
```

Common dry-run `plan.kind` values:

- `repair_approved_resolution` / `repair_rejected_resolution` — legacy partial approval
- `invalidate_terminal_run` — workflow already ended
- `invalidate_stale_snapshot` — policy snapshot drifted
- `invalidate_stale_request` — job payload/hash drifted
- publish-intent repair plans — state was decided but follow-up publish must be replayed

### Apply the repair

If the dry-run plan is safe and expected:

```bash
cordumctl approval repair "$JOB_ID" --apply --note "on-call repair after zombie approval"
```

What happens next:

- the approval lifecycle is updated atomically
- an audit entry is recorded with action `repair`
- if possible, any deferred submit/DLQ publish is emitted immediately
- if publish cannot complete inline, recovery logic replays it later and the response
  includes `publish_deferred=true`

### When not to repair manually

Do **not** apply a manual repair when:

- the conflict code is `approval_retryable_lock` (retry instead)
- the approval is still `actionable`
- the dry-run plan is not the state transition you expect
- you cannot explain the policy snapshot or job hash drift

## Kubernetes Deployment Issues

### Dashboard or gateway pod in CrashLoopBackOff

**Symptom:** Pod shows `CrashLoopBackOff`, logs show `can't create /tmp/...: Read-only file system`.

**Cause:** The Helm chart sets `readOnlyRootFilesystem: true` in the container security context. The dashboard's nginx entrypoint writes to `/tmp`, `/etc/nginx/conf.d`, `/var/cache/nginx`, and `/var/run`. The gateway's pack install API extracts bundles to `/tmp`.

**Fix:** The Helm chart (v0.2.0+) adds `emptyDir` volumes for all writable paths. If you're on an older chart version, add these volumes manually to the deployment spec.

### Safety kernel logs "NATS connection failed, relying on poll"

**Symptom:** Safety kernel starts but logs `NATS connection failed`. Policy updates from pack installs are delayed.

**Cause:** The safety kernel's `NATS_URL` env var was missing in Helm chart versions before v0.2.0. It defaults to `nats://nats:4222` but the K8s service is `nats://cordum-nats:4222`.

**Fix:** Upgrade to Helm chart v0.2.0+ which sets `NATS_URL` on all services. Verify with `kubectl logs -l app.kubernetes.io/component=safety-kernel | grep "subscribed to config change"`.

### Jobs fail with "unknown topic"

**Symptom:** Workflow steps fail with `{"message": "unknown topic"}`.

**Cause:** The topic is not registered in the topic registry (Redis `cfg:system:topics`). This happens when you run workflows without installing the pack first. Workers connecting via heartbeat do NOT auto-register topics — this is by design (security).

**Fix:** Install the pack before running workflows:
```bash
tar -czf pack.tgz -C ./my-pack pack.yaml workflows overlays
curl -X POST https://api.cordum.example.com/api/v1/packs/install \
  -H "X-API-Key: $API_KEY" -F "bundle=@pack.tgz"
```
The pack install auto-registers topics, pool mappings, policy fragments, and workflows.

### Pack install fails with "invalid archive path"

**Symptom:** `POST /api/v1/packs/install` returns `{"error": "extract tar.gz: validate tar entry path ./: invalid archive path"}`.

**Cause:** The tar bundle includes `./` as a path entry (created by `tar -czf file.tgz .`). The gateway rejects this as a path traversal protection.

**Fix:** Create the tar without the `./` prefix:
```bash
tar -czf pack.tgz -C ./my-pack pack.yaml workflows overlays
```

## Related Docs

- [production.md](production.md) — Production readiness guide with incident runbooks
- [configuration-reference.md](configuration-reference.md) — Complete config schema reference
- [configuration.md](configuration.md) — Config files and environment variables
- [DOCKER.md](DOCKER.md) — Docker Compose setup and JetStream durability
- [k8s-deployment.md](k8s-deployment.md) — Kubernetes deployment guide
- [websocket-streaming.md](websocket-streaming.md) — WebSocket protocol reference
- [horizontal-scaling.md](horizontal-scaling.md) — Multi-replica deployment guide

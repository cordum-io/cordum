# Production Readiness Guide

This guide covers the minimum hardening steps before running Cordum in production,
plus operational runbooks for disaster recovery, incident response, and scaling.

The sample K8s manifests under `deploy/k8s` are a starter only.
For a production-oriented baseline, use the kustomize overlay under
`deploy/k8s/production/` (stateful NATS/Redis, TLS/mTLS, network policies,
monitoring, backups, and HA defaults).

For Helm-based deployments, start with `cordum-helm/` and apply the same
hardening steps below.

> For detailed Kubernetes manifest walkthroughs, see [k8s-deployment.md](k8s-deployment.md).

---

## 1) Persistence + Durability

- Run NATS with JetStream persistence (PVCs). Prefer a 3-node NATS cluster for HA.
- Tune JetStream `sync_interval` (fsync cadence) for crash durability; the production overlay sets `1s` by default.
- Run Redis with persistence + backups (managed Redis, Redis Sentinel, or Redis Cluster).
- Verify pointer retention policies (result/context/lock TTLs) match your compliance needs.

## 2) High Availability

- **Gateway**: Stateless — run multiple replicas behind a Service + HPA. Scale freely.
- **Safety kernel**: Stateless — can be replicated if your gRPC evaluation load requires it.
- **Scheduler**: Uses a distributed Redis lock (`cordum:reconciler:default`) for the reconciler. Multiple replicas are safe — only one acquires the lock per tick. Scale with care.
- **Workflow engine**: Stateful — scale cautiously and coordinate instances.
- **Dashboard**: Static SPA served by nginx — scale freely.

The production overlay includes:
- PodDisruptionBudgets (maxUnavailable: 1) for gateway, scheduler, workflow-engine, and safety-kernel
- HPAs for gateway (2–10 replicas, CPU 70%, memory 80%) and scheduler (2–10 replicas, same targets)

## 3) Security Baseline

- TLS for all ingress traffic.
- TLS/mTLS for NATS and Redis (or use managed services with encryption).
- Set `CORDUM_ENV=production` (or `CORDUM_PRODUCTION=true`) to enforce TLS on HTTP/gRPC and safety kernel clients.
- Configure API keys (`CORDUM_API_KEYS` or `CORDUM_API_KEY`); production mode fails without keys.
- Configure user authentication (`CORDUM_USER_AUTH_ENABLED=true`) for dashboard access with proper user credentials.
- Set a strong admin password (`CORDUM_ADMIN_PASSWORD`) for initial admin user creation.
- Configure policy signature verification (`SAFETY_POLICY_PUBLIC_KEY` + signature); production mode fails without a public key.
- Keep gRPC reflection disabled (enable only for debugging with `CORDUM_GRPC_REFLECTION=1`).
- HSTS headers are set automatically in production mode (`Strict-Transport-Security: max-age=63072000; includeSubDomains`).
- NetworkPolicies to restrict lateral traffic (gateway <-> redis/nats/safety).
- Secrets stored in a proper secret manager (KMS, Vault, etc.).
- Rotate API keys and enterprise license material regularly.

### TLS Secrets (K8s)

The production K8s overlay expects these TLS secrets:

| Secret | Purpose |
|--------|---------|
| `cordum-nats-server-tls` | NATS server cert + key + CA |
| `cordum-redis-server-tls` | Redis server cert + key + CA |
| `cordum-client-tls` | Client cert + key + CA (for service-to-service mTLS) |
| `cordum-ingress-tls` | Ingress TLS termination |

Generate with cert-manager or manually:

```bash
# Example: self-signed CA for dev/staging
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 \
  -keyout ca.key -out ca.crt -days 365 -nodes -subj "/CN=Cordum CA"

# Server cert (e.g., for NATS)
openssl req -newkey ec -pkeyopt ec_paramgen_curve:P-256 \
  -keyout server.key -out server.csr -nodes \
  -subj "/CN=cordum-nats" \
  -addext "subjectAltName=DNS:cordum-nats,DNS:*.cordum-nats.cordum.svc"
openssl x509 -req -in server.csr -CA ca.crt -CAkey ca.key \
  -CAcreateserial -out server.crt -days 365 -copy_extensions copy

# Create K8s secret
kubectl create secret generic cordum-nats-server-tls \
  --from-file=tls.crt=server.crt \
  --from-file=tls.key=server.key \
  --from-file=ca.crt=ca.crt \
  -n cordum
```

## 4) Observability

### Metrics Endpoints

| Service | Port | Path | Env Override |
|---------|------|------|--------------|
| API Gateway | `:9092` | `/metrics` | `GATEWAY_METRICS_ADDR` |
| Scheduler | `:9090` | `/metrics` | `SCHEDULER_METRICS_ADDR` |
| Workflow Engine | `:9093` | `/health` | — |

In production, metrics endpoints bind to loopback unless explicitly set with `GATEWAY_METRICS_PUBLIC=1` or `SCHEDULER_METRICS_PUBLIC=1`.

### Health Checks

| Service | Endpoint | Protocol |
|---------|----------|----------|
| Gateway | `/health` | HTTP |
| Gateway gRPC | `/grpc.health.v1.Health/Check` | gRPC |
| Scheduler | metrics port | HTTP |
| Workflow Engine | `:9093/health` | HTTP |

### Key Prometheus Metrics

**Scheduler metrics** (namespace: `cordum`):

| Metric | Type | Description |
|--------|------|-------------|
| `cordum_jobs_received_total` | Counter | Jobs received by topic |
| `cordum_jobs_dispatched_total` | Counter | Jobs dispatched by topic |
| `cordum_jobs_completed_total` | Counter | Jobs completed by topic and status |
| `cordum_safety_denied_total` | Counter | Jobs denied by safety kernel |
| `cordum_safety_unavailable_total` | Counter | Jobs deferred (safety kernel unreachable) |
| `cordum_output_policy_quarantined_total` | Counter | Output policy quarantines |
| `cordum_output_denials_total` | Counter | Output policy denials/quarantines |
| `cordum_output_redactions_total` | Counter | Output policy redactions |
| `cordum_scheduler_dispatch_latency_seconds` | Histogram | Receive-to-dispatch latency |
| `cordum_output_check_latency_seconds` | Histogram | Output policy check latency by phase |
| `cordum_scheduler_stale_jobs` | Gauge | Stale jobs by state (reconciler) |
| `cordum_scheduler_active_goroutines` | Gauge | Active handler goroutines |
| `cordum_scheduler_orphan_replayed_total` | Counter | Orphaned PENDING jobs replayed |
| `cordum_saga_rollbacks_total` | Counter | Saga rollbacks triggered |
| `cordum_saga_compensation_failed_total` | Counter | Compensation dispatch failures |
| `cordum_saga_rollback_duration_seconds` | Histogram | Saga rollback duration |

**Gateway metrics** (namespace: `cordum`):

| Metric | Type | Description |
|--------|------|-------------|
| `cordum_http_requests_total` | Counter | HTTP requests by method/route/status |
| `cordum_http_request_duration_seconds` | Histogram | Request latency by method/route |

**Workflow metrics** (namespace: `cordum`):

| Metric | Type | Description |
|--------|------|-------------|
| `cordum_workflows_started_total` | Counter | Workflows started by name |
| `cordum_workflows_completed_total` | Counter | Workflows completed by name/status |
| `cordum_workflow_duration_seconds` | Histogram | Workflow duration by name |

### Centralized Logging

All Cordum services emit structured JSON logs via `log/slog`. Configure your log collector (Fluentd, Vector, Promtail) to ingest from stdout.

## 5) Operational Limits

- Configure pool timeouts and max retries (`config/timeouts.yaml`).
- Apply policy constraints (max runtime, max retries, artifact sizes).
- Configure rate limits on the gateway:

| Env Var | Default | Description |
|---------|---------|-------------|
| `API_RATE_LIMIT_RPS` | 100 | Authenticated API rate (requests/second) |
| `API_RATE_LIMIT_BURST` | 200 | Authenticated API burst |
| `API_PUBLIC_RATE_LIMIT_RPS` | 10 | Public endpoint rate |
| `API_PUBLIC_RATE_LIMIT_BURST` | 20 | Public endpoint burst |

Rate limit keys have a 30-minute TTL with cleanup every 5 minutes.

- Configure CORS allowed origins:

```bash
# Any of these env vars (checked in order)
CORDUM_ALLOWED_ORIGINS="https://dashboard.example.com"
CORDUM_CORS_ALLOW_ORIGINS="https://dashboard.example.com"
CORS_ALLOW_ORIGINS="https://dashboard.example.com"
```

## 6) Backup + Restore

### Backup Strategy

The production K8s overlay includes CronJobs for hourly backups:

**Redis RDB backup** (`cordum-redis-backup`):
```bash
# Runs hourly, connects via TLS, dumps RDB to /backup/
redis-cli --tls \
  --cacert /etc/cordum/tls/client/ca.crt \
  --cert /etc/cordum/tls/client/tls.crt \
  --key /etc/cordum/tls/client/tls.key \
  -h cordum-redis-0.cordum-redis.cordum.svc -p 6379 \
  --rdb /backup/redis-$(date -u +%Y%m%dT%H%M%SZ).rdb
```

**NATS JetStream snapshot** (`cordum-nats-backup`):
```bash
# Snapshots both streams
nats stream snapshot CORDUM_SYS /backup/nats-${ts}/CORDUM_SYS.snapshot
nats stream snapshot CORDUM_JOBS /backup/nats-${ts}/CORDUM_JOBS.snapshot
```

Both CronJobs use a shared 20Gi PVC (`cordum-backups`) and retain 2 successful + 2 failed job histories.

### Restore Procedures

**Redis restore from RDB**:

```bash
# 1. Scale down all Cordum services
kubectl scale deployment --all --replicas=0 -n cordum

# 2. Copy RDB to Redis pod
kubectl cp /path/to/redis-backup.rdb cordum/cordum-redis-0:/data/dump.rdb

# 3. Restart Redis (it loads dump.rdb on startup)
kubectl delete pod cordum-redis-0 -n cordum
# Wait for pod to restart and become ready

# 4. For Redis Cluster: repeat for each node, then run CLUSTER MEET
# 5. Scale Cordum services back up
kubectl scale deployment cordum-api-gateway --replicas=2 -n cordum
kubectl scale deployment cordum-scheduler --replicas=2 -n cordum
kubectl scale deployment cordum-safety-kernel --replicas=2 -n cordum
kubectl scale deployment cordum-workflow-engine --replicas=1 -n cordum
```

**NATS JetStream restore**:

```bash
# 1. Stop consumers (scale down scheduler + workflow engine)
kubectl scale deployment cordum-scheduler --replicas=0 -n cordum
kubectl scale deployment cordum-workflow-engine --replicas=0 -n cordum

# 2. Restore stream from snapshot
nats stream restore CORDUM_SYS /backup/nats-${ts}/CORDUM_SYS.snapshot \
  --server tls://cordum-nats:4222 \
  --tlscacert /etc/cordum/tls/client/ca.crt \
  --tlscert /etc/cordum/tls/client/tls.crt \
  --tlskey /etc/cordum/tls/client/tls.key

nats stream restore CORDUM_JOBS /backup/nats-${ts}/CORDUM_JOBS.snapshot \
  --server tls://cordum-nats:4222 \
  --tlscacert /etc/cordum/tls/client/ca.crt \
  --tlscert /etc/cordum/tls/client/tls.crt \
  --tlskey /etc/cordum/tls/client/tls.key

# 3. Scale services back up
kubectl scale deployment cordum-scheduler --replicas=2 -n cordum
kubectl scale deployment cordum-workflow-engine --replicas=1 -n cordum
```

**Configuration backup/restore**:

```bash
# Backup: export all config from Redis
redis-cli --tls ... GET cfg:system:default > config-backup.json
redis-cli --tls ... DUMP cordum:policies > policies-backup.rdb

# Restore: reimport
redis-cli --tls ... SET cfg:system:default "$(cat config-backup.json)"
# Note: cfg:system:default is write-once (bootstrapConfig). Delete the key
# first if you need to force a reload:
redis-cli --tls ... DEL cfg:system:default
```

### Backup Schedule Recommendations

| Data | RPO | Method | Schedule |
|------|-----|--------|----------|
| Redis (job state, workflows, DLQ, pointers) | 1 hour | RDB snapshot | Hourly CronJob |
| NATS JetStream (CORDUM_SYS, CORDUM_JOBS) | 1 hour | Stream snapshot | Hourly CronJob |
| Safety policies | On change | Git + Redis dump | CI/CD pipeline |
| TLS certificates | On rotation | Secret manager | Cert-manager auto-renewal |
| Configuration (system.yaml, pools.yaml) | On change | Git | Version control |

Document a restore drill and run it at least quarterly.

## 7) Upgrade Strategy

- Use versioned images and a staged rollout (dev -> staging -> prod).
- Validate schema/policy changes in staging before publish.
- Keep backward compatibility for workflow payloads.
- The production K8s overlay pins all images to `v0.1.0` — update `kustomization.yaml` images section for upgrades.

### Rolling Upgrade Procedure

```bash
# 1. Update image tags in kustomization.yaml
# 2. Apply to staging first
kubectl apply -k deploy/k8s/production -n cordum-staging

# 3. Run smoke test
bash ./tools/scripts/platform_smoke.sh

# 4. Apply to production
kubectl apply -k deploy/k8s/production -n cordum

# 5. Monitor rollout
kubectl rollout status deployment/cordum-api-gateway -n cordum
kubectl rollout status deployment/cordum-scheduler -n cordum
kubectl rollout status deployment/cordum-safety-kernel -n cordum
```

### Rollback

```bash
# Rollback a specific deployment
kubectl rollout undo deployment/cordum-api-gateway -n cordum

# Rollback all Cordum deployments
for dep in cordum-api-gateway cordum-scheduler cordum-safety-kernel cordum-workflow-engine; do
  kubectl rollout undo deployment/$dep -n cordum
done
```

## 8) Enterprise Gating (if applicable)

- Enforce `CORDUM_LICENSE_REQUIRED=true` for enterprise gateways.
- Keep public and enterprise images in separate registries.
- Audit all API keys and principal roles.

## 9) K8s Hardening (recommended)

- Requests/limits for every pod (already in `deploy/k8s/base.yaml`).
- PodDisruptionBudgets for replicated services.
- Non-root security contexts for Cordum services (already configured: `runAsNonRoot: true`, `readOnlyRootFilesystem: true`, `seccompProfile: RuntimeDefault`).
- Readiness/liveness probes on every workload.

## 10) Production K8s Overlay

```bash
kubectl apply -k deploy/k8s/production
```

The overlay swaps in stateful NATS/Redis, enables TLS/mTLS, applies network
policies, adds an ingress with TLS, and installs Prometheus ServiceMonitors +
rules (requires the Prometheus Operator CRDs).

Redis clustering uses `REDIS_CLUSTER_ADDRESSES` as seed nodes; update the list if
you change the Redis replica count.
JetStream replication is controlled by `NATS_JS_REPLICAS` (set to 3 in the
production overlay).

The overlay includes a `cordum-redis-cluster-init` Job that bootstraps the Redis
cluster once the pods are ready. Re-run it if you replace the cluster.

---

## 11) Incident Runbooks

### Runbook: Safety Kernel Unreachable

**Symptoms**: `cordum_safety_unavailable_total` rising, jobs stuck in PENDING.

**Impact**: The scheduler cannot evaluate safety policies. Jobs requiring safety checks are deferred with exponential backoff (up to `safetyThrottleDelay = 5s`). No jobs are auto-approved — the system fails closed.

**Steps**:

1. Check safety kernel pod status:
   ```bash
   kubectl get pods -l app=cordum-safety-kernel -n cordum
   kubectl logs -l app=cordum-safety-kernel -n cordum --tail=50
   ```

2. Verify gRPC connectivity:
   ```bash
   # From gateway pod
   grpcurl -cacert /etc/cordum/tls/client/ca.crt \
     cordum-safety-kernel:50051 grpc.health.v1.Health/Check
   ```

3. Check TLS configuration:
   ```bash
   # Verify env vars are set
   kubectl exec deploy/cordum-api-gateway -n cordum -- \
     env | grep SAFETY_KERNEL
   # Expected: SAFETY_KERNEL_ADDR, SAFETY_KERNEL_TLS_CA, etc.
   ```

4. If the kernel is crashlooping, check for policy parse errors:
   ```bash
   kubectl logs -l app=cordum-safety-kernel -n cordum | grep -i "error\|panic"
   ```

5. **Escalation**: If the safety kernel cannot be restored quickly, all new jobs will queue indefinitely. There is no bypass — this is by design (safety-first). Scale the safety kernel if it's a capacity issue:
   ```bash
   kubectl scale deployment cordum-safety-kernel --replicas=3 -n cordum
   ```

### Runbook: Redis Connection Lost

**Symptoms**: HTTP 500 errors across all endpoints, scheduler cannot persist state, DLQ unavailable.

**Impact**: Critical — Redis stores all job state, workflow state, DLQ, configuration, and pointer data. All services degrade.

**Steps**:

1. Check Redis pod status:
   ```bash
   kubectl get pods -l app=redis -n cordum
   kubectl logs -l app=redis -n cordum --tail=50
   ```

2. Test connectivity:
   ```bash
   redis-cli --tls \
     --cacert /etc/cordum/tls/client/ca.crt \
     --cert /etc/cordum/tls/client/tls.crt \
     --key /etc/cordum/tls/client/tls.key \
     -h cordum-redis-0.cordum-redis.cordum.svc PING
   ```

3. For Redis Cluster, check cluster health:
   ```bash
   redis-cli --tls ... CLUSTER INFO
   # Key fields:
   #   cluster_state:ok        — cluster is healthy
   #   cluster_slots_ok:16384  — all hash slots assigned
   #   cluster_known_nodes:6   — all nodes visible
   ```

4. Check individual node roles and replication:
   ```bash
   redis-cli --tls ... CLUSTER NODES
   # Each line shows: <id> <ip>:<port> <flags> <master-id> <ping-sent> <pong-recv> <epoch> <link-state> <slot-range>
   # Look for: 3 lines with "master", 3 with "slave", no "fail" or "fail?" flags
   ```

5. Check for stuck slot migrations:
   ```bash
   redis-cli --tls ... CLUSTER NODES | grep -E "migrating|importing"
   # Should return empty — any output means a rebalance is stuck
   ```

6. If nodes are down, check PVC status:
   ```bash
   kubectl get pvc -l app=redis -n cordum
   ```

7. **In-flight job impact**: Jobs in DISPATCHED or RUNNING state cannot transition. The reconciler will mark them as timed out once Redis is restored. Workers holding these jobs will see stale state.

8. **Data loss assessment**: Check the last successful backup timestamp:
   ```bash
   kubectl get jobs -l job-name=cordum-redis-backup -n cordum --sort-by=.status.startTime
   ```
   The backup CronJob backs up all primaries (not just node-0). Each backup produces per-shard files: `redis-{timestamp}-node{N}.rdb`.

9. **Recovery**: If a single Redis node is lost, the cluster auto-heals via automatic failover within 5s (`cluster-node-timeout`). For multi-node or full cluster loss, see the detailed DR runbooks in [Redis Operations Guide](./redis-operations.md#4-disaster-recovery-runbooks).

### Runbook: NATS Partition or Failure

**Symptoms**: Jobs not being dispatched, heartbeats missing, WebSocket stream silent.

**Impact**: NATS is the message bus for all job lifecycle events, heartbeats, and audit trail. A NATS failure halts job dispatch and worker communication.

**Steps**:

1. Check NATS cluster status:
   ```bash
   kubectl get pods -l app=cordum-nats -n cordum
   nats server list --server tls://cordum-nats:4222 ...
   ```

2. Check JetStream health:
   ```bash
   nats stream list --server tls://cordum-nats:4222 ...
   nats stream info CORDUM_SYS --server tls://cordum-nats:4222 ...
   nats stream info CORDUM_JOBS --server tls://cordum-nats:4222 ...
   ```

3. **Message delivery guarantees**: NATS JetStream provides at-least-once delivery for job requests. Messages published during a partition may be lost if not yet replicated. The reconciler (`cordum:reconciler:default`) will detect stuck jobs and re-queue them.

4. **Manual replay**: For orphaned PENDING jobs, the scheduler's pending replayer periodically scans and re-dispatches them. Monitor `cordum_scheduler_orphan_replayed_total` to confirm replay is working.

5. **Recovery**: If a NATS node is permanently lost:
   ```bash
   # Delete the failed pod (StatefulSet will recreate)
   kubectl delete pod cordum-nats-2 -n cordum
   # Wait for it to rejoin the cluster
   nats server list ...
   ```

### Runbook: DLQ Overflow

**Symptoms**: `cordum_dlq_depth` rising steadily, DLQ page in dashboard showing hundreds of entries.

**Impact**: Dead-lettered jobs are not being processed. May indicate a systemic issue (bad policy, broken worker pool, misconfigured topic routing).

**Steps**:

1. Check DLQ depth and recent entries:
   ```bash
   curl -H "X-API-Key: $CORDUM_API_KEY" -H "X-Tenant-ID: default" \
     https://cordum.example.com/api/v1/dlq?limit=10
   ```

2. **Triage**: Group entries by error reason:
   - `timeout: dispatched exceeded` — Workers not picking up jobs. Check pool health and topic routing.
   - `timeout: running exceeded` — Workers hanging. Check worker logs and increase timeout if legitimate.
   - `safety: denied` — Policy is blocking jobs. Review safety policy rules.
   - `output_quarantined` — Output policy flagged results. Review in the dashboard Quarantine tab.
   - `max retries exceeded` — Persistent failures. Check worker error logs.

3. **Bulk retry** (safe entries):
   ```bash
   # Retry a specific entry
   curl -X POST -H "X-API-Key: $CORDUM_API_KEY" -H "X-Tenant-ID: default" \
     https://cordum.example.com/api/v1/dlq/JOB_ID/retry

   # Delete entries that should not be retried
   curl -X DELETE -H "X-API-Key: $CORDUM_API_KEY" -H "X-Tenant-ID: default" \
     https://cordum.example.com/api/v1/dlq/JOB_ID
   ```

4. **Alerting threshold**: Set an alert when DLQ depth exceeds your SLO:
   ```yaml
   - alert: CordumDLQDepthHigh
     expr: cordum_dlq_entries > 100
     for: 10m
     labels:
       severity: warning
     annotations:
       summary: DLQ depth exceeds 100 entries
   ```

### Runbook: High Latency

**Symptoms**: `cordum_scheduler_dispatch_latency_seconds` p99 > 1s, `cordum_http_request_duration_seconds` p99 > 500ms, dashboard feels slow.

**Impact**: Job throughput degrades, workflow completion times increase, user experience suffers.

**Steps**:

1. **Identify the bottleneck**:
   ```promql
   # Gateway latency by route
   histogram_quantile(0.99, rate(cordum_http_request_duration_seconds_bucket[5m]))

   # Scheduler dispatch latency
   histogram_quantile(0.99, rate(cordum_scheduler_dispatch_latency_seconds_bucket[5m]))

   # Output policy check latency
   histogram_quantile(0.99, rate(cordum_output_check_latency_seconds_bucket[5m]))

   # Safety kernel evaluation (should be < 5ms p99)
   histogram_quantile(0.99, rate(cordum_output_eval_duration_seconds_bucket[5m]))
   ```

2. **Redis latency**: Check Redis slowlog:
   ```bash
   redis-cli --tls ... SLOWLOG GET 10
   redis-cli --tls ... INFO stats | grep instantaneous_ops_per_sec
   ```

3. **NATS throughput**: Check message rates:
   ```bash
   nats server report jetstream --server tls://cordum-nats:4222 ...
   ```

4. **Scaling triggers**:
   - Gateway CPU > 70% sustained: HPA should auto-scale (check HPA status)
   - Scheduler dispatch latency > 500ms: Consider adding scheduler replicas
   - Safety kernel latency > 5ms p99: Scale safety kernel replicas
   - Redis ops/sec > 80% of max: Vertical scale or add read replicas

5. **Quick wins**:
   - Increase gateway/scheduler replicas manually if HPA is too slow
   - Check for hot topics (single topic consuming all scheduler capacity)
   - Verify job lock contention: `cordum_scheduler_job_lock_wait_seconds`

### Runbook: Worker Pool Unresponsive

**Symptoms**: Jobs stuck in DISPATCHED state, heartbeats missing for a pool, `cordum_scheduler_stale_jobs{state="dispatched"}` rising.

**Steps**:

1. Check recent heartbeats for the pool:
   ```bash
   # Via the dashboard Agent Fleet page, or:
   curl -H "X-API-Key: $CORDUM_API_KEY" -H "X-Tenant-ID: default" \
     https://cordum.example.com/api/v1/workers
   ```

2. Verify workers are connected to NATS and subscribed to the correct topic.

3. The reconciler will automatically time out DISPATCHED jobs after `dispatchTimeout` (default: configurable via `config/timeouts.yaml`) and move them to FAILED/DLQ.

4. If the pool is permanently gone, DLQ the stuck jobs and reconfigure topic routing.

---

## 12) Scaling Guide

### Service Scaling Characteristics

| Service | Stateless? | Scaling Model | Recommended Replicas |
|---------|-----------|---------------|---------------------|
| API Gateway | Yes | Horizontal (HPA) | 2–10 |
| Safety Kernel | Yes | Horizontal | 2–5 |
| Scheduler | Semi (Redis lock) | Horizontal with lock | 2–5 |
| Workflow Engine | No (in-memory run state) | Cautious horizontal | 1–2 |
| Dashboard | Yes (static SPA) | Horizontal | 1–3 |
| NATS | No (JetStream state) | StatefulSet cluster | 3 (fixed) |
| Redis | No (data store) | Cluster (6 nodes) | 6 (3 primary + 3 replica) |

### Horizontal Scaling

**Gateway**: Fully stateless. Add replicas freely. Each instance handles HTTP + gRPC + WebSocket connections independently. The production overlay HPA scales on CPU (70%) and memory (80%).

**Safety Kernel**: Fully stateless — policies are loaded into memory from Redis/config and cached. Each instance evaluates independently. Scale when `cordum_output_eval_duration_seconds` p99 exceeds 5ms.

**Scheduler**: Multiple replicas are safe. The reconciler uses a distributed Redis lock (`cordum:reconciler:default`, TTL = 2x poll interval) so only one instance runs timeout checks at a time. Job handling is concurrent across all replicas via NATS queue groups. Scale when dispatch latency rises.

**Workflow Engine**: Holds in-memory run state during DAG execution. Scaling requires coordination — runs are not automatically redistributed. Use 1 replica for simplicity, 2 for HA with the understanding that crash recovery resumes from Redis-persisted state.

### Vertical Scaling

**Redis memory sizing**:
- Base: ~100 bytes per job record, ~500 bytes per workflow run, ~1KB per DLQ entry
- Estimate: 1 million concurrent jobs = ~100MB job state + context/result pointers
- Rule of thumb: 2x your estimated working set for headroom
- Monitor with: `redis-cli INFO memory`

**NATS JetStream storage**:
- Each JetStream stream has a configurable max size and message TTL
- Production overlay: 20Gi PVC per NATS node
- Monitor fill rate: `nats stream info CORDUM_JOBS`

### Capacity Planning

| Metric | Per Gateway Instance | Per Scheduler Instance |
|--------|---------------------|----------------------|
| HTTP requests/sec | ~5,000 (depends on payload size) | N/A |
| Job dispatches/sec | N/A | ~1,000 (depends on safety check latency) |
| WebSocket connections | ~10,000 (limited by file descriptors) | N/A |
| Safety evaluations/sec | N/A (proxied to kernel) | ~2,000 per kernel instance |

These are approximate — benchmark with your actual workload using `tools/scripts/platform_smoke.sh` as a starting point.

---

## 13) Monitoring Alerts

### Recommended Prometheus Alert Rules

Add these to your PrometheusRule resource alongside the platform alerts in `deploy/k8s/production/monitoring.yaml`:

```yaml
groups:
- name: cordum.operations
  rules:
  # Safety kernel evaluation latency
  - alert: CordumSafetyKernelSlow
    expr: |
      histogram_quantile(0.99,
        rate(cordum_output_eval_duration_seconds_bucket[5m])
      ) > 0.005
    for: 5m
    labels:
      severity: warning
    annotations:
      summary: Safety kernel p99 latency exceeds 5ms
      description: >
        Output policy evaluation is slower than the 5ms p99 target.
        Consider scaling safety kernel replicas.

  # DLQ depth
  - alert: CordumDLQDepthHigh
    expr: cordum_dlq_entries > 100
    for: 10m
    labels:
      severity: warning
    annotations:
      summary: DLQ depth exceeds 100 entries
      description: >
        Dead-letter queue has {{ $value }} entries.
        Triage and retry or delete entries.

  # Job failure rate
  - alert: CordumJobFailureRateHigh
    expr: |
      rate(cordum_jobs_completed_total{status="failed"}[5m])
      / rate(cordum_jobs_completed_total[5m]) > 0.1
    for: 10m
    labels:
      severity: warning
    annotations:
      summary: Job failure rate exceeds 10%
      description: >
        More than 10% of completed jobs are failing.
        Check worker logs and DLQ for root cause.

  # Worker heartbeat gaps
  - alert: CordumWorkerHeartbeatGap
    expr: |
      time() - cordum_last_heartbeat_timestamp > 60
    for: 5m
    labels:
      severity: warning
    annotations:
      summary: Worker heartbeat gap exceeds 60 seconds
      description: >
        A worker pool has not sent a heartbeat in over 60 seconds.
        Check worker connectivity and NATS health.

  # Dispatch latency
  - alert: CordumDispatchLatencyHigh
    expr: |
      histogram_quantile(0.99,
        rate(cordum_scheduler_dispatch_latency_seconds_bucket[5m])
      ) > 1
    for: 5m
    labels:
      severity: warning
    annotations:
      summary: Scheduler dispatch p99 latency exceeds 1 second
      description: >
        Jobs are taking more than 1 second to dispatch.
        Check scheduler capacity and Redis latency.

  # Saga compensation failures
  - alert: CordumSagaCompensationFailing
    expr: rate(cordum_saga_compensation_failed_total[10m]) > 0
    for: 10m
    labels:
      severity: warning
    annotations:
      summary: Saga compensation dispatches are failing
      description: >
        Compensation jobs are failing to dispatch.
        Check NATS connectivity and safety kernel availability.

  # Stale jobs (reconciler not clearing them)
  - alert: CordumStaleJobsHigh
    expr: cordum_scheduler_stale_jobs > 50
    for: 15m
    labels:
      severity: warning
    annotations:
      summary: More than 50 stale jobs detected
      description: >
        The reconciler has detected {{ $value }} stale jobs in state {{ $labels.state }}.
        Check reconciler logs and Redis connectivity.

  # Output policy quarantine spike
  - alert: CordumOutputQuarantineSpike
    expr: rate(cordum_output_policy_quarantined_total[5m]) > 1
    for: 5m
    labels:
      severity: warning
    annotations:
      summary: Output quarantine rate exceeds 1/second
      description: >
        Unusual number of outputs being quarantined.
        Review output policy rules and recent job outputs.
```

---

## 14) Security Hardening Checklist

| Area | Check | How to Verify |
|------|-------|---------------|
| TLS everywhere | All inter-service traffic encrypted | `kubectl exec ... -- env \| grep TLS` |
| API keys | Strong, unique keys per tenant | Check `CORDUM_API_KEYS` length (>= 32 hex chars) |
| Admin password | Strong password set | `CORDUM_ADMIN_PASSWORD` is non-empty, >= 12 chars |
| Policy signatures | Signature verification enabled | `SAFETY_POLICY_PUBLIC_KEY` is set |
| gRPC reflection | Disabled in production | `CORDUM_GRPC_REFLECTION` is unset or `0` |
| HSTS | Auto-enabled in production mode | Response includes `Strict-Transport-Security` header |
| CORS | Explicit allowlist (not `*`) | `CORDUM_ALLOWED_ORIGINS` lists specific domains |
| Rate limiting | Configured per environment | `API_RATE_LIMIT_RPS` / `API_RATE_LIMIT_BURST` set |
| Network policies | Lateral traffic restricted | `kubectl get networkpolicy -n cordum` |
| Dashboard API key embed | Disabled | `CORDUM_DASHBOARD_EMBED_API_KEY` is unset |
| Secrets management | External secret store | No plaintext secrets in manifests |
| Security contexts | Non-root, read-only filesystem | Check pod spec `securityContext` |
| Key rotation | Schedule documented | API keys and TLS certs have rotation schedule |

---

## 15) Runbook Checklist

Pre-go-live verification:

- [ ] Smoke test the platform (`bash ./tools/scripts/platform_smoke.sh`)
- [ ] Verify DLQ + retry flow
- [ ] Verify policy evaluate/simulate/explain endpoints
- [ ] Confirm audit trail (run timeline + approval metadata)
- [ ] Test backup + restore drill
- [ ] Verify all Prometheus alerts are firing correctly (use `amtool` or Alertmanager UI)
- [ ] Load test at expected peak (use `hey` or `k6` against the gateway)
- [ ] Verify graceful shutdown (drain connections, complete in-flight jobs)
- [ ] Confirm log aggregation pipeline captures all services
- [ ] Review network policies block unexpected traffic

---

## Environment Variables Reference

Complete list of production-relevant environment variables:

| Variable | Service | Description |
|----------|---------|-------------|
| `CORDUM_ENV` | All | Set to `production` to enable production mode |
| `CORDUM_PRODUCTION` | All | Alternative: set to `true` for production mode |
| `CORDUM_API_KEY` | Gateway | Single API key for authentication |
| `CORDUM_API_KEYS` | Gateway | Comma-separated list of API keys |
| `CORDUM_USER_AUTH_ENABLED` | Gateway | Enable user/password authentication |
| `CORDUM_ADMIN_PASSWORD` | Gateway | Admin user password (required if user auth enabled) |
| `CORDUM_ALLOWED_ORIGINS` | Gateway | CORS allowed origins (comma-separated) |
| `CORDUM_TLS_MIN_VERSION` | All | Minimum TLS version (`1.2` or `1.3`; defaults to 1.3 in production) |
| `CORDUM_GRPC_REFLECTION` | Gateway | Enable gRPC reflection (`1` to enable) |
| `CORDUM_DASHBOARD_EMBED_API_KEY` | Gateway | Embed API key in dashboard JS (DO NOT use in production) |
| `CORDUM_ALLOW_HEADER_PRINCIPAL` | Gateway | Allow `X-Principal` header override (disabled in production) |
| `GATEWAY_HTTP_ADDR` | Gateway | HTTP listen address (default `:8081`) |
| `GATEWAY_GRPC_ADDR` | Gateway | gRPC listen address (default `:50051`) |
| `GATEWAY_METRICS_ADDR` | Gateway | Metrics listen address (default `:9092`) |
| `GATEWAY_METRICS_PUBLIC` | Gateway | Allow public metrics bind (`1` to allow) |
| `GATEWAY_HTTP_TLS_CERT` | Gateway | Path to HTTP TLS certificate |
| `GATEWAY_HTTP_TLS_KEY` | Gateway | Path to HTTP TLS key |
| `SAFETY_KERNEL_ADDR` | Gateway, Scheduler | Safety kernel gRPC address |
| `SAFETY_KERNEL_TLS_REQUIRED` | Gateway, Scheduler | Require TLS for safety kernel connection |
| `SAFETY_KERNEL_TLS_CA` | Gateway, Scheduler | CA cert for safety kernel TLS |
| `SAFETY_POLICY_PUBLIC_KEY` | Safety Kernel | ECDSA public key for policy signature verification |
| `SAFETY_POLICY_SIGNATURE_REQUIRED` | Safety Kernel | Require policy signatures |
| `SCHEDULER_METRICS_ADDR` | Scheduler | Metrics listen address (default `:9090`) |
| `SCHEDULER_METRICS_PUBLIC` | Scheduler | Allow public metrics bind (`1` to allow) |
| `API_RATE_LIMIT_RPS` | Gateway | Authenticated API rate limit (requests/sec) |
| `API_RATE_LIMIT_BURST` | Gateway | Authenticated API burst limit |
| `API_PUBLIC_RATE_LIMIT_RPS` | Gateway | Public endpoint rate limit |
| `API_PUBLIC_RATE_LIMIT_BURST` | Gateway | Public endpoint burst limit |
| `NATS_TLS_CA` | All | NATS TLS CA certificate path |
| `NATS_TLS_CERT` | All | NATS TLS client certificate path |
| `NATS_TLS_KEY` | All | NATS TLS client key path |
| `NATS_JS_REPLICAS` | Scheduler | JetStream replication factor |
| `REDIS_TLS_CA` | All | Redis TLS CA certificate path |
| `REDIS_TLS_CERT` | All | Redis TLS client certificate path |
| `REDIS_TLS_KEY` | All | Redis TLS client key path |
| `REDIS_CLUSTER_ADDRESSES` | All | Redis cluster seed node addresses |
| `CORDUM_LICENSE_REQUIRED` | Gateway | Enforce enterprise license check |

---

## Related Docs

- [k8s-deployment.md](k8s-deployment.md) — Detailed Kubernetes manifest walkthrough
- [configuration.md](configuration.md) — Config files and environment variables overview
- [configuration-reference.md](configuration-reference.md) — Complete config schema reference
- [safety-kernel.md](safety-kernel.md) — Safety kernel deep reference
- [scheduler-internals.md](scheduler-internals.md) — Scheduler engine internals
- [output-policy.md](output-policy.md) — Output policy architecture
- [DOCKER.md](DOCKER.md) — Docker Compose deployment
- [production-gate.md](production-gate.md) — Production gate script

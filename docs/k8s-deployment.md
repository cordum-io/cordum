# Kubernetes Deployment Guide

Detailed guide for deploying Cordum on Kubernetes — from dev-mode single-apply to production-hardened overlays with TLS, clustering, monitoring, and backups.

> For Docker Compose local development, see [DOCKER.md](DOCKER.md).
> For the production readiness checklist, see [production.md](production.md).
> For Helm chart deployment, see [helm.md](helm.md).

---

## Directory Structure

```
deploy/k8s/
├── base.yaml                          # All-in-one dev manifest
├── ingress.yaml                       # Dev ingress (no TLS)
├── local-patches.yaml                 # Local dev overrides
├── local/
│   └── kustomization.yaml             # Local kustomize overlay
├── production/
│   ├── kustomization.yaml             # Production overlay entry point
│   ├── nats.yaml                      # NATS 3-node StatefulSet + TLS cluster
│   ├── redis.yaml                     # Redis 6-node cluster + TLS + exporter
│   ├── ingress.yaml                   # Ingress with TLS termination
│   ├── ha.yaml                        # PDBs + HPAs
│   ├── monitoring.yaml                # ServiceMonitors + PrometheusRules
│   ├── networkpolicy.yaml             # Ingress/egress network policies
│   ├── backup.yaml                    # CronJob backups (Redis RDB + NATS snapshots)
│   └── patches/
│       ├── delete-nats-deployment.yaml    # Remove dev NATS Deployment
│       ├── delete-redis-deployment.yaml   # Remove dev Redis Deployment
│       ├── tls-env.yaml                   # Inject TLS env vars + volume mounts
│       ├── service-labels.yaml            # Add app labels to Services
│       └── pod-anti-affinity.yaml         # Spread pods across nodes
└── README.md
```

---

## 1. Base Manifests (Dev/Staging)

`deploy/k8s/base.yaml` is a single-file manifest containing all resources for a minimal deployment. Apply it directly for dev or staging:

```bash
kubectl apply -f deploy/k8s/base.yaml
```

### Resource Inventory

| Resource | Kind | Ports | Probe |
|----------|------|-------|-------|
| `nats` | Deployment (1 replica) | 4222 (client) | TCP :4222 |
| `redis` | Deployment (1 replica) | 6379 | TCP :6379 |
| `cordum-context-engine` | Deployment (1 replica) | 50070 (gRPC) | gRPC :50070 |
| `cordum-safety-kernel` | Deployment (1 replica) | 50051 (gRPC) | gRPC :50051 |
| `cordum-scheduler` | Deployment (1 replica) | 9090 (metrics) | HTTP /metrics :9090 |
| `cordum-api-gateway` | Deployment (1 replica) | 8080 (gRPC), 8081 (HTTP), 9092 (metrics) | HTTP /health :8081 |
| `cordum-workflow-engine` | Deployment (1 replica) | 9093 (HTTP) | HTTP /health :9093 |
| `cordum-dashboard` | Deployment (1 replica) | 8080 (HTTP) | HTTP / :8080 |

### ConfigMaps

| ConfigMap | Mount Path | Description |
|-----------|-----------|-------------|
| `cordum-pools` | `/etc/cordum/pools.yaml` | Topic-to-pool routing |
| `cordum-timeouts` | `/etc/cordum/timeouts.yaml` | Dispatch/running timeouts, reconciler interval |
| `cordum-nats-config` | `/etc/nats/nats.conf` | NATS server config (JetStream sync) |
| `cordum-safety` | `/etc/cordum/safety.yaml` | Safety kernel policy |

### Secrets

| Secret | Keys | Used By |
|--------|------|---------|
| `cordum-api-key` | `API_KEY` | Gateway, Dashboard |
| `cordum-admin-creds` | `CORDUM_ADMIN_USERNAME`, `CORDUM_ADMIN_PASSWORD`, `CORDUM_ADMIN_EMAIL` | Gateway (optional) |

**Important**: Set `API_KEY` to a strong value before applying:

```bash
kubectl create secret generic cordum-api-key \
  --namespace cordum \
  --from-literal=API_KEY="$(openssl rand -hex 32)"
```

### Security Defaults

All Cordum service pods run with hardened security contexts:

```yaml
securityContext:
  runAsNonRoot: true
  runAsUser: 65532
  runAsGroup: 65532
  fsGroup: 65532
  seccompProfile:
    type: RuntimeDefault
containers:
- securityContext:
    readOnlyRootFilesystem: true
    allowPrivilegeEscalation: false
```

### ServiceAccount

All pods use a dedicated `cordum` ServiceAccount with `automountServiceAccountToken: false`. No Cordum service needs Kubernetes API access, so the API token is not mounted into pods. This follows the principle of least privilege — compromised containers cannot access the K8s API or read cluster secrets.

If a future service needs K8s API access (e.g., for leader election), create a separate ServiceAccount with a scoped Role/RoleBinding.

### Resource Quotas

The base manifest includes a `ResourceQuota` and `LimitRange` for the `cordum` namespace:

| Quota | Value | Notes |
|-------|-------|-------|
| `requests.cpu` | 8 | Total CPU requests across all pods |
| `limits.cpu` | 16 | Total CPU limits |
| `requests.memory` | 8Gi | Total memory requests |
| `limits.memory` | 16Gi | Total memory limits |
| `pods` | 50 | Max pod count (accommodates HPA max replicas + headroom) |
| `services` | 20 | Max Service count |
| `persistentvolumeclaims` | 10 | Max PVC count |

The `LimitRange` assigns default resource requests (100m CPU, 128Mi memory) and limits (500m CPU, 256Mi memory) to containers that don't specify them.

**Adjusting for larger deployments:** Increase the ResourceQuota values in `base.yaml` or apply a kustomize patch in your production overlay:

```yaml
# In production/kustomization.yaml patches:
- target:
    kind: ResourceQuota
    name: cordum-quota
  patch: |
    - op: replace
      path: /spec/hard/pods
      value: "100"
    - op: replace
      path: /spec/hard/limits.cpu
      value: "32"
```

### Resource Requests/Limits

| Service | CPU Request | CPU Limit | Memory Request | Memory Limit |
|---------|-------------|-----------|----------------|--------------|
| NATS | 100m | 500m | 128Mi | 512Mi |
| Redis | 100m | 500m | 256Mi | 512Mi |
| Context Engine | 100m | 500m | 128Mi | 512Mi |
| Safety Kernel | 100m | 500m | 128Mi | 512Mi |
| Scheduler | 150m | 750m | 256Mi | 768Mi |
| API Gateway | 200m | 1000m | 256Mi | 1Gi |
| Workflow Engine | 150m | 750m | 256Mi | 768Mi |
| Dashboard | 100m | 500m | 128Mi | 512Mi |

### Dev Ingress

For local dev with an Ingress controller:

```bash
kubectl apply -f deploy/k8s/ingress.yaml
```

Routes:
- `/api/v1/*` and `/health` → `cordum-api-gateway:8081`
- `/` → `cordum-dashboard:8080`

---

## 2. Production Overlay

The production overlay replaces dev-mode NATS/Redis with clustered StatefulSets, adds TLS everywhere, enables HA (PDBs + HPAs), monitoring, network policies, and automated backups.

### Apply

```bash
kubectl apply -k deploy/k8s/production
```

### What the Overlay Does

The `production/kustomization.yaml` composes:

**Resources added:**
- `nats.yaml` — 3-node NATS cluster (StatefulSet) with mTLS and JetStream persistence
- `redis.yaml` — 6-node Redis cluster (StatefulSet) with TLS, exporter sidecar, and init job
- `ingress.yaml` — TLS-terminated Ingress
- `ha.yaml` — PodDisruptionBudgets + HorizontalPodAutoscalers
- `monitoring.yaml` — ServiceMonitors + PrometheusRules (requires Prometheus Operator)
- `networkpolicy.yaml` — Ingress and egress network policies
- `backup.yaml` — Hourly CronJob backups for Redis and NATS

**Patches applied:**
- `delete-nats-deployment.yaml` — Removes dev single-node NATS Deployment (replaced by StatefulSet)
- `delete-redis-deployment.yaml` — Removes dev single-node Redis Deployment (replaced by StatefulSet)
- `tls-env.yaml` — Injects TLS env vars and client cert volume mounts into all services
- `service-labels.yaml` — Adds `app` labels to Services (required for ServiceMonitor selectors)
- `pod-anti-affinity.yaml` — Spreads all pods across nodes via `preferredDuringSchedulingIgnoredDuringExecution`

**Image tags:**
```yaml
images:
  - name: cordum-context-engine
    newTag: v0.1.0
  - name: cordum-safety-kernel
    newTag: v0.1.0
  - name: cordum-scheduler
    newTag: v0.1.0
  - name: cordum-api-gateway
    newTag: v0.1.0
  - name: cordum-workflow-engine
    newTag: v0.1.0
  - name: cordum-dashboard
    newTag: v0.1.0
```

**Replica overrides:**
```yaml
replicas:
  - name: cordum-api-gateway
    count: 2
  - name: cordum-safety-kernel
    count: 2
  - name: cordum-scheduler
    count: 2
```

---

## 3. Secrets Management

### Required Secrets

| Secret | Keys | Purpose |
|--------|------|---------|
| `cordum-api-key` | `API_KEY` | API authentication |
| `cordum-admin-creds` | `CORDUM_ADMIN_USERNAME`, `CORDUM_ADMIN_PASSWORD`, `CORDUM_ADMIN_EMAIL` | User auth (optional) |
| `cordum-nats-server-tls` | `tls.crt`, `tls.key`, `ca.crt` | NATS server TLS |
| `cordum-redis-server-tls` | `tls.crt`, `tls.key`, `ca.crt` | Redis server TLS |
| `cordum-client-tls` | `tls.crt`, `tls.key`, `ca.crt` | Client certs for services connecting to NATS/Redis |
| `cordum-ingress-tls` | `tls.crt`, `tls.key` | Ingress TLS termination |

### Creating TLS Secrets

Generate a CA and service certificates (example using `openssl`):

```bash
# Generate CA
openssl genrsa -out ca.key 4096
openssl req -new -x509 -key ca.key -sha256 -days 3650 \
  -subj "/CN=Cordum Internal CA" -out ca.crt

# Generate NATS server cert
openssl genrsa -out nats.key 2048
openssl req -new -key nats.key -subj "/CN=nats" \
  -addext "subjectAltName=DNS:nats,DNS:cordum-nats,DNS:cordum-nats-0.cordum-nats.cordum.svc,DNS:cordum-nats-1.cordum-nats.cordum.svc,DNS:cordum-nats-2.cordum-nats.cordum.svc,DNS:*.cordum-nats.cordum.svc" \
  -out nats.csr
openssl x509 -req -in nats.csr -CA ca.crt -CAkey ca.key -CAcreateserial \
  -days 365 -sha256 -copy_extensions copyall -out nats.crt

# Generate Redis server cert
openssl genrsa -out redis.key 2048
openssl req -new -key redis.key -subj "/CN=redis" \
  -addext "subjectAltName=DNS:redis,DNS:cordum-redis,DNS:*.cordum-redis.cordum.svc" \
  -out redis.csr
openssl x509 -req -in redis.csr -CA ca.crt -CAkey ca.key -CAcreateserial \
  -days 365 -sha256 -copy_extensions copyall -out redis.crt

# Generate client cert (used by all Cordum services)
openssl genrsa -out client.key 2048
openssl req -new -key client.key -subj "/CN=cordum-client" -out client.csr
openssl x509 -req -in client.csr -CA ca.crt -CAkey ca.key -CAcreateserial \
  -days 365 -sha256 -out client.crt

# Create Kubernetes secrets
kubectl create secret generic cordum-nats-server-tls --namespace cordum \
  --from-file=tls.crt=nats.crt --from-file=tls.key=nats.key --from-file=ca.crt=ca.crt

kubectl create secret generic cordum-redis-server-tls --namespace cordum \
  --from-file=tls.crt=redis.crt --from-file=tls.key=redis.key --from-file=ca.crt=ca.crt

kubectl create secret generic cordum-client-tls --namespace cordum \
  --from-file=tls.crt=client.crt --from-file=tls.key=client.key --from-file=ca.crt=ca.crt
```

For **cert-manager** automation, create `Certificate` resources referencing a `ClusterIssuer`:

```yaml
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: cordum-nats-server-tls
  namespace: cordum
spec:
  secretName: cordum-nats-server-tls
  issuerRef:
    name: cordum-ca-issuer
    kind: ClusterIssuer
  commonName: nats
  dnsNames:
    - nats
    - cordum-nats
    - "*.cordum-nats.cordum.svc"
```

### Certificate Rotation

1. Regenerate certs (or let cert-manager auto-renew)
2. Update the Secret: `kubectl create secret generic ... --dry-run=client -o yaml | kubectl apply -f -`
3. Restart affected pods: `kubectl rollout restart deployment -n cordum`

---

## 4. NATS Clustering

The production overlay deploys a 3-node NATS cluster as a StatefulSet.

### Configuration

- **Replicas**: 3 (`cordum-nats-0`, `cordum-nats-1`, `cordum-nats-2`)
- **Headless Service**: `cordum-nats` (DNS-based peer discovery)
- **JetStream**: Enabled with 20Gi persistent volume per node
- **TLS**: Full mTLS between clients and cluster peers
- **Cluster routes**: Hardcoded in ConfigMap for deterministic discovery
- **Monitoring**: Port 8222 exposed for `/healthz` probes and metrics
- **Anti-affinity**: Pods spread across nodes via `preferredDuringSchedulingIgnoredDuringExecution`

### NATS ConfigMap (production)

```
port: 4222
http: 8222
jetstream {
  store_dir: /data/jetstream
  sync_interval: "1s"
}
tls {
  cert_file: /etc/nats/tls/tls.crt
  key_file: /etc/nats/tls/tls.key
  ca_file: /etc/nats/tls/ca.crt
  verify: true
}
cluster {
  name: cordum
  port: 6222
  routes = [
    nats://cordum-nats-0.cordum-nats.cordum.svc:6222
    nats://cordum-nats-1.cordum-nats.cordum.svc:6222
    nats://cordum-nats-2.cordum-nats.cordum.svc:6222
  ]
  tls { ... }
}
```

### JetStream Durability

- `sync_interval: "1s"` — fsync every second (trade-off: lower = more durable, slower)
- Streams are replicated across all 3 nodes (`NATS_JS_REPLICAS=3` in the TLS patch)
- PVCs: 20Gi `ReadWriteOnce` per node

### Tuning

| Parameter | Default | Notes |
|-----------|---------|-------|
| `sync_interval` | `1s` | Lower for stricter durability, higher for throughput |
| `NATS_JS_REPLICAS` | `3` | Must not exceed cluster size |
| Storage per node | `20Gi` | Increase for high-volume deployments |

---

## 5. Redis Clustering

The production overlay deploys a 6-node Redis cluster (3 masters + 3 replicas).

### Configuration

- **Replicas**: 6 (StatefulSet `cordum-redis`)
- **Headless Service**: `cordum-redis` (DNS-based discovery)
- **TLS**: Full TLS with `tls-auth-clients yes`
- **Persistence**: AOF enabled (`appendonly yes`), 20Gi PVC per node
- **Cluster mode**: `cluster-enabled yes`, `cluster-node-timeout 5000`
- **Exporter sidecar**: `oliver006/redis_exporter:v1.58.0` on port 9121

### Cluster Init Job

After all 6 Redis pods are running, the `cordum-redis-cluster-init` Job must complete once:

```bash
# Check pod readiness
kubectl get pods -n cordum -l app=redis

# The init job runs automatically — check its status
kubectl get job cordum-redis-cluster-init -n cordum
kubectl logs job/cordum-redis-cluster-init -n cordum
```

The init job:
1. Waits for all 6 nodes to respond to `PING`
2. Runs `redis-cli --cluster create ... --cluster-replicas 1 --cluster-yes`

**Re-running**: Delete the job and re-create it if you need to re-initialize:
```bash
kubectl delete job cordum-redis-cluster-init -n cordum
kubectl apply -k deploy/k8s/production
```

### Client Connection

All Cordum services connect to Redis cluster via:
```
REDIS_CLUSTER_ADDRESSES=cordum-redis-0.cordum-redis.cordum.svc:6379,...,cordum-redis-5.cordum-redis.cordum.svc:6379
REDIS_URL=rediss://redis:6379
REDIS_TLS_CA=/etc/cordum/tls/client/ca.crt
REDIS_TLS_CERT=/etc/cordum/tls/client/tls.crt
REDIS_TLS_KEY=/etc/cordum/tls/client/tls.key
```

---

## 6. Network Policies

The production overlay defines strict ingress and egress rules per service.

### Ingress Rules

| Target | Allowed Sources | Ports |
|--------|----------------|-------|
| NATS | All Cordum services + NATS peers | 4222 (client), 6222 (cluster), 8222 (monitor) |
| Redis | All Cordum services + Redis peers | 6379 (client), 16379 (cluster bus) |

### Egress Rules

| Source | Allowed Destinations | Ports |
|--------|---------------------|-------|
| API Gateway | NATS, Redis, Safety Kernel, DNS | 4222, 6379, 50051, 53 |
| Scheduler | NATS, Redis, Safety Kernel, DNS | 4222, 6379, 50051, 53 |
| Workflow Engine | NATS, Redis, DNS | 4222, 6379, 53 |
| Safety Kernel | NATS, Redis, DNS | 4222, 6379, 53 |
| Dashboard | API Gateway, DNS | 8081, 53 |

### Traffic Flow Diagram

```
                    ┌──────────┐
    Internet ──────►│ Ingress  │
                    └────┬─────┘
                         │
              ┌──────────┼──────────┐
              ▼                     ▼
      ┌───────────────┐    ┌───────────────┐
      │  API Gateway  │    │   Dashboard   │
      │  :8081 :8080  │◄───│     :8080     │
      │    :9092      │    └───────────────┘
      └──┬────┬───┬───┘
         │    │   │
    ┌────┘    │   └────┐
    ▼         ▼        ▼
┌───────┐ ┌───────┐ ┌──────────────┐
│ NATS  │ │ Redis │ │Safety Kernel │
│ :4222 │ │ :6379 │ │   :50051     │
│ :6222 │ │:16379 │ └──────┬───┬───┘
└───┬───┘ └───┬───┘        │   │
    │         │        ┌───┘   └───┐
    ▼         ▼        ▼           ▼
┌──────────┐      ┌───────┐   ┌───────┐
│Scheduler │      │ NATS  │   │ Redis │
│  :9090   │      └───────┘   └───────┘
└──┬───┬───┘
   │   │
   ▼   ▼
┌───────┐ ┌───────┐
│ NATS  │ │ Redis │
└───────┘ └───────┘

┌─────────────────┐
│ Workflow Engine  │──► NATS, Redis
│     :9093       │
└─────────────────┘

┌─────────────────┐
│ Context Engine  │──► Redis
│    :50070       │
└─────────────────┘
```

---

## 7. Ingress Configuration

### Dev Ingress (no TLS)

```bash
kubectl apply -f deploy/k8s/ingress.yaml
```

Routes `/api/v1/*` and `/health` to the gateway, `/` to the dashboard. No TLS.

### Production Ingress (TLS)

The production overlay includes `production/ingress.yaml` with:

```yaml
annotations:
  nginx.ingress.kubernetes.io/force-ssl-redirect: "true"
  nginx.ingress.kubernetes.io/ssl-protocols: "TLSv1.2 TLSv1.3"
  nginx.ingress.kubernetes.io/proxy-read-timeout: "3600"   # WebSocket support
  nginx.ingress.kubernetes.io/proxy-send-timeout: "3600"
spec:
  tls:
  - hosts:
    - cordum.example.com
    secretName: cordum-ingress-tls
```

**Before applying**, update the hostname from `cordum.example.com` to your actual domain.

Create the Ingress TLS secret:

```bash
kubectl create secret tls cordum-ingress-tls --namespace cordum \
  --cert=cordum-ingress.crt --key=cordum-ingress.key
```

### Annotations for Other Ingress Controllers

**Traefik:**
```yaml
annotations:
  traefik.ingress.kubernetes.io/router.tls: "true"
  traefik.ingress.kubernetes.io/router.entrypoints: websecure
```

**Istio (VirtualService):** Use an Istio `Gateway` + `VirtualService` instead of the Ingress resource.

---

## 8. Monitoring

The production overlay deploys Prometheus Operator CRDs.

**Prerequisite**: Install the [Prometheus Operator](https://github.com/prometheus-operator/prometheus-operator) or [kube-prometheus-stack](https://github.com/prometheus-community/helm-charts/tree/main/charts/kube-prometheus-stack) Helm chart.

### ServiceMonitors

| Target | Port | Path | Interval |
|--------|------|------|----------|
| `cordum-api-gateway` | `metrics` (9092) | `/metrics` | 30s |
| `cordum-scheduler` | `metrics` (9090) | `/metrics` | 30s |
| `cordum-nats-monitor` | `monitor` (8222) | `/metrics` | 30s |
| `cordum-redis` (exporter) | `metrics` (9121) | `/metrics` | 30s |

### Alert Rules (PrometheusRule)

| Alert | Expression | Severity | For |
|-------|-----------|----------|-----|
| `CordumGatewayDown` | `sum(up{service="cordum-api-gateway"}) == 0` | critical | 5m |
| `CordumSchedulerDown` | `sum(up{service="cordum-scheduler"}) == 0` | critical | 5m |
| `CordumNATSDown` | `sum(up{service="cordum-nats-monitor"}) == 0` | critical | 5m |
| `CordumRedisDown` | `sum(up{service="cordum-redis"}) == 0` | critical | 5m |

### Key Metrics

| Metric | Source | Description |
|--------|--------|-------------|
| `cordum_jobs_dispatched_total` | Scheduler | Jobs dispatched by pool/type |
| `cordum_jobs_duration_seconds` | Scheduler | Job latency histogram |
| `cordum_safety_evaluations_total` | Gateway | Policy evaluations by decision |
| `cordum_http_requests_total` | Gateway | HTTP requests by method/path/status |

---

## 9. Scaling

### HorizontalPodAutoscalers

| Service | Min | Max | CPU Target | Memory Target |
|---------|-----|-----|-----------|--------------|
| `cordum-api-gateway` | 2 | 10 | 70% | 80% |
| `cordum-scheduler` | 2 | 10 | 70% | 80% |

### Scaling Recommendations

| Service | Scale When | Notes |
|---------|-----------|-------|
| API Gateway | High HTTP request volume | Stateless — scales freely |
| Safety Kernel | High policy evaluation load | Stateless — scales freely |
| Scheduler | Large job backlogs | Stateful leader election — multiple replicas use Redis locking |
| Workflow Engine | Many concurrent workflow runs | Single instance recommended unless using Redis-based coordination |
| Context Engine | High context read/write volume | Stateless — scales freely |
| Dashboard | High user traffic | Static assets — scales freely |
| NATS | N/A | Fixed 3-node cluster; increase storage, not replicas |
| Redis | N/A | Fixed 6-node cluster; increase storage or reshard |

### PodDisruptionBudgets

All critical services have PDBs with `maxUnavailable: 1`:
- `cordum-api-gateway`
- `cordum-scheduler`
- `cordum-workflow-engine`
- `cordum-safety-kernel`
- `cordum-dashboard`

Infrastructure StatefulSets use `minAvailable` to preserve quorum and data availability:

| StatefulSet | `minAvailable` | Rationale |
|-------------|---------------|-----------|
| NATS (3 nodes) | 2 | Maintains Raft quorum during node drains — losing 2 of 3 nodes would break consensus |
| Redis (6 nodes: 3 primary + 3 replica) | 4 | Ensures at least 2 primary + 2 replica survive, maintaining data availability during rolling upgrades |

See [Horizontal Scaling Guide](./horizontal-scaling.md) for NATS delivery semantics and multi-replica considerations.

---

## 10. Backups

The production overlay includes hourly CronJob backups for both Redis and NATS.

### Redis Backup

- **Schedule**: `0 * * * *` (hourly)
- **Method**: `redis-cli --rdb` creates an RDB snapshot from `cordum-redis-0`
- **Storage**: `cordum-backups` PVC (20Gi)
- **Retention**: Last 2 successful + 2 failed jobs kept
- **File format**: `redis-YYYYMMDDTHHMMSSZ.rdb`

### NATS JetStream Backup

- **Schedule**: `0 * * * *` (hourly)
- **Method**: `nats stream snapshot` for `CORDUM_SYS` and `CORDUM_JOBS` streams
- **Storage**: Same `cordum-backups` PVC
- **File format**: `nats-YYYYMMDDTHHMMSSZ/{stream}.snapshot`

### Restore Procedures

**Redis:**
```bash
# Stop the cluster, copy RDB to data dir, restart
kubectl cp backup/redis-20260213T120000Z.rdb cordum/cordum-redis-0:/data/dump.rdb
kubectl delete pod cordum-redis-0 -n cordum  # Pod restarts and loads RDB
```

**NATS:**
```bash
# Restore a stream snapshot
kubectl exec -n cordum cordum-nats-0 -- nats stream restore CORDUM_JOBS /backup/nats-.../CORDUM_JOBS.snapshot
```

---

## 11. Upgrade Procedures

### Rolling Updates

All Cordum Deployments use the default `RollingUpdate` strategy. To upgrade:

1. Update image tags in `production/kustomization.yaml`
2. Re-apply: `kubectl apply -k deploy/k8s/production`
3. Watch rollout: `kubectl rollout status deployment/cordum-api-gateway -n cordum`

### Pre-Upgrade Checklist

1. Back up Redis and NATS (or verify CronJob backups are recent)
2. Review changelog for breaking changes
3. Check that PDBs will allow the rolling update given current replica counts
4. If config schema changed, update ConfigMaps before rolling deployments

### Rollback

```bash
kubectl rollout undo deployment/cordum-api-gateway -n cordum
kubectl rollout undo deployment/cordum-scheduler -n cordum
kubectl rollout undo deployment/cordum-safety-kernel -n cordum
kubectl rollout undo deployment/cordum-workflow-engine -n cordum
kubectl rollout undo deployment/cordum-context-engine -n cordum
kubectl rollout undo deployment/cordum-dashboard -n cordum
```

---

## 12. Troubleshooting

### Common Issues

| Symptom | Likely Cause | Fix |
|---------|-------------|-----|
| `ImagePullBackOff` | Missing image or wrong tag | Check `kustomization.yaml` image tags and registry access |
| `CrashLoopBackOff` on gateway | Missing `API_KEY` secret | Create `cordum-api-key` secret |
| `CrashLoopBackOff` on scheduler | Can't reach NATS/Redis | Check service DNS, network policies, TLS certs |
| Redis cluster init job stuck | Pods not ready yet | Wait for all 6 Redis pods, then delete/recreate the job |
| NATS `connection refused` | TLS mismatch | Verify SANs on certs match service DNS names |
| `OOMKilled` | Memory limit too low | Increase `resources.limits.memory` in the Deployment |
| ServiceMonitor not scraped | Missing service labels | Verify `service-labels.yaml` patch was applied |
| Ingress 502 errors | Gateway not ready | Check readiness probe, verify gateway pod is running |

### Useful Commands

```bash
# Check all pods
kubectl get pods -n cordum -o wide

# Check events (recent errors)
kubectl get events -n cordum --sort-by=.lastTimestamp | tail -20

# Logs for a specific service
kubectl logs -n cordum deployment/cordum-api-gateway --tail=100

# Check Redis cluster status
kubectl exec -n cordum cordum-redis-0 -- redis-cli --tls \
  --cacert /etc/redis/tls/ca.crt --cert /etc/redis/tls/tls.crt \
  --key /etc/redis/tls/tls.key cluster info

# Check NATS JetStream streams
kubectl exec -n cordum cordum-nats-0 -- nats --tlscacert /etc/nats/tls/ca.crt \
  --tlscert /etc/nats/tls/tls.crt --tlskey /etc/nats/tls/tls.key stream ls

# Check network policies
kubectl get networkpolicy -n cordum

# Force config reload (delete cached config in Redis)
kubectl exec -n cordum deployment/cordum-api-gateway -- \
  redis-cli -h redis -p 6379 DEL cfg:system:default
```

---

## 13. Helm Charts

The `cordum-helm/` directory provides an alternative deployment method using Helm v3.

### Chart Structure

```
cordum-helm/
├── Chart.yaml                          # Chart metadata (v0.1.4)
├── values.yaml                         # Default values
├── README.md                           # Chart documentation
└── templates/
    ├── _helpers.tpl                    # Template helpers
    ├── configmap.yaml                  # Pools, timeouts, safety config
    ├── configmap-nats.yaml             # NATS server configuration
    ├── deployment-control-plane.yaml   # All control plane services
    ├── deployment-dashboard.yaml       # Dashboard deployment
    ├── ingress.yaml                    # Ingress resource
    ├── secret.yaml                     # API key + admin password
    ├── service.yaml                    # Service definitions
    └── serviceaccount.yaml             # ServiceAccount
```

### Key values.yaml Overrides

| Key | Default | Description |
|-----|---------|-------------|
| `global.image.repository` | `ghcr.io/cordum-io/cordum/control-plane` | Base image for control plane services |
| `global.image.tag` | `v0.1.4` | Image tag for all services |
| `secrets.apiKey` | `""` | API key (required) |
| `secrets.adminPassword` | `""` | Admin password for user auth |
| `nats.enabled` | `true` | Deploy bundled NATS |
| `nats.persistence.enabled` | `false` | Enable JetStream persistence |
| `redis.enabled` | `true` | Deploy bundled Redis |
| `redis.persistence.enabled` | `false` | Enable Redis persistence |
| `external.natsUrl` | `""` | Use external NATS (disables bundled) |
| `external.redisUrl` | `""` | Use external Redis (disables bundled) |
| `external.safetyKernelAddr` | `""` | Use external safety kernel |
| `gateway.replicaCount` | `1` | Gateway replicas |
| `gateway.env.apiRateLimitRps` | `50` | API rate limit |
| `gateway.env.userAuthEnabled` | `false` | Enable user/password auth |
| `scheduler.replicaCount` | `1` | Scheduler replicas |
| `ingress.enabled` | `false` | Create Ingress resource |
| `ingress.className` | `""` | Ingress class (nginx, traefik) |
| `ingress.tls` | `[]` | TLS configuration |

### Install

```bash
# Basic install
helm install cordum ./cordum-helm \
  --namespace cordum --create-namespace \
  --set secrets.apiKey="$(openssl rand -hex 32)"

# With persistence and ingress
helm install cordum ./cordum-helm \
  --namespace cordum --create-namespace \
  --set secrets.apiKey="$(openssl rand -hex 32)" \
  --set nats.persistence.enabled=true \
  --set redis.persistence.enabled=true \
  --set ingress.enabled=true \
  --set ingress.className=nginx
```

### Production Values Override

Create a `values-production.yaml` file:

```yaml
# values-production.yaml
global:
  image:
    tag: "v0.1.4"

secrets:
  apiKey: ""  # Set via --set or external secret

# Use external managed services
nats:
  enabled: false
external:
  natsUrl: "nats://nats-cluster.infra:4222"

redis:
  enabled: false
external:
  redisUrl: "rediss://redis-cluster.infra:6379"

# Scale control plane
gateway:
  replicaCount: 2
  env:
    apiRateLimitRps: 200
    apiRateLimitBurst: 400
    userAuthEnabled: true
  resources:
    limits:
      cpu: 2000m
      memory: 1Gi
    requests:
      cpu: 500m
      memory: 256Mi

scheduler:
  replicaCount: 2
  resources:
    limits:
      cpu: 2000m
      memory: 1Gi
    requests:
      cpu: 500m
      memory: 256Mi

safetyKernel:
  replicaCount: 2
  resources:
    limits:
      cpu: 1000m
      memory: 512Mi

workflowEngine:
  replicaCount: 1
  resources:
    limits:
      cpu: 2000m
      memory: 1Gi

# Enable ingress with TLS
ingress:
  enabled: true
  className: nginx
  annotations:
    cert-manager.io/cluster-issuer: letsencrypt-prod
  tls:
    - secretName: cordum-tls
      hosts:
        - cordum.example.com
        - api.cordum.example.com
  api:
    host: api.cordum.example.com
  dashboard:
    host: cordum.example.com
```

Deploy with overrides:

```bash
helm install cordum ./cordum-helm \
  --namespace cordum --create-namespace \
  -f values-production.yaml \
  --set secrets.apiKey="$(openssl rand -hex 32)" \
  --set secrets.adminPassword="$(openssl rand -base64 24)"
```

### Upgrade

```bash
helm upgrade cordum ./cordum-helm \
  --namespace cordum \
  -f values-production.yaml \
  --set global.image.tag="v0.2.0"
```

### Helm vs Kustomize

| Feature | Helm (`cordum-helm/`) | Kustomize (`deploy/k8s/production/`) |
|---------|----------------------|--------------------------------------|
| Bundled NATS/Redis | Toggle with `nats.enabled` / `redis.enabled` | Separate StatefulSets in overlay |
| External services | `external.natsUrl` / `external.redisUrl` | Manual env patch |
| TLS/mTLS | Not built-in (use external) | Full TLS overlay with patches |
| Redis Cluster | Not supported (single instance) | 6-node cluster with init job |
| NATS Cluster | Not supported (single instance) | 3-node StatefulSet |
| Network Policies | Not included | Full ingress + egress policies |
| Monitoring | Not included | ServiceMonitors + PrometheusRules |
| HA (PDB/HPA) | Manual replica count | PDBs + HPAs included |
| Best for | Quick starts, managed infrastructure | Full production with self-hosted infra |

> For production with self-hosted NATS/Redis, the kustomize overlay in `deploy/k8s/production/` is recommended. Use Helm when relying on external managed services (Amazon ElastiCache, Amazon MQ, etc.).

---

## Related Docs

- [production.md](production.md) — Production readiness guide with DR, runbooks, and scaling
- [production-gate.md](production-gate.md) — Automated production gate script
- [configuration.md](configuration.md) — Config files and environment variables
- [configuration-reference.md](configuration-reference.md) — Full config schema reference
- [DOCKER.md](DOCKER.md) — Docker Compose development setup
- [helm.md](helm.md) — Helm chart deployment

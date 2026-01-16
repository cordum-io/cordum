# Production Readiness Checklist

This checklist captures the minimum hardening steps before running Cordum in production.
The sample K8s manifests under `deploy/k8s` are a starter only.

For a production-oriented baseline, use the kustomize overlay under
`deploy/k8s/production/` (stateful NATS/Redis, TLS/mTLS, network policies,
monitoring, backups, and HA defaults).

For Helm-based deployments, start with `cordum-helm/` and apply the same
hardening steps below.

## 1) Persistence + durability

- Run NATS with JetStream persistence (PVCs). Prefer a 3-node NATS cluster for HA.
- Run Redis with persistence + backups (managed Redis, Redis Sentinel, or Redis Cluster).
- Verify pointer retention policies (result/context/lock TTLs) match your compliance needs.

## 2) High availability

- Gateway: run multiple replicas behind a Service + HPA.
- Workflow engine: scale cautiously; it is stateful and should be coordinated.
- Scheduler: run a single active instance unless you have a leader lock strategy.
- Safety kernel: can be replicated if your gRPC load requires it.

## 3) Security baseline

- TLS for all ingress traffic.
- TLS/mTLS for NATS and Redis (or use managed services with encryption).
- NetworkPolicies to restrict lateral traffic (gateway <-> redis/nats/safety).
- Secrets stored in a proper secret manager (KMS, Vault, etc.).
- Rotate API keys and enterprise license material regularly.
  - In the production K8s overlay, TLS secrets are expected:
    - `cordum-nats-server-tls` (server cert + key + CA)
    - `cordum-redis-server-tls`
    - `cordum-client-tls` (client cert + key + CA)
    - `cordum-ingress-tls` (Ingress TLS)

## 4) Observability

- Scrape `/metrics` for gateway (`:9092`) and scheduler (`:9090`).
- Capture workflow engine health (`:9093/health`).
- Centralize logs (structured log collection + retention).
- Optional: add OpenTelemetry tracing.

## 5) Operational limits

- Configure pool timeouts and max retries (`config/timeouts.yaml`).
- Apply policy constraints (max runtime, max retries, artifact sizes).
- Configure rate limits on the gateway (`API_RATE_LIMIT_RPS/BURST`).

## 6) Backup + restore

- Backup Redis (job state, workflows, config, DLQ, pointers).
- Backup JetStream volumes.
- Document a restore drill and run it at least quarterly.
  - The production overlay includes example CronJobs for Redis RDB and NATS stream snapshots.
    Adjust schedules and destinations to your backup system.

## 7) Upgrade strategy

- Use versioned images and a staged rollout (dev -> staging -> prod).
- Validate schema/policy changes in staging before publish.
- Keep backward compatibility for workflow payloads.

## 8) Enterprise gating (if applicable)

- Enforce `CORDUM_LICENSE_REQUIRED=true` for enterprise gateways.
- Keep public and enterprise images in separate registries.
- Audit all API keys and principal roles.

## 9) K8s hardening (recommended)

- Requests/limits for every pod (already in `deploy/k8s/base.yaml`).
- PodDisruptionBudgets for replicated services.
- Non-root security contexts for Cordum services.
- Readiness/liveness probes on every workload.

## 11) Production K8s overlay (recommended)

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

## 10) Runbook checklist

- Smoke test the platform (`tools/scripts/platform_smoke.sh`).
- Verify DLQ + retry flow.
- Verify policy evaluate/simulate/explain endpoints.
- Confirm audit trail (run timeline + approval metadata).

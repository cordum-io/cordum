# Cordum K8s Starter (sample)

Minimal manifests to run the platform control plane in a single namespace. This is a starter, not production-hardened.

## Components
- NATS (bus)
- Redis (ctx/res + JobStore)
- Safety kernel
- Scheduler
- API gateway (HTTP :8081, gRPC :8080, Prom :9092)
- Context engine (generic memory)
- Workflow engine (`:9093/health`)
- Dashboard (HTTP :8080)
- ConfigMaps: pools.yaml, timeouts.yaml, safety.yaml

## Apply
```bash
kubectl create namespace cordum
kubectl apply -n cordum -f deploy/k8s/base.yaml
# Optional (requires an Ingress controller):
kubectl apply -n cordum -f deploy/k8s/ingress.yaml
```

## Production overlay (recommended)

The production overlay uses kustomize to provide stateful NATS/Redis, TLS/mTLS,
network policies, HA defaults, monitoring, and backups:

```bash
kubectl apply -k deploy/k8s/production
```

Required secrets for the overlay:

- `cordum-nats-server-tls` (tls.crt/tls.key/ca.crt)
- `cordum-redis-server-tls` (tls.crt/tls.key/ca.crt)
- `cordum-client-tls` (tls.crt/tls.key/ca.crt)
- `cordum-ingress-tls` (Ingress TLS cert)

The monitoring manifests use ServiceMonitor/PrometheusRule CRDs (Prometheus Operator).
The Redis overlay includes a cluster init Job (`cordum-redis-cluster-init`) that
must complete once after the pods come up.

Enterprise gateway overlay (optional):

```bash
kubectl apply -n cordum -f /path/to/cordum-enterprise/deploy/k8s/enterprise-gateway.yaml
```

## Notes
- Images assume `cordum-<service>` tags; adjust `image:` as needed (e.g., from your registry).
- API key is stored in the `cordum-api-key` Secret (key: `API_KEY`). Default in this starter is `super-secret-key` (change it for real deployments).
- The gateway reads the API key from `CORDUM_SUPER_SECRET_API_TOKEN`, `CORDUM_API_KEY`, or `API_KEY`, wired from the same Secret.
- The dashboard reads the API key from the same Secret.
- Probes are basic HTTP/TCP; adjust for your environment.
- Resource requests/limits are conservative defaults; tune for your load profile.
- If you use an Ingress, `deploy/k8s/ingress.yaml` routes `/api/v1/*` and `/health` to `cordum-api-gateway`, and `/` to `cordum-dashboard`.

For production hardening, see `docs/production.md`.

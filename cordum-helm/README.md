# Cordum Helm Chart

This chart deploys the Cordum control plane (gateway, scheduler, safety kernel,
workflow engine, optional context engine) plus Redis and NATS by default.

## Install (local chart)

```bash
helm install cordum ./cordum-helm -n cordum --create-namespace \
  --set secrets.apiKey=<your-api-key> \
  --set gateway.env.tenantId=default \
  --set dashboard.env.tenantId=default
```

## Install (published chart)

```bash
helm repo add cordum https://charts.cordum.io
helm repo update
helm install cordum cordum/cordum -n cordum --create-namespace \
  --set secrets.apiKey=<your-api-key> \
  --set gateway.env.tenantId=default \
  --set dashboard.env.tenantId=default
```

Note: the chart defaults to the image tags in `values.yaml` (currently `v0.1.4`)
and pulls from GHCR. Override `global.image.tag` and `dashboard.image.tag` if
your registry uses different tags.

## Configuration

Common overrides:

```bash
helm install cordum ./cordum-helm \
  -n cordum --create-namespace \
  --set global.image.tag=v0.1.4 \
  --set secrets.apiKey=<your-api-key> \
  --set gateway.env.tenantId=default \
  --set dashboard.env.tenantId=default \
  --set ingress.enabled=true
```

Use external Redis/NATS:

```bash
helm install cordum ./cordum-helm \
  -n cordum --create-namespace \
  --set nats.enabled=false \
  --set redis.enabled=false \
  --set external.natsUrl=nats://nats.example.com:4222 \
  --set external.redisUrl=redis://redis.example.com:6379
```

Use an external safety kernel:

```bash
helm install cordum ./cordum-helm \
  -n cordum --create-namespace \
  --set safetyKernel.enabled=false \
  --set external.safetyKernelAddr=safety-kernel.example.com:50051
```

## Local dev (kind + local images)

If you are installing from a local clone and do not have published images,
build and load images into kind, then override tags:

```bash
docker compose build

for svc in api-gateway scheduler safety-kernel workflow-engine context-engine; do
  docker tag "cordum-cordum-${svc}:latest" "ghcr.io/cordum-io/cordum/control-plane:dev-${svc}"
done
docker tag cordum-cordum-dashboard:latest ghcr.io/cordum-io/cordum/dashboard:dev

kind load docker-image --name cordum \
  ghcr.io/cordum-io/cordum/control-plane:dev-api-gateway \
  ghcr.io/cordum-io/cordum/control-plane:dev-scheduler \
  ghcr.io/cordum-io/cordum/control-plane:dev-safety-kernel \
  ghcr.io/cordum-io/cordum/control-plane:dev-workflow-engine \
  ghcr.io/cordum-io/cordum/control-plane:dev-context-engine \
  ghcr.io/cordum-io/cordum/dashboard:dev

helm upgrade --install cordum ./cordum-helm -n cordum --create-namespace \
  --set global.image.tag=dev \
  --set dashboard.image.tag=dev
```

## Access (port-forward)

Services are ClusterIP by default. For local access:

```bash
kubectl -n cordum port-forward svc/cordum-api-gateway 8081:8081
kubectl -n cordum port-forward svc/cordum-dashboard 8082:8080
```

Dashboard: `http://localhost:8082`
HTTP requests must include `X-API-Key` and `X-Tenant-ID` (use `gateway.env.tenantId` as the default tenant).

## Notes

- Gateway HTTP is exposed on the `api-gateway` service (port 8081).
- Dashboard is exposed on the `dashboard` service (port 8080).
- Update `config.safety`, `config.pools`, and `config.timeouts` in values.yaml
  to control policy and routing defaults.

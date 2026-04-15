---
sidebar_position: 1
title: Deployment
slug: /operations/deployment
---

# Deployment

## Docker Compose (Development)

```bash
git clone https://github.com/cordum-io/cordum.git
cd cordum
make dev-up
```

## Kubernetes (Production)

Cordum ships with Kustomize-based Kubernetes manifests in `deploy/k8s/`.

```bash
# Base deployment
kubectl apply -k deploy/k8s/base/

# Production overlay (with replicas, resource limits, TLS)
kubectl apply -k deploy/k8s/production/
```

## Environment Variables

All services share common configuration via environment variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `REDIS_URL` | `redis://localhost:6379` | Redis connection URL |
| `NATS_URL` | `nats://localhost:4222` | NATS connection URL |
| `TENANT_ID` | `default` | Default tenant ID |
| `OTEL_ENABLED` | `false` | Enable OpenTelemetry tracing |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | OTLP collector endpoint |

See [Configuration Reference](/operations/configuration) for the full list.

# Cordum Helm Chart

This chart deploys the Cordum control plane (gateway, scheduler, safety kernel,
workflow engine, optional context engine) plus Redis and NATS by default.

## Install (local chart)

```bash
helm install cordum ./cordum-helm -n cordum --create-namespace
```

## Install (published chart)

```bash
helm repo add cordum https://charts.cordum.io
helm repo update
helm install cordum cordum/cordum -n cordum --create-namespace
```

## Configuration

Common overrides:

```bash
helm install cordum ./cordum-helm \
  -n cordum --create-namespace \
  --set global.image.tag=v0.1.1 \
  --set secrets.apiKey=change-me \
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

## Notes

- Gateway HTTP is exposed on the `api-gateway` service (port 8081).
- Dashboard is exposed on the `dashboard` service (port 8080).
- Update `config.safety`, `config.pools`, and `config.timeouts` in values.yaml
  to control policy and routing defaults.

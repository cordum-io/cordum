# Helm Install

Cordum ships with a Helm chart under `cordum-helm/`.

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

## Common overrides

```bash
helm install cordum ./cordum-helm \
  -n cordum --create-namespace \
  --set global.image.tag=v0.1.1 \
  --set secrets.apiKey=change-me
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

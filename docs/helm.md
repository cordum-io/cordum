# Helm Install

Cordum ships with a Helm chart under `cordum-helm/`.

## Prerequisites

- Kubernetes cluster
- Helm 3
- kubectl

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

Note: the chart defaults to the image tags in `values.yaml` (currently `v0.1.4`)
and pulls from GHCR. If those tags are not published in your registry, override
`global.image.tag` and `dashboard.image.tag` (or point to your own registry).

## Local dev (kind + local images)

The chart expects images like `ghcr.io/cordum-io/cordum/control-plane:<tag>-api-gateway`.
If you are installing from a local clone without published images, build and
load images into your cluster and override tags:

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

For non-kind clusters, push to a registry you control and set
`global.image.repository`, `global.image.tag`, and `dashboard.image.*`
accordingly (plus `imagePullSecrets` if needed).

## Access (port-forward)

The chart exposes services as ClusterIP by default. For local access:

```bash
kubectl -n cordum port-forward svc/cordum-api-gateway 8081:8081
kubectl -n cordum port-forward svc/cordum-dashboard 8082:8080
```

Dashboard: `http://localhost:8082`

The API key is required. Set it with:

```bash
helm upgrade --install cordum ./cordum-helm \
  -n cordum --create-namespace \
  --set secrets.apiKey=<your-api-key>
```

To embed the API key in the dashboard config (not recommended for shared environments):

```bash
helm upgrade --install cordum ./cordum-helm \
  -n cordum --create-namespace \
  --set secrets.apiKey=<your-api-key> \
  --set dashboard.env.embedApiKey=true
```

## Common overrides

```bash
helm install cordum ./cordum-helm \
  -n cordum --create-namespace \
  --set global.image.tag=v0.1.4 \
  --set secrets.apiKey=<your-api-key>
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

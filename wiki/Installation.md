# Installation

Cordum supports a fast local quickstart and production-grade Kubernetes
installations. All options require an API key and tenant ID.

## Prerequisites

- Docker + Docker Compose (local install)
- curl + jq (smoke tests)
- Go (optional, for `cordumctl`)
- Helm + kubectl (Kubernetes)

## Required environment variables

```bash
export CORDUM_API_KEY="$(openssl rand -hex 32)"
export CORDUM_TENANT_ID=default
```

Docker Compose loads `.env` automatically; the helper scripts read environment
variables from your shell, so keep the `export` lines when running scripts.

## Option A: Quickstart (recommended)

```bash
./tools/scripts/quickstart.sh
```

This builds the stack, starts services, and runs a workflow smoke test.

## Option B: Install script

```bash
CORDUM_API_KEY="$(openssl rand -hex 32)" \
CORDUM_TENANT_ID=default \
curl -fsSL https://raw.githubusercontent.com/cordum-io/cordum/main/tools/scripts/install.sh | bash
```

Local installer (from a clone):

```bash
CORDUM_API_KEY="$(openssl rand -hex 32)" CORDUM_TENANT_ID=default ./tools/scripts/install.sh
```

## Option C: Docker Compose (manual)

```bash
export CORDUM_API_KEY="$(openssl rand -hex 32)"
export CORDUM_TENANT_ID=default

docker compose build

docker compose up -d
```

## Option D: Kubernetes (Helm)

```bash
helm repo add cordum https://charts.cordum.io
helm repo update
helm install cordum cordum/cordum -n cordum --create-namespace \
  --set secrets.apiKey=<your-api-key> \
  --set gateway.env.tenantId=default \
  --set dashboard.env.tenantId=default
```

Port-forward to access locally:

```bash
kubectl -n cordum port-forward svc/cordum-api-gateway 8081:8081
kubectl -n cordum port-forward svc/cordum-dashboard 8082:8080
```

## Verify

Smoke test:

```bash
CORDUM_API_KEY=${CORDUM_API_KEY:?set CORDUM_API_KEY} \
CORDUM_TENANT_ID=${CORDUM_TENANT_ID:-default} \
bash ./tools/scripts/platform_smoke.sh
```

Status endpoint:

```bash
curl -sS http://localhost:8081/api/v1/status \
  -H "X-API-Key: ${CORDUM_API_KEY}" \
  -H "X-Tenant-ID: ${CORDUM_TENANT_ID}" | jq
```

## Next steps

- [Quickstart](Quickstart) walkthrough
- [Security](Security) production hardening
- [Configuration](Configuration) for env + config files

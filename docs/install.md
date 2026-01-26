# Installation Guide

This guide covers the supported install paths and the minimum required
configuration for a secure Cordum deployment.

## Prerequisites

- Docker + Docker Compose (local quickstart)
- curl + jq (for smoke tests)
- Go (optional, for `cordumctl`)
- Helm + kubectl (Kubernetes installs)

## Required environment variables

Cordum requires an API key and a tenant for all API calls.

```bash
export CORDUM_API_KEY="$(openssl rand -hex 32)"
export CORDUM_TENANT_ID=default
```

Compose and the quickstart scripts fail fast if `CORDUM_API_KEY` is missing.

## Option A: One-command quickstart (recommended)

```bash
./tools/scripts/quickstart.sh
```

This builds the local stack, starts the services, and runs a smoke test.

## Option B: Install script (local or remote)

```bash
# Remote installer
CORDUM_API_KEY="$(openssl rand -hex 32)" \
CORDUM_TENANT_ID=default \
curl -fsSL https://raw.githubusercontent.com/cordum-io/cordum/main/tools/scripts/install.sh | bash

# Local installer
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

Port-forward for local access:

```bash
kubectl -n cordum port-forward svc/cordum-api-gateway 8081:8081
kubectl -n cordum port-forward svc/cordum-dashboard 8082:8080
```

## Verify the install

Smoke test:

```bash
CORDUM_API_KEY=${CORDUM_API_KEY:?set CORDUM_API_KEY} \
CORDUM_TENANT_ID=${CORDUM_TENANT_ID:-default} \
./tools/scripts/platform_smoke.sh
```

Status endpoint:

```bash
curl -sS http://localhost:8081/api/v1/status \
  -H "X-API-Key: ${CORDUM_API_KEY}" \
  -H "X-Tenant-ID: ${CORDUM_TENANT_ID}" | jq
```

## Next steps

- Run the guardrails demo: `./tools/scripts/demo_guardrails_run.sh`
- Install the hello pack: `examples/hello-pack`
- Open the dashboard: `http://localhost:8082`
- Review production hardening: `docs/production.md`

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

Retrieve the API key from the Kubernetes secret:

```bash
kubectl get secret cordum-api-key -n cordum -o jsonpath='{.data.API_KEY}' | base64 -d
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

## Config auto-bootstrap

On first startup, the gateway and scheduler automatically create a `system/default`
config document with minimal safety and rate-limit defaults. No manual config seeding
is required.

To customize the default config after startup:

```bash
curl -X POST http://localhost:8081/api/v1/config \
  -H "X-API-Key: ${CORDUM_API_KEY}" \
  -H "X-Tenant-ID: default" \
  -H "Content-Type: application/json" \
  -d '{"safetyStance": "strict", "rateLimitPerKey": 300}'
```

For Helm deployments, the chart includes a post-install Job that verifies the config
endpoint is reachable. Disable it with `--set configBootstrap.enabled=false`.

If the dashboard settings page shows empty state, verify `GET /api/v1/config` returns
`200` with a JSON object. If it returns `404`, restart the gateway to trigger
auto-bootstrap, or POST a config document manually.

## Next steps

- [Quickstart](Quickstart) walkthrough
- [Security](Security) production hardening
- [Configuration](Configuration) for env + config files

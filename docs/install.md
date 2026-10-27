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
Docker Compose loads `.env` automatically; the helper scripts read environment
variables from your shell, so keep the `export` lines when running scripts.

## Option A: One-command quickstart (recommended)

```bash
./tools/scripts/quickstart.sh
```

This builds the local stack, starts the services, and runs a smoke test.

## Option B: Install script (local or remote)

```bash
# Remote installer
curl -fsSL https://raw.githubusercontent.com/cordum-io/cordum/main/tools/scripts/install.sh | bash

# Local installer
./tools/scripts/install.sh
```

The installer clones the repo into `DEST_DIR` (default `./cordum`, reused if it
is already a clone) and runs `tools/scripts/quickstart.sh --skip-smoke`, which
writes `.env` with a generated `CORDUM_API_KEY` and `REDIS_PASSWORD`, generates
the dev certificates in `./certs` (with `cordumctl` or `go run`), and starts
the HTTPS stack. An exported
`CORDUM_API_KEY` or `REDIS_PASSWORD` is used instead of a generated one.

## Option C: Docker Compose (manual)

Compose needs `CORDUM_API_KEY`, `REDIS_PASSWORD` and the TLS certificates in
`./certs/` that every service mounts; `quickstart.sh` (Option A) creates all
three. By hand:

```bash
export CORDUM_API_KEY="$(openssl rand -hex 32)"
export REDIS_PASSWORD="$(openssl rand -hex 24)"
export CORDUM_TENANT_ID=default

# TLS certificates in ./certs/ (needs Go; or run: cordumctl generate-certs)
go run ./cmd/cordumctl generate-certs

docker compose build

docker compose up -d
```

Compose reads both values from the shell or from `.env`; put them in `.env` so
later `docker compose` commands in a new shell still work.

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
bash ./tools/scripts/platform_smoke.sh
```

Status endpoint:

```bash
curl -sS --cacert ./certs/ca/ca.crt https://localhost:8081/api/v1/status \
  -H "X-API-Key: ${CORDUM_API_KEY}" \
  -H "X-Tenant-ID: ${CORDUM_TENANT_ID}" | jq
```

The Compose installs serve the gateway over HTTPS with the development CA in
`./certs/ca/ca.crt`. The Helm chart leaves gateway TLS off unless
`global.tls.enabled=true`, so a default Helm install answers on
`http://localhost:8081` (plain HTTP only when the gateway runs without
`GATEWAY_HTTP_TLS_CERT`, outside production); drop `--cacert` there.

## Next steps

- Run the guardrails demo: `./tools/scripts/demo_guardrails_run.sh`
- Install the hello pack: `examples/hello-pack`
- Open the dashboard: `http://localhost:8082`
- Review production hardening: `docs/production.md`

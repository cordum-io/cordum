---
sidebar_position: 3
title: "Getting Started"
slug: /getting-started/guide
---

# Getting Started

This guide gets a local Cordum stack running with the default Docker compose
setup.

## Prerequisites

- Docker + Docker Compose (4GB+ RAM allocated to Docker)
- curl
- Go 1.24+ (for cert generation and local builds)
- Node 18+ (only if developing the dashboard locally)

Optional: jq (for pretty-printing JSON responses)

## Fastest Path (recommended)

```bash
./tools/scripts/quickstart.sh
```

This single command:
1. Creates `.env` from `.env.example` with auto-generated API key and Redis password
2. Generates TLS certificates
3. Builds and starts all 7 services + NATS + Redis
4. Waits for health readiness
5. Runs smoke tests

**Default login:** `admin` / `admin123` (change via `CORDUM_ADMIN_PASSWORD` in `.env`)

## Manual Setup (alternative)

If you prefer step-by-step control:

```bash
# 1. Create environment config
cp .env.example .env
# Edit .env: set CORDUM_API_KEY (generate with: openssl rand -hex 32)

# 2. Start the stack
go run ./cmd/cordumctl up
# Or: make build SERVICE=cordumctl && ./bin/cordumctl up

# 3. Open dashboard
open http://localhost:8082
# Login: admin / admin123
```

## Platform Notes

| Platform | Notes |
|----------|-------|
| **Windows/MSYS** | Use Unix paths. Prefix docker exec with `MSYS_NO_PATHCONV=1`. Use `-count=3` instead of `-race` for Go tests. |
| **macOS** | Ensure Docker Desktop has 4GB+ RAM (Settings → Resources) |
| **Linux** | Add user to docker group: `sudo usermod -aG docker $USER` |

`cordumctl up` sets `COMPOSE_HTTP_TIMEOUT` and `DOCKER_CLIENT_TIMEOUT` to `1800`
seconds if they are not already set. Override them in your shell if needed.

**TLS is enabled by default.** `cordumctl up` and `cordumctl dev` auto-generate
self-signed TLS certificates into `./certs/` on first run. All services
communicate over TLS with proper certificate verification — no `--insecure`
flags needed.

The API gateway listens on `https://localhost:8081` by default. Use `--cacert`
to trust the auto-generated CA:

```bash
curl --cacert ./certs/ca/ca.crt \
  -H "X-API-Key: $CORDUM_API_KEY" -H "X-Tenant-ID: default" \
  https://localhost:8081/api/v1/status
```

For full TLS details, see the TLS Setup Guide.

## Enterprise gateway

Enterprise setup and licensing live in the enterprise repo. See `docs/enterprise.md`
for details.

## Run a workflow smoke test

```bash
CORDUM_API_KEY=${CORDUM_API_KEY:?set CORDUM_API_KEY} bash ./tools/scripts/platform_smoke.sh
```

Expected output:
- workflow created
- run started
- workflow gate approval granted
- run completes
- workflow + run deleted

## Use the CLI

```bash
make build SERVICE=cordumctl
export PATH="$PWD/bin:$PATH"
./tools/scripts/cordumctl_smoke.sh
```

## Run the hello pack (optional)

This demo installs a tiny pack and a Go worker that echoes workflow input.

```bash
# In one terminal, start the worker
cd examples/hello-worker-go
go run .

# In another terminal, install the pack
cd ../../
./bin/cordumctl pack install ./examples/hello-pack

# Trigger a run
curl -sS --cacert ./certs/ca/ca.crt \
  -X POST https://localhost:8081/api/v1/workflows/hello-pack.echo/runs \
  -H "X-API-Key: ${CORDUM_API_KEY:?set CORDUM_API_KEY}" \
  -H "X-Tenant-ID: ${CORDUM_TENANT_ID:-default}" \
  -H "Content-Type: application/json" \
  -d '{"message":"hello from pack","author":"demo"}'
```

Other runtime examples:
- `examples/python-worker`
- `examples/node-worker`
- `examples/langchain-guard` — Secure a LangGraph agent with Cordum

## Open the dashboard (optional)

The dashboard is automatically configured with your local API key when using
the default Compose stack. For shared environments, remove
`CORDUM_DASHBOARD_EMBED_API_KEY` from compose to require manual auth.

```text
http://localhost:8082
```

## Reset local state

```bash
docker compose exec redis redis-cli --tls --cacert /etc/cordum/tls/ca/ca.crt -a "${REDIS_PASSWORD:-cordum-dev}" FLUSHALL
```

To wipe JetStream state too:

```bash
docker compose down -v
```

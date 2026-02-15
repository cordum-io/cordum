# Getting Started

This guide gets a local Cordum stack running with the default Docker compose
setup.

## Prerequisites

- Docker + Docker Compose
- curl
- jq
- openssl (optional, for generating an API key)

## Set an API key (required)

Cordum requires an API key for all API requests. Compose and the quickstart
scripts will fail fast if `CORDUM_API_KEY` is missing.

```bash
cp .env.example .env
# generate a key (requires openssl)
export CORDUM_API_KEY="$(openssl rand -hex 32)"
# set a tenant for requests
export CORDUM_TENANT_ID=default
```

Docker Compose automatically loads `.env`. The helper scripts read environment
variables from your shell, so keep the `export` lines when running scripts.

## Start the stack

One command (requires Go):

```bash
go run ./cmd/cordumctl up
```

Or build the CLI once and run it:

```bash
make build SERVICE=cordumctl
./bin/cordumctl up
```

Or the fastest one-liner:

```bash
./tools/scripts/quickstart.sh
```

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

For full TLS details, see [TLS Setup Guide](guides/tls-setup.md).

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
- approval step approved
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
- `examples/langchain-guard` — [Secure a LangGraph agent with Cordum](tutorials/langchain-guard.md)

## Open the dashboard (optional)

The dashboard is automatically configured with your local API key when using
the default Compose stack. For shared environments, remove
`CORDUM_DASHBOARD_EMBED_API_KEY` from compose to require manual auth.

```text
http://localhost:8082
```

## Reset local state

```bash
docker compose exec redis redis-cli FLUSHALL
```

To wipe JetStream state too:

```bash
docker compose down -v
```

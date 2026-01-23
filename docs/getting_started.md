# Getting Started

This guide gets a local Cordum stack running with the default Docker compose
setup.

## Prerequisites

- Docker + Docker Compose
- curl
- jq

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

```bash
docker compose build
docker compose up -d
```

The API gateway listens on `http://localhost:8081` by default.

## Enterprise gateway

Enterprise setup and licensing live in the enterprise repo. See `docs/enterprise.md`
for details.

## Set an API key

Compose uses a default API key of `[REDACTED]`. To override:

```bash
cp .env.example .env
# edit CORDUM_API_KEY
```

## Run a workflow smoke test

```bash
CORDUM_API_KEY=${CORDUM_API_KEY:-[REDACTED]} ./tools/scripts/platform_smoke.sh
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
curl -sS -X POST http://localhost:8081/api/v1/workflows/hello-pack.echo/runs \
  -H "X-API-Key: ${CORDUM_API_KEY:-[REDACTED]}" \
  -H "Content-Type: application/json" \
  -d '{"message":"hello from pack","author":"demo"}'
```

Other runtime examples:
- `examples/python-worker`
- `examples/node-worker`

## Open the dashboard (optional)

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

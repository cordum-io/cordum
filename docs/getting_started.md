# Getting Started

This guide gets a local Cordum stack running with the default Docker compose
setup.

## Prerequisites

- Docker + Docker Compose
- curl
- jq

## Start the stack

One command (recommended):

```bash
./cmd/cordumctl/cordumctl up
```

```bash
docker compose build
docker compose up -d
```

The API gateway listens on `http://localhost:8081` by default.

## Set an API key

Compose uses a default API key of `super-secret-key`. To override:

```bash
cp .env.example .env
# edit CORDUM_API_KEY
```

## Run a workflow smoke test

```bash
./tools/scripts/platform_smoke.sh
```

Expected output:
- workflow created
- run started
- approval step approved
- run completes
- workflow + run deleted

## Use the CLI

```bash
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
./cmd/cordumctl/cordumctl pack install ./examples/hello-pack

# Trigger a run
curl -sS -X POST http://localhost:8081/api/v1/workflows/hello-pack.echo/runs \
  -H "X-API-Key: ${CORDUM_API_KEY:-super-secret-key}" \
  -H "Content-Type: application/json" \
  -d '{"message":"hello from pack","author":"demo"}'
```

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

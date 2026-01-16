# Hello Worker (Go)

Minimal Go worker that consumes `job.hello-pack.echo`, reads step input from
Redis, and writes an echo result back to Redis.

## Run locally

```bash
export NATS_URL=${NATS_URL:-nats://localhost:4222}
export REDIS_URL=${REDIS_URL:-redis://localhost:6379}
go run .
```

## Run in Docker

```bash
docker build -t cordum-hello-worker-go .
docker run --rm \
  -e NATS_URL=nats://host.docker.internal:4222 \
  -e REDIS_URL=redis://host.docker.internal:6379 \
  cordum-hello-worker-go
```

When running inside the Cordum compose network, use service names:
`NATS_URL=nats://nats:4222`, `REDIS_URL=redis://redis:6379`.

## Try it

Install the hello pack and trigger a run:

```bash
./cmd/cordumctl/cordumctl pack install ./examples/hello-pack
curl -sS -X POST http://localhost:8081/api/v1/workflows/hello-pack.echo/runs \
  -H "X-API-Key: ${CORDUM_API_KEY:-super-secret-key}" \
  -H "Content-Type: application/json" \
  -d '{"message":"hello from pack","author":"demo"}' | jq
```

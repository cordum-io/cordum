# Hello Worker (Go)

Minimal Go worker that consumes `job.hello-pack.echo`, reads step input from
Redis, and writes an echo result back to Redis.

## Run locally

| Variable | Dev (plaintext) | Prod-like (TLS) |
| --- | --- | --- |
| `NATS_URL` | `nats://localhost:4222` | `tls://localhost:4222` |
| `REDIS_URL` | `redis://:cordum-dev@localhost:6379/0` | `rediss://:${REDIS_PASSWORD}@localhost:6379/0` |
| `NATS_TLS_CA` | unset | `./certs/ca/ca.crt` |
| `NATS_TLS_CERT` | unset | `./certs/client/tls.crt` |
| `NATS_TLS_KEY` | unset | `./certs/client/tls.key` |
| `REDIS_TLS_CA` | unset | `./certs/ca/ca.crt` |
| `REDIS_TLS_CERT` | unset | `./certs/client/tls.crt` |
| `REDIS_TLS_KEY` | unset | `./certs/client/tls.key` |

```bash
export NATS_URL=${NATS_URL:-nats://localhost:4222}
export REDIS_URL=${REDIS_URL:-redis://:cordum-dev@localhost:6379/0}
go run .
```

## Run in Docker

```bash
docker build -t cordum-hello-worker-go .
docker run --rm \
  -e NATS_URL=nats://host.docker.internal:4222 \
  -e REDIS_URL=redis://:cordum-dev@host.docker.internal:6379/0 \
  cordum-hello-worker-go
```

When running against the prod-like Cordum compose stack, keep the TLS posture
and use the same environment names as core services:

```bash
export NATS_URL=tls://localhost:4222
export NATS_TLS_CA=./certs/ca/ca.crt
export NATS_TLS_CERT=./certs/client/tls.crt
export NATS_TLS_KEY=./certs/client/tls.key

export REDIS_URL=rediss://:${REDIS_PASSWORD:-cordum-dev}@localhost:6379/0
export REDIS_TLS_CA=./certs/ca/ca.crt
export REDIS_TLS_CERT=./certs/client/tls.crt
export REDIS_TLS_KEY=./certs/client/tls.key
go run .
```

The worker logs only URL schemes (`nats`, `tls`, `redis`, `rediss`) at startup,
not hosts, usernames, passwords, or tokens.

## Try it

Install the hello pack and trigger a run:

```bash
./cmd/cordumctl/cordumctl pack install ./examples/hello-worker-go/pack
curl -sS -X POST http://localhost:8081/api/v1/jobs \
  -H "X-API-Key: ${CORDUM_API_KEY:?set CORDUM_API_KEY}" \
  -H "X-Tenant-ID: ${CORDUM_TENANT_ID:-default}" \
  -H "Content-Type: application/json" \
  -d '{"topic":"job.hello-pack.echo","prompt":"hello from pack","context":{"message":"hello from pack","author":"demo"}}' | jq
```

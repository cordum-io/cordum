# Guardrails Demo Worker

Consumes demo topics and writes results back to Redis.

## Run locally

```bash
export NATS_URL=${NATS_URL:-nats://localhost:4222}
export REDIS_URL=${REDIS_URL:-redis://localhost:6379}
go run .
```

# Guardrails Demo Worker

Consumes demo topics and writes results back to Redis.

## Run locally

The default Cordum stack runs NATS and Redis TLS-only, so point the worker at
the generated certificates in the checkout's `certs/` (paths below are
relative to this directory):

```bash
export NATS_URL=${NATS_URL:-tls://localhost:4222}
export NATS_TLS_CA=../../../certs/ca/ca.crt
export NATS_TLS_CERT=../../../certs/client/tls.crt
export NATS_TLS_KEY=../../../certs/client/tls.key
export REDIS_URL=${REDIS_URL:-rediss://:${REDIS_PASSWORD}@localhost:6379}
export REDIS_TLS_CA=../../../certs/ca/ca.crt
go run .
```

Against a NATS and Redis that accept plaintext, `nats://` and `redis://` URLs
work without the `*_TLS_*` variables.

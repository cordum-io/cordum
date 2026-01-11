# Configuration

Cordum uses a mix of config files (mounted into containers) and environment
variables.

## Config files

Compose mounts these files from `config/`:

- `config/pools.yaml` - topic -> pool routing
- `config/timeouts.yaml` - per-topic and per-workflow timeouts
- `config/safety.yaml` - safety kernel policy

## Core environment variables

Shared across services:

- `NATS_URL` (default `nats://nats:4222`)
- `REDIS_URL` (default `redis://redis:6379`)
- `NATS_USE_JETSTREAM` (`0|1`)
- `POOL_CONFIG_PATH`, `TIMEOUT_CONFIG_PATH`
- `SAFETY_KERNEL_ADDR`

## Gateway

- `GATEWAY_GRPC_ADDR`, `GATEWAY_HTTP_ADDR`, `GATEWAY_METRICS_ADDR`
- `API_RATE_LIMIT_RPS`, `API_RATE_LIMIT_BURST`
- `TENANT_ID` (single-tenant default)
- API keys: `CORDUM_SUPER_SECRET_API_TOKEN`, `CORDUM_API_KEY`, or `API_KEY`
- CORS: `CORDUM_ALLOWED_ORIGINS`, `CORDUM_CORS_ALLOW_ORIGINS`, `CORS_ALLOW_ORIGINS`
- TLS: `GRPC_TLS_CERT`, `GRPC_TLS_KEY`

## Scheduler

- `JOB_META_TTL` / `JOB_META_TTL_SECONDS`
- `WORKER_SNAPSHOT_INTERVAL`
- `NATS_JS_ACK_WAIT`, `NATS_JS_MAX_AGE`

## Workflow engine

- `WORKFLOW_ENGINE_HTTP_ADDR`
- `WORKFLOW_ENGINE_SCAN_INTERVAL`
- `WORKFLOW_ENGINE_RUN_SCAN_LIMIT`

## Safety kernel

- `SAFETY_KERNEL_ADDR`, `SAFETY_POLICY_PATH`
- TLS server: `SAFETY_KERNEL_TLS_CERT`, `SAFETY_KERNEL_TLS_KEY`
- TLS client: `SAFETY_KERNEL_TLS_CA`, `SAFETY_KERNEL_INSECURE`

For full details, see `docs/DOCKER.md`.

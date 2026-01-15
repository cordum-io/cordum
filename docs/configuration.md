# Configuration

Cordum uses a mix of config files (mounted into containers) and environment
variables.

## Config files

Compose mounts these files from `config/`:

- `config/pools.yaml` - topic -> pool routing
- `config/timeouts.yaml` - per-topic and per-workflow timeouts
- `config/safety.yaml` - safety kernel policy
- `config/system.yaml` - system config template for the config service (budgets, rate limits, observability, alerting)

## Core environment variables

Shared across services:

- `NATS_URL` (default `nats://nats:4222`)
- `REDIS_URL` (default `redis://redis:6379`)
- `NATS_USE_JETSTREAM` (`0|1`)
- `POOL_CONFIG_PATH`, `TIMEOUT_CONFIG_PATH`
- `SAFETY_KERNEL_ADDR`
- NATS TLS: `NATS_TLS_CA`, `NATS_TLS_CERT`, `NATS_TLS_KEY`, `NATS_TLS_INSECURE`, `NATS_TLS_SERVER_NAME`
- Redis TLS: `REDIS_TLS_CA`, `REDIS_TLS_CERT`, `REDIS_TLS_KEY`, `REDIS_TLS_INSECURE`, `REDIS_TLS_SERVER_NAME`
- Redis clustering: `REDIS_CLUSTER_ADDRESSES` (comma-separated host:port seeds)

## Gateway

- `GATEWAY_GRPC_ADDR`, `GATEWAY_HTTP_ADDR`, `GATEWAY_METRICS_ADDR`
- `API_RATE_LIMIT_RPS`, `API_RATE_LIMIT_BURST`
- `TENANT_ID` (single-tenant default)
- API keys: `CORDUM_SUPER_SECRET_API_TOKEN`, `CORDUM_API_KEY`, or `API_KEY`
- CORS: `CORDUM_ALLOWED_ORIGINS`, `CORDUM_CORS_ALLOW_ORIGINS`, `CORS_ALLOW_ORIGINS`
- TLS: `GRPC_TLS_CERT`, `GRPC_TLS_KEY`
- Pack catalog defaults: `CORDUM_PACK_CATALOG_URL`, `CORDUM_PACK_CATALOG_ID`,
  `CORDUM_PACK_CATALOG_TITLE`, `CORDUM_PACK_CATALOG_DEFAULT_DISABLED=1`

## Scheduler

- `JOB_META_TTL` / `JOB_META_TTL_SECONDS`
- `WORKER_SNAPSHOT_INTERVAL`
- `NATS_JS_ACK_WAIT`, `NATS_JS_MAX_AGE`
- `NATS_JS_REPLICAS` (JetStream stream replication factor)

## Workflow engine

- `WORKFLOW_ENGINE_HTTP_ADDR`
- `WORKFLOW_ENGINE_SCAN_INTERVAL`
- `WORKFLOW_ENGINE_RUN_SCAN_LIMIT`

## Safety kernel

- `SAFETY_KERNEL_ADDR`, `SAFETY_POLICY_PATH`
- TLS server: `SAFETY_KERNEL_TLS_CERT`, `SAFETY_KERNEL_TLS_KEY`
- TLS client: `SAFETY_KERNEL_TLS_CA`, `SAFETY_KERNEL_INSECURE`
- Decision cache: `SAFETY_DECISION_CACHE_TTL` (e.g. `5s`, `250ms`)

For full details, see `docs/DOCKER.md`.

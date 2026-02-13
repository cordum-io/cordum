# Configuration

> For a comprehensive field-by-field reference of all config schemas, the config overlay system, and the complete environment variables master table, see [configuration-reference.md](configuration-reference.md).

Cordum uses a mix of config files (mounted into containers) and environment
variables.

## Config files

Compose mounts these files from `config/`:

- `config/pools.yaml` - topic -> pool routing
- `config/timeouts.yaml` - per-topic and per-workflow timeouts
- `config/safety.yaml` - safety kernel policy
- `config/nats.conf` - NATS server config (JetStream `sync_interval`)

`config/system.yaml` is a sample payload for the config service (budgets, rate limits, observability, alerting). It is not mounted by default; use `POST /api/v1/config` to store it.

The control plane validates pool/timeout/safety files against embedded JSON
schemas (see `core/infra/config/schema/`). Invalid configs return errors and,
for timeouts, fall back to defaults.

## Core environment variables

Shared across services:

- `CORDUM_ENV` (`production` enables strict security defaults)
- `CORDUM_PRODUCTION` (`true` enables strict security defaults)
- `CORDUM_TLS_MIN_VERSION` (`1.2` or `1.3`, default `1.3` in production)
- `CORDUM_LOG_FORMAT` (`json` or `text`, default `text`)
- `CORDUM_GRPC_REFLECTION` (set to `1` to enable gRPC reflection, dev only)
- `NATS_URL` (default `nats://nats:4222`)
- `REDIS_URL` (compose default `redis://:${REDIS_PASSWORD:-cordum-dev}@redis:6379`)
- `NATS_USE_JETSTREAM` (`0|1`)
- `POOL_CONFIG_PATH`, `TIMEOUT_CONFIG_PATH`
- `SAFETY_KERNEL_ADDR`
- NATS TLS: `NATS_TLS_CA`, `NATS_TLS_CERT`, `NATS_TLS_KEY`, `NATS_TLS_INSECURE`, `NATS_TLS_SERVER_NAME`
- Redis TLS: `REDIS_TLS_CA`, `REDIS_TLS_CERT`, `REDIS_TLS_KEY`, `REDIS_TLS_INSECURE`, `REDIS_TLS_SERVER_NAME`
- Redis clustering: `REDIS_CLUSTER_ADDRESSES` (comma-separated host:port seeds)

## Gateway

- `GATEWAY_GRPC_ADDR`, `GATEWAY_HTTP_ADDR`, `GATEWAY_METRICS_ADDR`
- `GATEWAY_METRICS_PUBLIC` (set to `1` to allow non-loopback metrics bind in production)
- `API_RATE_LIMIT_RPS`, `API_RATE_LIMIT_BURST` (applied per tenant; falls back to client IP when tenant is missing)
- `TENANT_ID` (single-tenant default)
- API keys: `CORDUM_API_KEY`, `API_KEY`, or `CORDUM_API_KEYS` (comma-separated or JSON)
- API key file: `CORDUM_API_KEYS_PATH` (same format as `CORDUM_API_KEYS`, reloads on change)
- Allow anonymous auth (local/dev only): `CORDUM_ALLOW_INSECURE_NO_AUTH=1`
- Header principal: `CORDUM_ALLOW_HEADER_PRINCIPAL=true` (disabled by default in production)
- CORS: `CORDUM_ALLOWED_ORIGINS`, `CORDUM_CORS_ALLOW_ORIGINS`, `CORS_ALLOW_ORIGINS`
- HTTP TLS: `GATEWAY_HTTP_TLS_CERT`, `GATEWAY_HTTP_TLS_KEY`
- gRPC TLS: `GRPC_TLS_CERT`, `GRPC_TLS_KEY`
- Artifacts: `ARTIFACT_MAX_BYTES` (max upload/download size)
- JWT auth: `CORDUM_JWT_HMAC_SECRET`, `CORDUM_JWT_PUBLIC_KEY`, `CORDUM_JWT_PUBLIC_KEY_PATH`,
  `CORDUM_JWT_ISSUER`, `CORDUM_JWT_AUDIENCE`, `CORDUM_JWT_DEFAULT_ROLE`,
  `CORDUM_JWT_CLOCK_SKEW`, `CORDUM_JWT_REQUIRED`
- Pack catalog defaults: `CORDUM_PACK_CATALOG_URL`, `CORDUM_PACK_CATALOG_ID`,
  `CORDUM_PACK_CATALOG_TITLE`, `CORDUM_PACK_CATALOG_DEFAULT_DISABLED=1`
- Marketplace fetch: `CORDUM_MARKETPLACE_ALLOW_HTTP=1`, `CORDUM_MARKETPLACE_HTTP_TIMEOUT` (e.g. `15s`)

### User authentication

The gateway supports user/password authentication in addition to API key authentication:

- `CORDUM_USER_AUTH_ENABLED=true` - Enable user/password authentication (stores users in Redis)
- `CORDUM_ADMIN_USERNAME` - Default admin username (default: `admin`)
- `CORDUM_ADMIN_PASSWORD` - Default admin password (creates admin user on first startup if set)
- `CORDUM_ADMIN_EMAIL` - Optional admin email

When user auth is enabled, the `/api/v1/auth/login` endpoint accepts both:
1. User credentials (username/email + password)
2. API keys (for programmatic access via scripts/CI)

User management endpoints (admin only):
- `POST /api/v1/users` - Create a new user
- `POST /api/v1/auth/password` - Change password (authenticated)

## Context engine

- `CONTEXT_ENGINE_ADDR`
- TLS server: `CONTEXT_ENGINE_TLS_CERT`, `CONTEXT_ENGINE_TLS_KEY`
- TLS client: `CONTEXT_ENGINE_TLS_CA`, `CONTEXT_ENGINE_TLS_REQUIRED`, `CONTEXT_ENGINE_INSECURE`

## Scheduler

- `JOB_META_TTL` / `JOB_META_TTL_SECONDS`
- `WORKER_SNAPSHOT_INTERVAL`
- `SCHEDULER_CONFIG_RELOAD_INTERVAL` (interval for config overlay reload, e.g. `30s`)
- `OUTPUT_POLICY_ENABLED` (`0|1|true|false`, default disabled)
- `NATS_JS_ACK_WAIT`, `NATS_JS_MAX_AGE`
- `NATS_JS_REPLICAS` (JetStream stream replication factor)
- `SCHEDULER_METRICS_ADDR` (default `:9090`)
- `SCHEDULER_METRICS_PUBLIC` (set to `1` to allow non-loopback metrics bind in production)

## Workflow engine

- `WORKFLOW_ENGINE_HTTP_ADDR`
- `WORKFLOW_ENGINE_SCAN_INTERVAL`
- `WORKFLOW_ENGINE_RUN_SCAN_LIMIT`

## Safety kernel

- `SAFETY_KERNEL_ADDR`, `SAFETY_POLICY_PATH` (or `SAFETY_POLICY_URL`)
- Policy URL allowlist: `SAFETY_POLICY_URL_ALLOWLIST` (comma-separated hostnames)
- Allow private/loopback policy URLs (not recommended): `SAFETY_POLICY_URL_ALLOW_PRIVATE=1`
- TLS server: `SAFETY_KERNEL_TLS_CERT`, `SAFETY_KERNEL_TLS_KEY`
- TLS client: `SAFETY_KERNEL_TLS_CA`, `SAFETY_KERNEL_TLS_REQUIRED`, `SAFETY_KERNEL_INSECURE`
- Decision cache: `SAFETY_DECISION_CACHE_TTL` (e.g. `5s`, `250ms`)
- Policy signature verification: `SAFETY_POLICY_PUBLIC_KEY`, `SAFETY_POLICY_SIGNATURE`, `SAFETY_POLICY_SIGNATURE_PATH`,
  `SAFETY_POLICY_SIGNATURE_REQUIRED`
- Policy reload/overlays: `SAFETY_POLICY_RELOAD_INTERVAL`, `SAFETY_POLICY_CONFIG_SCOPE`, `SAFETY_POLICY_CONFIG_ID`, `SAFETY_POLICY_CONFIG_KEY`, `SAFETY_POLICY_CONFIG_DISABLE`
- Safety kernel reads policy bundle fragments from the config service in Redis; ensure `REDIS_URL` is set when using pack policy overlays.

## NATS server durability (JetStream)

JetStream fsync cadence is controlled by `sync_interval` in the NATS server
config. Lower values improve crash durability at the cost of throughput.

- Compose: edit `config/nats.conf`.
- K8s base: edit the `cordum-nats-config` ConfigMap in `deploy/k8s/base.yaml`.
- Production overlay: edit the `cordum-nats-config` ConfigMap in `deploy/k8s/production/nats.yaml`.
- Helm: set `nats.jetstream.syncInterval` in `cordum-helm/values.yaml` (or `--set nats.jetstream.syncInterval=1s`).

For full details, see `docs/DOCKER.md`.

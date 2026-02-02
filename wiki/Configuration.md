# Configuration

Cordum is configured via environment variables and YAML config files.

## Required

- `CORDUM_API_KEY` or `CORDUM_API_KEYS` for API auth
- `CORDUM_TENANT_ID` for multi-tenant request isolation

## Common environment variables

- `CORDUM_API_KEY` - single API key
- `CORDUM_API_KEYS` - comma-separated or JSON list of keys
- `CORDUM_API_KEYS_PATH` - path to a JSON or CSV list of keys (hot reload)
- `CORDUM_TENANT_ID` - default tenant for clients/scripts
- `CORDUM_ORG_ID` - organization id (defaults to tenant)
- `CORDUM_ENV=production` (or `CORDUM_PRODUCTION=true`) - enforce production hardening
- `CORDUM_GRPC_REFLECTION=1` - enable gRPC reflection (off by default)
- `CORDUM_ALLOW_INSECURE_NO_AUTH=1` - local-only opt-out for auth (not allowed in production)
- `GATEWAY_METRICS_PUBLIC=1` / `SCHEDULER_METRICS_PUBLIC=1` - bind metrics to 0.0.0.0
- `CORDUM_DASHBOARD_EMBED_API_KEY=1` - embed API key into dashboard config (local dev only)

## TLS (production)

Production mode requires TLS for HTTP/gRPC and for Redis/NATS clients.

- `GATEWAY_HTTP_TLS_CERT`, `GATEWAY_HTTP_TLS_KEY`
- `GRPC_TLS_CERT`, `GRPC_TLS_KEY`
- `REDIS_TLS_CA`, `NATS_TLS_CA`, plus client cert/key if using mTLS

## Config files

Mounted config files (Compose defaults):

- `config/pools.yaml` - worker pool routing
- `config/timeouts.yaml` - timeouts and retries
- `config/safety.yaml` - safety kernel defaults

See `docs/configuration.md` for the full matrix.

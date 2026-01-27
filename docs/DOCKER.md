# Docker Compose Quickstart (platform only)

This repo ships the control-plane stack plus an optional dashboard UI. Compose builds the platform binaries and runs:

Prereqs: Docker + Docker Compose. The smoke test script requires `curl` and `jq`.

- Infra: `nats`, `redis`
- Control plane: `cordum-api-gateway`, `cordum-scheduler`, `cordum-safety-kernel`, `cordum-workflow-engine`
- Optional: `cordum-context-engine` (generic memory helper)
- Optional UI: `cordum-dashboard` (React UI served by a lightweight static server)

## Services in `docker-compose.yml`

- `nats` (JetStream enabled, data volume)
- `redis`
- `cordum-api-gateway` (HTTP :8081, gRPC :8080, metrics :9092)
- `cordum-scheduler` (metrics :9090)
- `cordum-safety-kernel` (gRPC :50051)
- `cordum-workflow-engine` (HTTP health :9093)
- `cordum-context-engine` (gRPC :50070)
- `cordum-dashboard` (UI :8082, talks to gateway)

## Bring up the stack

```bash
export CORDUM_API_KEY="$(openssl rand -hex 32)"
export CORDUM_TENANT_ID=default
docker compose build
docker compose up -d
docker compose ps
```

Docker Compose automatically loads `.env`. The helper scripts read environment
variables from your shell, so keep the `export` lines when running scripts.

## Use GHCR images (release builds)

Export the release version and use the release compose file:

```bash
export CORDUM_VERSION=v0.1.4
docker compose -f docker-compose.release.yml pull
docker compose -f docker-compose.release.yml up -d
```

The release images are published as:
- `ghcr.io/cordum-io/cordum/control-plane:<version>-api-gateway`
- `ghcr.io/cordum-io/cordum/control-plane:<version>-scheduler`
- `ghcr.io/cordum-io/cordum/control-plane:<version>-safety-kernel`
- `ghcr.io/cordum-io/cordum/control-plane:<version>-workflow-engine`
- `ghcr.io/cordum-io/cordum/control-plane:<version>-context-engine`
- `ghcr.io/cordum-io/cordum/dashboard:<version>`

## Smoke test (no workers required)

```bash
CORDUM_API_KEY=${CORDUM_API_KEY:?set CORDUM_API_KEY} \
CORDUM_TENANT_ID=${CORDUM_TENANT_ID:-default} \
bash ./tools/scripts/platform_smoke.sh
```

## API key setup

The gateway requires an API key (or JWT) by default.
Compose now requires `CORDUM_API_KEY` to be set before startup.
Production mode (`CORDUM_ENV=production` or `CORDUM_PRODUCTION=true`) always fails to start without API keys configured.
For local-only testing, you can opt out by setting `CORDUM_ALLOW_INSECURE_NO_AUTH=1` (not allowed in production).

To override:

```bash
cp .env.example .env
# generate a key (requires openssl)
export CORDUM_API_KEY="$(openssl rand -hex 32)"
# set a tenant for requests
export CORDUM_TENANT_ID=default
```

HTTP requests must include `X-API-Key` and `X-Tenant-ID`; gRPC uses metadata `x-api-key`.
The default tenant is `TENANT_ID` (defaults to `default` in compose).
WebSocket stream auth uses `Sec-WebSocket-Protocol: cordum-api-key, <base64url>` plus `?tenant_id=<tenant>` (the dashboard handles this automatically).

The default Compose stack embeds the API key into the dashboard config for local
development (`CORDUM_DASHBOARD_EMBED_API_KEY=true`). Remove that variable in
shared environments to require manual auth.

Production mode (`CORDUM_ENV=production`) requires TLS for HTTP/gRPC and for Redis/NATS clients.
Metrics endpoints bind to loopback in production unless you set `GATEWAY_METRICS_PUBLIC=1`.

For multiple API keys, set `CORDUM_API_KEYS` (comma-separated or JSON). Example:

```
CORDUM_API_KEYS=key-a,key-b
```

API keys support JSON metadata for roles/tenants/expiry, for example:

```
CORDUM_API_KEYS='[{"key":"k1","role":"admin","tenant":"default","expires_at":"2030-01-01T00:00:00Z"}]'
```

To rotate keys without a restart, set `CORDUM_API_KEYS_PATH` to a file with the
same content; the gateway reloads on change.

Enterprise deployments (multi-tenant keys, RBAC, SSO, SIEM export) are configured in the enterprise repo.

## Config mounts

Compose mounts:
- `config/pools.yaml`
- `config/timeouts.yaml`
- `config/safety.yaml`
- `config/nats.conf` (NATS server config; tune `sync_interval` for JetStream durability)

To adjust JetStream durability for local/dev, edit `config/nats.conf` and set
`sync_interval` (lower values improve crash durability at the cost of throughput).

## Environment defaults (compose)

- `NATS_URL=nats://nats:4222`, `REDIS_URL=redis://redis:6379`
- `SAFETY_KERNEL_ADDR=cordum-safety-kernel:50051`
- `POOL_CONFIG_PATH=/etc/cordum/pools.yaml`, `TIMEOUT_CONFIG_PATH=/etc/cordum/timeouts.yaml`
- `NATS_USE_JETSTREAM=1` for scheduler/gateway/workflow engine
 - TLS: `GATEWAY_HTTP_TLS_CERT`, `GATEWAY_HTTP_TLS_KEY`, `GRPC_TLS_CERT`, `GRPC_TLS_KEY`

If you install policy bundles via packs, the safety kernel must have `REDIS_URL`
set so it can load policy fragments from the config service (compose does this
by default).

## Tear down

```bash
docker compose down
```

To remove JetStream data too:

```bash
docker compose down -v
```

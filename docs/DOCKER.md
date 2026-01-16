# Docker Compose Quickstart (platform only)

This repo ships the control-plane stack plus an optional dashboard UI. Compose builds the platform binaries and runs:

- Infra: `nats`, `redis`
- Control plane: `cordum-api-gateway`, `cordum-scheduler`, `cordum-safety-kernel`, `cordum-workflow-engine`
- Optional: `cordum-context-engine` (generic memory helper)
- Optional UI: `cordum-dashboard` (React UI served via Nginx)

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
docker compose build
docker compose up -d
docker compose ps
```

## Use Docker Hub images (release builds)

Export the release version and use the release compose file:

```bash
export CORDUM_VERSION=v0.1.1
docker compose -f docker-compose.release.yml pull
docker compose -f docker-compose.release.yml up -d
```

The release images are published as:
- `cordum/control-plane:<version>-api-gateway`
- `cordum/control-plane:<version>-scheduler`
- `cordum/control-plane:<version>-safety-kernel`
- `cordum/control-plane:<version>-workflow-engine`
- `cordum/control-plane:<version>-context-engine`
- `cordum/dashboard:<version>`

## Smoke test (no workers required)

```bash
./tools/scripts/platform_smoke.sh
```

## API key setup

The gateway enforces an API key when `CORDUM_API_KEY` or `API_KEY` is set.
Compose defaults to `super-secret-key` for local use.

To override:

```bash
cp .env.example .env
# edit CORDUM_API_KEY
```

HTTP requests must include `X-API-Key`; gRPC uses metadata `x-api-key`.
WebSocket stream auth uses `Sec-WebSocket-Protocol: cordum-api-key, <base64url>` (the dashboard handles this automatically).

For multiple API keys, set `CORDUM_API_KEYS` (comma-separated or JSON). Example:

```
CORDUM_API_KEYS=key-a,key-b
```

Enterprise deployments (multi-tenant keys, RBAC, SSO, SIEM export) are configured in the enterprise repo.

## Config mounts

Compose mounts:
- `config/pools.yaml`
- `config/timeouts.yaml`
- `config/safety.yaml`

## Environment defaults (compose)

- `NATS_URL=nats://nats:4222`, `REDIS_URL=redis://redis:6379`
- `SAFETY_KERNEL_ADDR=cordum-safety-kernel:50051`
- `POOL_CONFIG_PATH=/etc/cordum/pools.yaml`, `TIMEOUT_CONFIG_PATH=/etc/cordum/timeouts.yaml`
- `NATS_USE_JETSTREAM=1` for scheduler/gateway/workflow engine

## Tear down

```bash
docker compose down
```

To remove JetStream data too:

```bash
docker compose down -v
```

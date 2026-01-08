# Docker Compose Quickstart (platform only)

This repo ships the control-plane stack plus an optional dashboard UI. Compose builds the platform binaries and runs:

- Infra: `nats`, `redis`
- Control plane: `coretex-api-gateway`, `coretex-scheduler`, `coretex-safety-kernel`, `coretex-workflow-engine`
- Optional: `coretex-context-engine` (generic memory helper)
- Optional UI: `coretex-dashboard` (React UI served via Nginx)

## Services in `docker-compose.yml`

- `nats` (JetStream enabled, data volume)
- `redis`
- `coretex-api-gateway` (HTTP :8081, gRPC :8080, metrics :9092)
- `coretex-scheduler` (metrics :9090)
- `coretex-safety-kernel` (gRPC :50051)
- `coretex-workflow-engine` (HTTP health :9093)
- `coretex-context-engine` (gRPC :50070)
- `coretex-dashboard` (UI :8082, talks to gateway)

## Bring up the stack

```bash
docker compose build
docker compose up -d
docker compose ps
```

## Smoke test (no workers required)

```bash
./tools/scripts/platform_smoke.sh
```

## API key setup

The gateway enforces an API key when `CORETEX_API_KEY` or `API_KEY` is set.
Compose defaults to `super-secret-key` for local use.

To override:

```bash
cp .env.example .env
# edit CORETEX_API_KEY
```

HTTP requests must include `X-API-Key`; gRPC uses metadata `x-api-key`.
WebSocket stream auth uses `Sec-WebSocket-Protocol: coretex-api-key, <base64url>` (the dashboard handles this automatically).

## Config mounts

Compose mounts:
- `config/pools.yaml`
- `config/timeouts.yaml`
- `config/safety.yaml`

## Environment defaults (compose)

- `NATS_URL=nats://nats:4222`, `REDIS_URL=redis://redis:6379`
- `SAFETY_KERNEL_ADDR=coretex-safety-kernel:50051`
- `POOL_CONFIG_PATH=/etc/coretex/pools.yaml`, `TIMEOUT_CONFIG_PATH=/etc/coretex/timeouts.yaml`
- `NATS_USE_JETSTREAM=1` for scheduler/gateway/workflow engine

## Tear down

```bash
docker compose down
```

To remove JetStream data too:

```bash
docker compose down -v
```

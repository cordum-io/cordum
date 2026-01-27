# Quickstart (5 minutes)

This walkthrough starts the local stack and runs a minimal approval workflow
without external workers.

Fastest path:

```bash
export CORDUM_API_KEY="$(openssl rand -hex 32)"
export CORDUM_TENANT_ID=default
./tools/scripts/quickstart.sh
```

`quickstart.sh` brings up the stack and runs the approval smoke test for you.

## 1) Set credentials

```bash
export CORDUM_API_KEY="$(openssl rand -hex 32)"
export CORDUM_TENANT_ID=default
```

Docker Compose loads `.env` automatically; the helper scripts read environment
variables from your shell, so keep the `export` lines when running scripts.

## 2) Start the stack

```bash
go run ./cmd/cordumctl up
```

Or use Docker Compose:

```bash
docker compose build

docker compose up -d
```

## 3) Verify the gateway

```bash
curl -sS http://localhost:8081/api/v1/status \
  -H "X-API-Key: ${CORDUM_API_KEY}" \
  -H "X-Tenant-ID: ${CORDUM_TENANT_ID}" | jq
```

## 4) Run the smoke test

```bash
CORDUM_API_KEY=${CORDUM_API_KEY:?set CORDUM_API_KEY} \
CORDUM_TENANT_ID=${CORDUM_TENANT_ID:-default} \
bash ./tools/scripts/platform_smoke.sh
```

## Next steps

- [Packs and Workers](Packs-and-Workers) for custom capabilities
- [Dashboard](Dashboard) for the UI
- [Demos](Demos) for guardrails and mock bank

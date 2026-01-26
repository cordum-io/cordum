#!/usr/bin/env bash
set -euo pipefail

require() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing dependency: $1" >&2
    exit 1
  fi
}

require curl
require jq
require go

API_BASE=${CORDUM_API_BASE:-http://localhost:8081}
API_KEY=${CORDUM_API_KEY:-${CORDUM_SUPER_SECRET_API_TOKEN:-${API_KEY:-}}}
if [[ -z "${API_KEY}" ]]; then
  echo "CORDUM_API_KEY is required; export it before running the demo." >&2
  exit 1
fi
ORG_ID=${CORDUM_ORG_ID:-${CORDUM_TENANT_ID:-default}}
TENANT_ID=${CORDUM_TENANT_ID:-${ORG_ID}}
NATS_URL=${NATS_URL:-nats://localhost:4222}
REDIS_URL=${REDIS_URL:-redis://localhost:6379}

cleanup() {
  if [[ -n "${WORKER_PID:-}" ]]; then
    kill "${WORKER_PID}" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

echo "[guardrails] checking gateway"
curl -sS "${API_BASE}/api/v1/status" -H "X-API-Key: ${API_KEY}" -H "X-Tenant-ID: ${TENANT_ID}" >/dev/null

echo "[guardrails] starting demo worker"
(cd examples/demo-guardrails/worker && NATS_URL="${NATS_URL}" REDIS_URL="${REDIS_URL}" go run .) &
WORKER_PID=$!
sleep 1

echo "[guardrails] running demo"
CORDUM_API_KEY="${API_KEY}" CORDUM_ORG_ID="${ORG_ID}" CORDUM_TENANT_ID="${TENANT_ID}" CORDUM_API_BASE="${API_BASE}" ./tools/scripts/demo_guardrails.sh

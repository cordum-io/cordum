#!/usr/bin/env bash
set -euo pipefail

require() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing dependency: $1" >&2
    exit 1
  fi
}

require go
require curl

if command -v python3 >/dev/null 2>&1; then
  PYTHON_BIN="python3"
elif command -v python >/dev/null 2>&1; then
  PYTHON_BIN="python"
else
  echo "python3 is required to serve the demo UI" >&2
  exit 1
fi

if command -v cordumctl >/dev/null 2>&1; then
  CTL_BIN="cordumctl"
elif [[ -x "./bin/cordumctl" ]]; then
  CTL_BIN="./bin/cordumctl"
elif [[ -x "./cordumctl" ]]; then
  CTL_BIN="./cordumctl"
else
  CTL_BIN=""
fi

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
PORT=${MOCK_BANK_PORT:-8099}

cleanup() {
  if [[ -n "${WORKER_PID:-}" ]]; then
    kill "${WORKER_PID}" >/dev/null 2>&1 || true
  fi
  if [[ -n "${SERVER_PID:-}" ]]; then
    kill "${SERVER_PID}" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

echo "[mock-bank] checking gateway"
curl -sS "${API_BASE}/api/v1/status" -H "X-API-Key: ${API_KEY}" -H "X-Tenant-ID: ${TENANT_ID}" >/dev/null

echo "[mock-bank] installing pack"
if [[ -n "${CTL_BIN}" && -x "${CTL_BIN}" ]]; then
  CORDUM_API_KEY="${API_KEY}" CORDUM_ORG_ID="${ORG_ID}" CORDUM_TENANT_ID="${TENANT_ID}" "${CTL_BIN}" pack install --upgrade ./demo/mock-bank/pack
else
  CORDUM_API_KEY="${API_KEY}" CORDUM_ORG_ID="${ORG_ID}" CORDUM_TENANT_ID="${TENANT_ID}" go run ./cmd/cordumctl pack install --upgrade ./demo/mock-bank/pack
fi

echo "[mock-bank] starting worker"
(cd demo/mock-bank/worker && NATS_URL="${NATS_URL}" REDIS_URL="${REDIS_URL}" go run .) &
WORKER_PID=$!

echo "[mock-bank] serving UI on :${PORT}"
(cd demo/mock-bank && "${PYTHON_BIN}" -m http.server "${PORT}") &
SERVER_PID=$!

echo ""
echo "Open: http://localhost:${PORT}/?apiKey=${API_KEY}&apiBaseUrl=${API_BASE}&orgId=${ORG_ID}&tenantId=${TENANT_ID}"
echo "Press Ctrl+C to stop."

wait

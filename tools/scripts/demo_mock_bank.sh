#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
cd "${ROOT_DIR}"

require() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing dependency: $1" >&2
    exit 1
  fi
}

require go

if command -v cordumctl >/dev/null 2>&1; then
  CTL_BIN="cordumctl"
elif [[ -x "./bin/cordumctl" ]]; then
  CTL_BIN="./bin/cordumctl"
elif [[ -x "./cordumctl" ]]; then
  CTL_BIN="./cordumctl"
else
  CTL_BIN=""
fi

API_BASE=${CORDUM_API_BASE:-https://localhost:8081}
API_KEY=${CORDUM_API_KEY:-${API_KEY:-}}
if [[ -z "${API_KEY}" ]]; then
  echo "CORDUM_API_KEY is required; export it before running the demo." >&2
  exit 1
fi
export CORDUM_GATEWAY=${CORDUM_GATEWAY:-${API_BASE}}
ORG_ID=${CORDUM_ORG_ID:-${CORDUM_TENANT_ID:-default}}
TENANT_ID=${CORDUM_TENANT_ID:-${ORG_ID}}

# The compose stack is TLS-only: trust the dev CA from `cordumctl generate-certs`
# and give the worker the client certificate that NATS requires.
CURL_TLS_OPTS=()
TLS_CA="${CORDUM_TLS_CA:-}"
if [[ -z "${TLS_CA}" && "${API_BASE}" == https://* && -f "${ROOT_DIR}/certs/ca/ca.crt" ]]; then
  TLS_CA="${ROOT_DIR}/certs/ca/ca.crt"
fi
if [[ -n "${TLS_CA}" ]]; then
  export CORDUM_TLS_CA="${TLS_CA}"
  CURL_TLS_OPTS=(--cacert "${TLS_CA}")
  if curl --version 2>/dev/null | grep -qi schannel; then
    CURL_TLS_OPTS+=(--ssl-no-revoke)
  fi
  NATS_URL=${NATS_URL:-tls://localhost:4222}
  REDIS_URL=${REDIS_URL:-rediss://:${REDIS_PASSWORD:-cordum-dev}@localhost:6379}
  : "${NATS_TLS_CA:=${TLS_CA}}"
  : "${NATS_TLS_CERT:=${ROOT_DIR}/certs/client/tls.crt}"
  : "${NATS_TLS_KEY:=${ROOT_DIR}/certs/client/tls.key}"
  : "${REDIS_TLS_CA:=${TLS_CA}}"
  : "${REDIS_TLS_CERT:=${ROOT_DIR}/certs/client/tls.crt}"
  : "${REDIS_TLS_KEY:=${ROOT_DIR}/certs/client/tls.key}"
  export NATS_TLS_CA NATS_TLS_CERT NATS_TLS_KEY REDIS_TLS_CA REDIS_TLS_CERT REDIS_TLS_KEY
else
  NATS_URL=${NATS_URL:-nats://localhost:4222}
  REDIS_URL=${REDIS_URL:-redis://:${REDIS_PASSWORD:-cordum-dev}@localhost:6379}
fi

MODE=${1:-docker}

install_pack() {
  if [[ -n "${CTL_BIN}" && -x "${CTL_BIN}" ]]; then
    CORDUM_API_KEY="${API_KEY}" CORDUM_ORG_ID="${ORG_ID}" CORDUM_TENANT_ID="${TENANT_ID}" "${CTL_BIN}" pack install --upgrade ./demo/mock-bank/pack
  else
    CORDUM_API_KEY="${API_KEY}" CORDUM_ORG_ID="${ORG_ID}" CORDUM_TENANT_ID="${TENANT_ID}" go run ./cmd/cordumctl pack install --upgrade ./demo/mock-bank/pack
  fi
}

if [[ "${MODE}" == "docker" ]]; then
  echo "[mock-bank] starting all services with demo profile..."
  docker compose --profile demo up -d --build

  echo "[mock-bank] waiting for services to be healthy..."
  sleep 5

  echo "[mock-bank] installing pack..."
  install_pack

  echo ""
  echo "=== Mock Bank Demo Ready ==="
  # The UI's nginx proxies /api/ to the gateway, so point the UI at itself.
  echo "Mock Bank UI:  http://localhost:${MOCK_BANK_UI_PORT:-3000}/?apiBaseUrl=http://localhost:${MOCK_BANK_UI_PORT:-3000}"
  echo "Dashboard:     http://localhost:8082"
  echo "API Gateway:   ${API_BASE}"
  echo ""
  echo "When prompted, paste your API key."
  echo "Try: \$40 (auto), \$200 (approval), \$5000 (blocked)"
  echo ""
  echo "To stop: docker compose --profile demo down"
  exit 0
fi

# --- Manual mode: run worker and UI server outside Docker ---

require curl

if command -v python3 >/dev/null 2>&1; then
  PYTHON_BIN="python3"
elif command -v python >/dev/null 2>&1; then
  PYTHON_BIN="python"
else
  echo "python3 is required to serve the demo UI in manual mode" >&2
  exit 1
fi

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
curl -sS "${CURL_TLS_OPTS[@]}" "${API_BASE}/api/v1/status" -H "X-API-Key: ${API_KEY}" -H "X-Tenant-ID: ${TENANT_ID}" >/dev/null

echo "[mock-bank] installing pack"
install_pack

echo "[mock-bank] starting worker"
(cd demo/mock-bank/worker && NATS_URL="${NATS_URL}" REDIS_URL="${REDIS_URL}" go run .) &
WORKER_PID=$!

echo "[mock-bank] serving UI on :${PORT}"
(cd demo/mock-bank && "${PYTHON_BIN}" -m http.server "${PORT}") &
SERVER_PID=$!

echo ""
echo "Open: http://localhost:${PORT}/?apiBaseUrl=${API_BASE}&orgId=${ORG_ID}&tenantId=${TENANT_ID}"
echo "When prompted, paste your API key."
echo "Try: \$40 (auto), \$200 (approval), \$5000 (blocked)"
echo "Press Ctrl+C to stop."

wait

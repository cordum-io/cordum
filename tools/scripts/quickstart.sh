#!/usr/bin/env bash
set -euo pipefail

require() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing dependency: $1" >&2
    exit 1
  fi
}

require docker
require curl
require jq

if ! docker compose version >/dev/null 2>&1; then
  echo "docker compose plugin required" >&2
  exit 1
fi

API_KEY=${CORDUM_API_KEY:-${CORDUM_SUPER_SECRET_API_TOKEN:-${API_KEY:-[REDACTED]}}}
ORG_ID=${CORDUM_ORG_ID:-default}
COMPOSE_FILES=${CORDUM_COMPOSE_FILES:-docker-compose.yml}
ALLOW_ENTERPRISE=${CORDUM_ALLOW_ENTERPRISE:-0}
SKIP_BUILD=${CORDUM_SKIP_BUILD:-0}
export COMPOSE_HTTP_TIMEOUT=${COMPOSE_HTTP_TIMEOUT:-1800}
export DOCKER_CLIENT_TIMEOUT=${DOCKER_CLIENT_TIMEOUT:-1800}

compose_args=()
for file in ${COMPOSE_FILES}; do
  if [[ "${ALLOW_ENTERPRISE}" != "1" && "${file}" == *enterprise* ]]; then
    echo "enterprise compose overrides are not supported in quickstart (OSS only)." >&2
    echo "Set CORDUM_ALLOW_ENTERPRISE=1 if you really want to use enterprise overrides." >&2
    exit 1
  fi
  compose_args+=("-f" "${file}")
done
compose_args+=("up" "-d")
if [[ "${SKIP_BUILD}" != "1" ]]; then
  compose_args+=("--build")
fi

echo "[quickstart] starting stack"
docker compose "${compose_args[@]}"

echo "[quickstart] stack ready"
echo "Gateway: http://localhost:8081"
echo "Dashboard: http://localhost:8082"
echo "API key: ${API_KEY}"
echo ""
echo "[quickstart] running smoke test"
CORDUM_API_KEY="${API_KEY}" CORDUM_ORG_ID="${ORG_ID}" ./tools/scripts/platform_smoke.sh
echo ""
echo "[quickstart] try:"
echo "curl -sS http://localhost:8081/api/v1/status -H \"X-API-Key: ${API_KEY}\" | jq"

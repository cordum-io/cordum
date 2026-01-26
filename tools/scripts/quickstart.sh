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

compose_cmd=()
if docker compose version >/dev/null 2>&1; then
  compose_cmd=(docker compose)
elif command -v docker-compose >/dev/null 2>&1; then
  compose_cmd=(docker-compose)
  echo "[quickstart] warning: docker-compose v1 detected; prefer Docker Compose v2." >&2
else
  echo "docker compose plugin required" >&2
  exit 1
fi

if ! docker info >/dev/null 2>&1; then
  echo "cannot connect to the Docker daemon." >&2
  echo "ensure Docker is running and your user can access /var/run/docker.sock." >&2
  echo "on Linux, add your user to the docker group or re-run with sudo." >&2
  exit 1
fi

port_in_use() {
  local port="$1"
  if command -v ss >/dev/null 2>&1; then
    ss -ltn 2>/dev/null | awk '{print $4}' | grep -E "(^|:)${port}$" >/dev/null 2>&1
    return $?
  fi
  if command -v lsof >/dev/null 2>&1; then
    lsof -iTCP:"${port}" -sTCP:LISTEN >/dev/null 2>&1
    return $?
  fi
  if command -v netstat >/dev/null 2>&1; then
    netstat -ltn 2>/dev/null | awk '{print $4}' | grep -E "(^|:)${port}$" >/dev/null 2>&1
    return $?
  fi
  return 2
}

warn_port() {
  local port="$1"
  local name="$2"
  if port_in_use "${port}"; then
    echo "[quickstart] warning: port ${port} (${name}) is already in use; compose may fail to bind." >&2
  fi
}

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/../.." && pwd)"
if [[ ! -f "docker-compose.yml" && -f "${repo_root}/docker-compose.yml" ]]; then
  cd "${repo_root}"
fi

warn_port 8081 "api-gateway http"
warn_port 8082 "dashboard"
warn_port 8080 "api-gateway grpc"
warn_port 9092 "gateway metrics"
warn_port 9093 "workflow-engine http"
warn_port 50051 "safety-kernel grpc"
warn_port 50070 "context-engine grpc"
warn_port 4222 "nats client"
warn_port 6379 "redis"

API_KEY=${CORDUM_API_KEY:-${CORDUM_SUPER_SECRET_API_TOKEN:-${API_KEY:-}}}
if [[ -z "${API_KEY}" ]]; then
  echo "CORDUM_API_KEY is required; export it before running quickstart." >&2
  exit 1
fi
export CORDUM_API_KEY="${API_KEY}"
ORG_ID=${CORDUM_ORG_ID:-${CORDUM_TENANT_ID:-default}}
TENANT_ID=${CORDUM_TENANT_ID:-${ORG_ID}}
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
"${compose_cmd[@]}" "${compose_args[@]}"

echo "[quickstart] stack ready"
echo "Gateway: http://localhost:8081"
echo "Dashboard: http://localhost:8082"
echo "API key: ${API_KEY}"
echo ""
echo "[quickstart] running smoke test"
CORDUM_API_KEY="${API_KEY}" CORDUM_ORG_ID="${ORG_ID}" CORDUM_TENANT_ID="${TENANT_ID}" ./tools/scripts/platform_smoke.sh
echo ""
echo "[quickstart] try:"
echo "curl -sS http://localhost:8081/api/v1/status -H \"X-API-Key: ${API_KEY}\" -H \"X-Tenant-ID: ${TENANT_ID}\" | jq"

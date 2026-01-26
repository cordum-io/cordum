#!/usr/bin/env bash
set -euo pipefail

require() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing dependency: $1" >&2
    exit 1
  fi
}

require git
require docker
require curl
require jq

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

assert_ports_free() {
  local allow="${CORDUM_E2E_ALLOW_PORTS:-0}"
  local ports=(8081 8082 8080 9092 9093 50051 50070 4222 6379)
  if [[ "${allow}" == "1" ]]; then
    return 0
  fi
  for port in "${ports[@]}"; do
    if port_in_use "${port}"; then
      echo "port ${port} is already in use; set CORDUM_E2E_ALLOW_PORTS=1 to override." >&2
      exit 1
    fi
  done
}

API_KEY=${CORDUM_API_KEY:-${CORDUM_SUPER_SECRET_API_TOKEN:-${API_KEY:-}}}
if [[ -z "${API_KEY}" ]]; then
  echo "CORDUM_API_KEY is required; export it before running this script." >&2
  exit 1
fi
export CORDUM_API_KEY="${API_KEY}"

TENANT_ID=${CORDUM_TENANT_ID:-default}
ORG_ID=${CORDUM_ORG_ID:-${TENANT_ID}}
export CORDUM_TENANT_ID="${TENANT_ID}"
export CORDUM_ORG_ID="${ORG_ID}"

REPO_URL=${REPO_URL:-https://github.com/cordum-io/cordum.git}
DEST_DIR=${DEST_DIR:-/tmp/cordum-e2e}
VERSION=${VERSION:-main}
USE_RELEASE_IMAGES=${USE_RELEASE_IMAGES:-0}
CORDUM_VERSION=${CORDUM_VERSION:-latest}

if [[ -d "${DEST_DIR}" ]]; then
  if [[ "${CORDUM_E2E_CLEAN:-0}" == "1" ]]; then
    rm -rf "${DEST_DIR}"
  elif [[ "${CORDUM_E2E_REUSE:-0}" == "1" ]]; then
    echo "[e2e] reusing existing ${DEST_DIR}"
  else
    echo "${DEST_DIR} already exists; set CORDUM_E2E_CLEAN=1 to delete or CORDUM_E2E_REUSE=1 to reuse." >&2
    exit 1
  fi
fi

assert_ports_free

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/../.." && pwd)"

if [[ ! -f "${repo_root}/tools/scripts/install.sh" ]]; then
  echo "install.sh not found at ${repo_root}/tools/scripts/install.sh" >&2
  exit 1
fi

if [[ "${CORDUM_E2E_REUSE:-0}" != "1" ]]; then
  echo "[e2e] installing Cordum (${VERSION}) into ${DEST_DIR}"
  REPO_URL="${REPO_URL}" \
  DEST_DIR="${DEST_DIR}" \
  VERSION="${VERSION}" \
  USE_RELEASE_IMAGES="${USE_RELEASE_IMAGES}" \
  CORDUM_VERSION="${CORDUM_VERSION}" \
  CORDUM_API_KEY="${API_KEY}" \
  CORDUM_TENANT_ID="${TENANT_ID}" \
  bash "${repo_root}/tools/scripts/install.sh"
fi

pushd "${DEST_DIR}" >/dev/null

echo "[e2e] running approval workflow smoke test"
CORDUM_API_KEY="${API_KEY}" CORDUM_TENANT_ID="${TENANT_ID}" CORDUM_ORG_ID="${ORG_ID}" \
  ./tools/scripts/platform_smoke.sh

if [[ "${CORDUM_E2E_TEARDOWN:-0}" == "1" ]]; then
  echo "[e2e] tearing down compose stack"
  if docker compose version >/dev/null 2>&1; then
    docker compose down -v
  else
    docker-compose down -v
  fi
fi

popd >/dev/null

echo "[e2e] done"

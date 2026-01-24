#!/usr/bin/env bash
set -euo pipefail

REPO_URL=${REPO_URL:-https://github.com/cordum-io/cordum.git}
DEST_DIR=${DEST_DIR:-cordum}
VERSION=${VERSION:-main}
USE_RELEASE_IMAGES=${USE_RELEASE_IMAGES:-0}
CORDUM_VERSION=${CORDUM_VERSION:-latest}

require() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing dependency: $1" >&2
    exit 1
  fi
}

require git
require docker
compose_cmd=()
if docker compose version >/dev/null 2>&1; then
  compose_cmd=(docker compose)
elif command -v docker-compose >/dev/null 2>&1; then
  compose_cmd=(docker-compose)
  echo "[install] warning: docker-compose v1 detected; prefer Docker Compose v2." >&2
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
    echo "[install] warning: port ${port} (${name}) is already in use; compose may fail to bind." >&2
  fi
}

warn_port 8081 "api-gateway http"
warn_port 8082 "dashboard"
warn_port 8080 "api-gateway grpc"
warn_port 9092 "gateway metrics"
warn_port 9093 "workflow-engine http"
warn_port 50051 "safety-kernel grpc"
warn_port 50070 "context-engine grpc"
warn_port 4222 "nats client"
warn_port 6379 "redis"

if [ -d "${DEST_DIR}/.git" ]; then
  echo "using existing repo at ${DEST_DIR}"
else
  echo "cloning ${REPO_URL} to ${DEST_DIR}"
  git clone --depth 1 --branch "${VERSION}" "${REPO_URL}" "${DEST_DIR}"
fi

cd "${DEST_DIR}"

if [ "${USE_RELEASE_IMAGES}" = "1" ]; then
  echo "starting from Docker Hub images (CORDUM_VERSION=${CORDUM_VERSION})"
  export CORDUM_VERSION
  "${compose_cmd[@]}" -f docker-compose.release.yml pull
  "${compose_cmd[@]}" -f docker-compose.release.yml up -d
else
  echo "building from source"
  "${compose_cmd[@]}" build
  "${compose_cmd[@]}" up -d
fi

cat <<'EOF'
Cordum is up.
- Dashboard: http://localhost:8082
- API: http://localhost:8081
Next steps (OSS, from repo root):
- Quickstart: ./tools/scripts/quickstart.sh
- Smoke test: CORDUM_API_KEY=[REDACTED] ./tools/scripts/platform_smoke.sh
- Guardrails demo: ./tools/scripts/demo_guardrails_run.sh
- Mock bank demo: ./tools/scripts/demo_mock_bank.sh
EOF

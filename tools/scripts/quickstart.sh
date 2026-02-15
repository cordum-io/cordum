#!/usr/bin/env bash
# =============================================================================
# Cordum Quickstart — deterministic deploy with health checks & artifact capture
#
# Usage:
#   ./tools/scripts/quickstart.sh [--clean] [--artifacts-dir DIR] [--skip-build]
#                                 [--skip-smoke] [--health-timeout N]
#
# Flags:
#   --clean           Tear down existing stack (compose down -v) before starting
#   --artifacts-dir   Directory for deploy artifacts (logs, status snapshots)
#   --skip-build      Reuse existing images (do not rebuild)
#   --skip-smoke      Skip the post-deploy smoke test
#   --health-timeout  Seconds to wait for health readiness (default: 120)
# =============================================================================
set -euo pipefail

require() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing dependency: $1" >&2
    exit 1
  fi
}

log() { echo "[quickstart] $*"; }
die() { echo "[quickstart] ERROR: $*" >&2; exit 1; }

require docker
require curl

if [[ -z "${REDIS_PASSWORD:-}" ]]; then
  log "warning: REDIS_PASSWORD not set — using dev default 'cordum-dev'"
  log "         Set REDIS_PASSWORD for production deployments"
fi

compose_cmd=()
if docker compose version >/dev/null 2>&1; then
  compose_cmd=(docker compose)
elif command -v docker-compose >/dev/null 2>&1; then
  compose_cmd=(docker-compose)
  log "warning: docker-compose v1 detected; prefer Docker Compose v2."
else
  die "docker compose plugin required"
fi

if ! docker info >/dev/null 2>&1; then
  die "cannot connect to the Docker daemon. Ensure Docker is running."
fi

port_in_use() {
  local port="$1"
  if command -v ss >/dev/null 2>&1; then
    if ss -ltn 2>/dev/null | awk '{print $4}' | grep -E "(^|:)${port}$" >/dev/null 2>&1; then
      return 0
    fi
    return 1
  fi
  if command -v lsof >/dev/null 2>&1; then
    if lsof -iTCP:"${port}" -sTCP:LISTEN >/dev/null 2>&1; then
      return 0
    fi
    return 1
  fi
  if command -v netstat >/dev/null 2>&1; then
    if netstat -ltn 2>/dev/null | awk '{print $4}' | grep -E "(^|:)${port}$" >/dev/null 2>&1; then
      return 0
    fi
    return 1
  fi
  return 2
}

warn_port() {
  local port="$1"
  local name="$2"
  if port_in_use "${port}"; then
    log "warning: port ${port} (${name}) is already in use; compose may fail to bind."
  fi
}

# Wait for the gateway /api/v1/status to report nats.connected=true and redis.ok=true
wait_for_health() {
  local timeout_sec="$1"
  local api_base="$2"
  local api_key="$3"
  local tenant_id="$4"
  local elapsed=0
  local body

  log "waiting for health readiness (timeout: ${timeout_sec}s)"
  while (( elapsed < timeout_sec )); do
    body="$(curl -sS -m 5 "${CURL_TLS_OPTS[@]}" \
      -H "X-API-Key: ${api_key}" -H "X-Tenant-ID: ${tenant_id}" \
      "${api_base}/api/v1/status" 2>/dev/null || true)"
    if [[ -n "${body}" ]]; then
      # Check with jq if available, otherwise fallback to grep
      if command -v jq >/dev/null 2>&1; then
        if echo "${body}" | jq -e '.nats.connected == true and .redis.ok == true' >/dev/null 2>&1; then
          log "health ready (${elapsed}s)"
          return 0
        fi
      else
        if echo "${body}" | grep -qE '"connected"\s*:\s*true' && echo "${body}" | grep -qE '"ok"\s*:\s*true'; then
          log "health ready (${elapsed}s)"
          return 0
        fi
      fi
    fi
    sleep 2
    elapsed=$((elapsed + 2))
  done
  log "health check timed out after ${timeout_sec}s"
  return 1
}

# Capture deploy artifacts: compose status, service logs, and env snapshot
capture_artifacts() {
  local dir="$1"
  local timestamp
  timestamp="$(date -u +%Y%m%dT%H%M%SZ)"

  mkdir -p "${dir}"

  log "capturing artifacts to ${dir}"

  # Container status
  "${compose_cmd[@]}" ps --format "table {{.Name}}\t{{.Status}}\t{{.Ports}}" \
    > "${dir}/compose-status-${timestamp}.txt" 2>/dev/null || \
    "${compose_cmd[@]}" ps > "${dir}/compose-status-${timestamp}.txt" 2>/dev/null || true

  # Service logs (last 200 lines per service, no follow)
  for svc in nats redis context-engine safety-kernel scheduler api-gateway workflow-engine dashboard; do
    "${compose_cmd[@]}" logs --tail=200 --no-color "${svc}" \
      > "${dir}/${svc}-${timestamp}.log" 2>&1 || true
  done

  # Docker image versions
  "${compose_cmd[@]}" images \
    > "${dir}/compose-images-${timestamp}.txt" 2>/dev/null || true

  # Git commit (if in a repo)
  if command -v git >/dev/null 2>&1 && git rev-parse --git-dir >/dev/null 2>&1; then
    git log --oneline -5 > "${dir}/git-log-${timestamp}.txt" 2>/dev/null || true
  fi

  log "artifacts captured (${dir})"
}

# --- Parse arguments ---
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/../.." && pwd)"
if [[ ! -f "docker-compose.yml" && -f "${repo_root}/docker-compose.yml" ]]; then
  cd "${repo_root}"
fi

CLEAN=0
ARTIFACTS_DIR=""
SKIP_BUILD=${CORDUM_SKIP_BUILD:-0}
SKIP_SMOKE=0
HEALTH_TIMEOUT=120

while [[ $# -gt 0 ]]; do
  case "$1" in
    --clean)         CLEAN=1; shift ;;
    --artifacts-dir) ARTIFACTS_DIR="$2"; shift 2 ;;
    --skip-build)    SKIP_BUILD=1; shift ;;
    --skip-smoke)    SKIP_SMOKE=1; shift ;;
    --health-timeout) HEALTH_TIMEOUT="$2"; shift 2 ;;
    -h|--help)
      sed -n '2,/^# =====/{ /^#/s/^# //p }' "${BASH_SOURCE[0]}"
      exit 0
      ;;
    *) die "unknown argument: $1" ;;
  esac
done

# --- Check ports ---
warn_port 8081 "api-gateway http"
warn_port 8082 "dashboard"
warn_port 8080 "api-gateway grpc"
warn_port 9092 "gateway metrics"
warn_port 9093 "workflow-engine http"
warn_port 50051 "safety-kernel grpc"
warn_port 50070 "context-engine grpc"
warn_port 4222 "nats client"
warn_port 6379 "redis"

# --- Env vars ---
API_KEY=${CORDUM_API_KEY:-${API_KEY:-}}
if [[ -z "${API_KEY}" ]]; then
  die "CORDUM_API_KEY is required; export it before running quickstart."
fi
export CORDUM_API_KEY="${API_KEY}"
ORG_ID=${CORDUM_ORG_ID:-${CORDUM_TENANT_ID:-default}}
TENANT_ID=${CORDUM_TENANT_ID:-${ORG_ID}}
COMPOSE_FILES=${CORDUM_COMPOSE_FILES:-docker-compose.yml}
ALLOW_ENTERPRISE=${CORDUM_ALLOW_ENTERPRISE:-0}
export COMPOSE_HTTP_TIMEOUT=${COMPOSE_HTTP_TIMEOUT:-1800}
export DOCKER_CLIENT_TIMEOUT=${DOCKER_CLIENT_TIMEOUT:-1800}

# --- TLS auto-detection ---
CURL_TLS_OPTS=()
TLS_CA="${CORDUM_TLS_CA:-}"
if [[ -z "${TLS_CA}" && -f "./certs/ca/ca.crt" ]]; then
  TLS_CA="./certs/ca/ca.crt"
fi
if [[ -n "${TLS_CA}" ]]; then
  CURL_TLS_OPTS=(--cacert "${TLS_CA}")
  # Windows/Schannel needs --ssl-no-revoke for self-signed CA certs.
  if curl --version 2>/dev/null | grep -qi schannel; then
    CURL_TLS_OPTS+=(--ssl-no-revoke)
  fi
  API_BASE="${CORDUM_API_BASE:-https://localhost:8081}"
  log "TLS detected (CA: ${TLS_CA})"
else
  API_BASE="${CORDUM_API_BASE:-http://localhost:8081}"
fi

# --- Clean teardown ---
if [[ "${CLEAN}" == "1" ]]; then
  log "tearing down existing stack (clean deploy)"
  "${compose_cmd[@]}" down -v --remove-orphans 2>/dev/null || true
  log "teardown complete"
fi

# --- Build compose args ---
compose_args=()
for file in ${COMPOSE_FILES}; do
  if [[ "${ALLOW_ENTERPRISE}" != "1" && "${file}" == *enterprise* ]]; then
    die "enterprise compose overrides are not supported in quickstart (OSS only). Set CORDUM_ALLOW_ENTERPRISE=1 to override."
  fi
  compose_args+=("-f" "${file}")
done
compose_args+=("up" "-d")
if [[ "${SKIP_BUILD}" != "1" ]]; then
  compose_args+=("--build")
fi

# --- Start stack ---
log "starting stack"
"${compose_cmd[@]}" "${compose_args[@]}"

# --- Health readiness ---
if ! wait_for_health "${HEALTH_TIMEOUT}" "${API_BASE}" "${API_KEY}" "${TENANT_ID}"; then
  log "stack did not become healthy within ${HEALTH_TIMEOUT}s"
  # Capture artifacts even on failure for debugging
  if [[ -n "${ARTIFACTS_DIR}" ]]; then
    capture_artifacts "${ARTIFACTS_DIR}"
  fi
  exit 1
fi

log "stack ready"

# Verify config auto-bootstrap completed
config_status="$(curl -s -o /dev/null -w '%{http_code}' "${CURL_TLS_OPTS[@]}" \
  -H "X-API-Key: ${API_KEY}" -H "X-Tenant-ID: ${TENANT_ID}" \
  "${API_BASE}/api/v1/config" 2>/dev/null || echo "000")"
if [[ "${config_status}" == "200" ]]; then
  log "config auto-bootstrap verified"
else
  log "warning: config endpoint returned ${config_status} — settings page may show empty state"
fi

echo "  Gateway:   ${API_BASE}"
echo "  Dashboard: http://localhost:8082"
echo "  API key:   ${API_KEY:0:8}... (masked — check .env or CORDUM_API_KEY env var)"

# --- Capture artifacts ---
if [[ -n "${ARTIFACTS_DIR}" ]]; then
  capture_artifacts "${ARTIFACTS_DIR}"
fi

# --- Smoke test ---
if [[ "${SKIP_SMOKE}" == "1" ]]; then
  log "skipping smoke test (--skip-smoke)"
else
  echo ""
  log "running smoke test"
  CORDUM_API_KEY="${API_KEY}" CORDUM_ORG_ID="${ORG_ID}" CORDUM_TENANT_ID="${TENANT_ID}" ./tools/scripts/platform_smoke.sh
fi

echo ""
log "try:"
echo "curl -sS ${API_BASE}/api/v1/status -H \"X-API-Key: \$CORDUM_API_KEY\" -H \"X-Tenant-ID: ${TENANT_ID}\" | jq"

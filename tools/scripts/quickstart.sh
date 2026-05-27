#!/usr/bin/env bash
# =============================================================================
# Cordum Quickstart — zero-config bootstrap with health checks & artifact capture
# Docs: docs/deployment/quickstart-env-contract.md
#
# Usage:
#   ./tools/scripts/quickstart.sh [--clean] [--artifacts-dir DIR] [--skip-build]
#                                 [--skip-smoke] [--skip-doctor] [--strict] [--health-timeout N]
#
# Flags:
#   --clean           Tear down existing stack (compose down -v) before starting
#   --artifacts-dir   Directory for deploy artifacts (logs, status snapshots)
#                     Includes predeploy baseline, failure snapshot, and postdeploy snapshot.
#   --skip-build      Reuse existing images (do not rebuild)
#   --skip-smoke      Skip the post-deploy smoke test (also skips doctor)
#   --skip-doctor     Skip the post-deploy `cordumctl doctor` verification
#   --strict          Abort on any detected host/container env divergence
#   --health-timeout  Seconds to wait for health readiness (default: 120)
#
# This script auto-creates .env, generates credentials, and starts the full
# stack. No manual setup needed — just run it.
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

resolve_cordumctl() {
  if [[ -n "${CORDUMCTL_BIN:-}" ]]; then
    printf '%s' "${CORDUMCTL_BIN}"
    return 0
  fi
  if [[ -n "${CORDUMCTL:-}" ]]; then
    printf '%s' "${CORDUMCTL}"
    return 0
  fi
  if command -v cordumctl >/dev/null 2>&1; then
    command -v cordumctl
    return 0
  fi
  return 1
}

# --- Generate a random hex string (no openssl dependency) ---
gen_hex() {
  local len="${1:-32}"
  if command -v openssl >/dev/null 2>&1; then
    openssl rand -hex "$len"
  elif [[ -r /dev/urandom ]]; then
    head -c "$len" /dev/urandom | xxd -p | tr -d '\n' | head -c $((len * 2))
  elif command -v python >/dev/null 2>&1; then
    python -c "import secrets; print(secrets.token_hex($len))"
  else
    die "cannot generate random key — install openssl, or ensure /dev/urandom is readable"
  fi
}

# --- Default dashboard admin password ---
# Seeded by the login bootstrap below when CORDUM_ADMIN_PASSWORD is unset. Must
# satisfy the gateway policy (>=12 chars, with an uppercase letter, a digit, and
# a special character — see core/controlplane/gateway/auth.ValidatePassword).
# This is a well-known DEV default for frictionless first login — CHANGE it
# before exposing the stack beyond localhost. Override via the environment with
# CORDUM_DEFAULT_ADMIN_PASSWORD.
DEFAULT_ADMIN_PASSWORD="${CORDUM_DEFAULT_ADMIN_PASSWORD:-ChangeMe123!}"

# --- Pre-flight checks ---
require docker
require curl

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

# persist_env_var writes KEY=VALUE into .env, replacing any existing KEY line.
# Keeps the shell (used for /status probe) and docker-compose (reads .env)
# in agreement after auto-generation. Creates .env if absent.
persist_env_var() {
  local key="$1"
  local value="$2"
  local file=".env"
  if [[ ! -f "${file}" ]]; then
    echo "${key}=${value}" > "${file}"
    return 0
  fi
  if grep -qE "^${key}=" "${file}"; then
    # Replace existing line in place; awk avoids sed's escaping headache
    # with hex secrets that can contain characters like / or &.
    local tmp
    tmp="$(mktemp)"
    awk -v k="${key}" -v v="${value}" 'BEGIN{p=0} {
      if ($0 ~ "^" k "=") { print k "=" v; p=1 } else { print $0 }
    } END{ if (!p) print k "=" v }' "${file}" > "${tmp}" && mv "${tmp}" "${file}"
  else
    echo "${key}=${value}" >> "${file}"
  fi
}

preflight_deploy() {
  local compose_files="$1"
  local allow_enterprise="$2"
  local api_key="$3"
  local auth_enabled="$4"
  local admin_password="$5"
  local admin_email="$6"
  local file

  log "running pre-flight deployment checks"
  log "docker daemon: reachable"
  log "compose command: ${compose_cmd[*]}"

  # Zero-config bootstrap: generate fresh secrets when the caller hasn't
  # pre-seeded them via env. Both values are written to .env so subsequent
  # runs + the advertised `cat .env` discovery path keep working. Callers
  # who need deterministic values can still override via the env before
  # invocation.
  if [[ -z "${api_key}" ]]; then
    api_key="$(openssl rand -hex 32)"
    export CORDUM_API_KEY="${api_key}"
    persist_env_var CORDUM_API_KEY "${api_key}"
    mark_env_source CORDUM_API_KEY "auto-generated" ".env"
    log "CORDUM_API_KEY: auto-generated and persisted to .env"
  fi

  if [[ -z "${REDIS_PASSWORD:-}" ]]; then
    REDIS_PASSWORD="$(openssl rand -hex 24)"
    export REDIS_PASSWORD
    persist_env_var REDIS_PASSWORD "${REDIS_PASSWORD}"
    mark_env_source REDIS_PASSWORD "auto-generated" ".env"
    log "REDIS_PASSWORD: auto-generated and persisted to .env"
  fi

  # Optional auth vars become required only when user auth is enabled.
  local auth_flag
  auth_flag="$(echo "${auth_enabled}" | tr '[:upper:]' '[:lower:]')"
  if [[ "${auth_flag}" == "1" || "${auth_flag}" == "true" || "${auth_flag}" == "yes" ]]; then
    if [[ -z "${admin_password}" ]]; then
      die "CORDUM_USER_AUTH_ENABLED=true but CORDUM_ADMIN_PASSWORD is empty."
    fi
    if [[ -z "${admin_email}" ]]; then
      log "warning: CORDUM_USER_AUTH_ENABLED=true but CORDUM_ADMIN_EMAIL is empty."
      log "         Set CORDUM_ADMIN_EMAIL for clearer operator identity metadata."
    fi
  fi

  if [[ -z "${compose_files}" ]]; then
    die "CORDUM_COMPOSE_FILES resolved to empty; provide at least docker-compose.yml"
  fi

  log "compose file set:"
  for file in ${compose_files}; do
    if [[ "${allow_enterprise}" != "1" && "${file}" == *enterprise* ]]; then
      die "enterprise compose overrides are not supported in quickstart (OSS only). Set CORDUM_ALLOW_ENTERPRISE=1 to override."
    fi
    if [[ ! -f "${file}" ]]; then
      die "compose file not found: ${file}"
    fi
    log "  - ${file}"
  done

  log "pre-flight checks passed"
}

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
  local phase="${2:-deploy}"
  local timestamp
  timestamp="$(date -u +%Y%m%dT%H%M%SZ)"

  mkdir -p "${dir}"

  log "capturing artifacts to ${dir}"

  # Container status
  "${compose_cmd[@]}" ps --format "table {{.Name}}\t{{.Status}}\t{{.Ports}}" \
    > "${dir}/${phase}-compose-status-${timestamp}.txt" 2>/dev/null || \
    "${compose_cmd[@]}" ps > "${dir}/${phase}-compose-status-${timestamp}.txt" 2>/dev/null || true

  # Service logs (last 200 lines per service, no follow)
  for svc in nats redis context-engine safety-kernel scheduler api-gateway workflow-engine dashboard; do
    "${compose_cmd[@]}" logs --tail=200 --no-color "${svc}" \
      > "${dir}/${phase}-${svc}-${timestamp}.log" 2>&1 || true
  done

  # Docker image versions
  "${compose_cmd[@]}" images \
    > "${dir}/${phase}-compose-images-${timestamp}.txt" 2>/dev/null || true

  # Git commit (if in a repo)
  if command -v git >/dev/null 2>&1 && git rev-parse --git-dir >/dev/null 2>&1; then
    git log --oneline -5 > "${dir}/${phase}-git-log-${timestamp}.txt" 2>/dev/null || true
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
SKIP_DOCTOR=0
STRICT=${CORDUM_QUICKSTART_STRICT:-0}
HEALTH_TIMEOUT=120

while [[ $# -gt 0 ]]; do
  case "$1" in
    --clean)         CLEAN=1; shift ;;
    --artifacts-dir) ARTIFACTS_DIR="$2"; shift 2 ;;
    --skip-build)    SKIP_BUILD=1; shift ;;
    --skip-smoke)    SKIP_SMOKE=1; shift ;;
    --skip-doctor)   SKIP_DOCTOR=1; shift ;;
    --strict)         STRICT=1; shift ;;
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
warn_port 9080 "api-gateway grpc"
warn_port 9092 "gateway metrics"
warn_port 9093 "workflow-engine http"
warn_port 50051 "safety-kernel grpc"
warn_port 50400 "context-engine grpc"
warn_port 4222 "nats client"
warn_port 6379 "redis"

# --- Env vars ---
# If .env exists from a prior run, share its values with THIS shell so the
# health probe uses the same API_KEY/REDIS_PASSWORD/admin creds that docker-
# compose is feeding the containers. Without this the script would auto-
# generate fresh secrets into its own shell env while the containers kept
# using .env's existing values — the /status probe would then 401 for the
# full health timeout. Only vars that aren't already set in the shell are
# read; shell-exported overrides still win.
TRACKED_ENV_VARS=(
  CORDUM_API_KEY
  REDIS_PASSWORD
  CORDUM_ADMIN_PASSWORD
  CORDUM_ADMIN_EMAIL
  CORDUM_USER_AUTH_ENABLED
  CORDUM_TENANT_ID
  CORDUM_ORG_ID
)

declare -A QUICKSTART_ENV_SOURCE=()
declare -A QUICKSTART_ENV_SOURCE_FILE=()

mark_env_source() {
  local key="$1"
  local source="$2"
  local file="${3:--}"
  QUICKSTART_ENV_SOURCE["${key}"]="${source}"
  QUICKSTART_ENV_SOURCE_FILE["${key}"]="${file}"
}

snapshot_shell_env_sources() {
  local var
  for var in "${TRACKED_ENV_VARS[@]}"; do
    if [[ -n "${!var:-}" ]]; then
      mark_env_source "${var}" "shell-override" "-"
    fi
  done
}

emit_env_source_banner() {
  local var source file
  for var in "${TRACKED_ENV_VARS[@]}"; do
    source="${QUICKSTART_ENV_SOURCE[${var}]:-unset}"
    file="${QUICKSTART_ENV_SOURCE_FILE[${var}]:--}"
    log "env.source key=${var} source=${source} file=${file}"
  done
}

persist_shell_secret_overrides() {
  local var
  for var in CORDUM_API_KEY REDIS_PASSWORD; do
    if [[ "${QUICKSTART_ENV_SOURCE[${var}]:-}" == "shell-override" && -n "${!var:-}" ]]; then
      persist_env_var "${var}" "${!var}"
    fi
  done
}

compose_exec_printenv() {
  local service="$1"
  local var="$2"
  if command -v timeout >/dev/null 2>&1; then
    MSYS_NO_PATHCONV=1 timeout 5s "${compose_cmd[@]}" exec -T "${service}" printenv "${var}"
  else
    MSYS_NO_PATHCONV=1 "${compose_cmd[@]}" exec -T "${service}" printenv "${var}"
  fi
}

detect_env_divergence() {
  local existing_containers
  existing_containers="$("${compose_cmd[@]}" ps -q 2>/dev/null | sed '/^[[:space:]]*$/d' || true)"
  [[ -n "${existing_containers}" ]] || return 0

  local services
  services="$("${compose_cmd[@]}" ps --services 2>/dev/null | sed '/^[[:space:]]*$/d' || true)"
  if [[ -z "${services}" ]]; then
    services="api-gateway scheduler workflow-engine safety-kernel context-engine dashboard redis nats"
  fi

  local strict_flag
  strict_flag="$(echo "${STRICT}" | tr '[:upper:]' '[:lower:]')"
  local action="warn"
  if [[ "${strict_flag}" == "1" || "${strict_flag}" == "true" || "${strict_flag}" == "yes" || "${CLEAN}" != "1" ]]; then
    action="abort"
  fi

  local diverged=0
  local service var expected observed
  for service in ${services}; do
    for var in "${TRACKED_ENV_VARS[@]}"; do
      expected="${!var:-}"
      [[ -n "${expected}" ]] || continue
      observed="$(compose_exec_printenv "${service}" "${var}" 2>/dev/null || true)"
      [[ -n "${observed}" ]] || continue
      if [[ "${observed}" != "${expected}" ]]; then
        diverged=1
        log "env.divergence key=${var} container=${service} action=${action}"
      fi
    done
  done

  if [[ "${diverged}" == "1" && "${action}" == "abort" ]]; then
    log "hint: run with --clean or docker compose up -d --force-recreate to rotate stale containers"
    exit 2
  fi
}

load_from_env_file() {
  local file="$1"
  [[ -f "${file}" ]] || return 0
  local var val
  for var in "${TRACKED_ENV_VARS[@]}"; do
    if [[ -z "${!var:-}" ]]; then
      # grep exits 1 when the var is absent from .env; with `set -e` +
      # `pipefail` that aborts the script during `local x=$(...)`. Tolerate
      # the missing-var case by catching the non-zero explicitly.
      val="$(grep -E "^${var}=" "${file}" 2>/dev/null | head -1 | cut -d= -f2- || true)"
      if [[ -n "${val}" ]]; then
        export "${var}=${val}"
        mark_env_source "${var}" "env-file" "${file}"
      fi
    fi
  done
}
snapshot_shell_env_sources
load_from_env_file ".env"

API_KEY=${CORDUM_API_KEY:-${API_KEY:-}}
if [[ -z "${CORDUM_API_KEY:-}" && -n "${API_KEY:-}" ]]; then
  mark_env_source CORDUM_API_KEY "shell-override" "-"
fi
export CORDUM_API_KEY="${API_KEY}"
REDIS_PASSWORD_VAL=${REDIS_PASSWORD:-}
export REDIS_PASSWORD="${REDIS_PASSWORD_VAL}"
persist_shell_secret_overrides
ORG_ID=${CORDUM_ORG_ID:-${CORDUM_TENANT_ID:-default}}
TENANT_ID=${CORDUM_TENANT_ID:-${ORG_ID}}
COMPOSE_FILES=${CORDUM_COMPOSE_FILES:-docker-compose.yml}
ALLOW_ENTERPRISE=${CORDUM_ALLOW_ENTERPRISE:-0}
# Dashboard login bootstrap — enable password auth by default and seed a known
# default, policy-compliant admin password (>=12 chars, with an uppercase
# letter, a digit, and a special character) when none is supplied, so the
# dashboard is loginable out of the box. Explicit env / .env values always win;
# the default is persisted to .env. CHANGE it before exposing the stack.
if [[ -z "${CORDUM_USER_AUTH_ENABLED:-}" ]]; then
  export CORDUM_USER_AUTH_ENABLED=true
  persist_env_var CORDUM_USER_AUTH_ENABLED true
  mark_env_source CORDUM_USER_AUTH_ENABLED "auto-generated" ".env"
fi
AUTH_ENABLED=${CORDUM_USER_AUTH_ENABLED:-false}
AUTH_FLAG_LC="$(printf '%s' "${AUTH_ENABLED}" | tr '[:upper:]' '[:lower:]')"
if [[ "${AUTH_FLAG_LC}" == "true" || "${AUTH_FLAG_LC}" == "1" || "${AUTH_FLAG_LC}" == "yes" ]]; then
  if [[ -z "${CORDUM_ADMIN_USERNAME:-}" ]]; then
    export CORDUM_ADMIN_USERNAME=admin
    persist_env_var CORDUM_ADMIN_USERNAME admin
  fi
  if [[ -z "${CORDUM_ADMIN_EMAIL:-}" ]]; then
    export CORDUM_ADMIN_EMAIL=admin@cordum.local
    persist_env_var CORDUM_ADMIN_EMAIL admin@cordum.local
  fi
  if [[ -z "${CORDUM_ADMIN_PASSWORD:-}" ]]; then
    export CORDUM_ADMIN_PASSWORD="${DEFAULT_ADMIN_PASSWORD}"
    persist_env_var CORDUM_ADMIN_PASSWORD "${CORDUM_ADMIN_PASSWORD}"
    mark_env_source CORDUM_ADMIN_PASSWORD "default" ".env"
    log "CORDUM_ADMIN_PASSWORD: set to the default dev password (CHANGE before exposing publicly); persisted to .env"
  fi
fi
ADMIN_PASSWORD=${CORDUM_ADMIN_PASSWORD:-}
ADMIN_EMAIL=${CORDUM_ADMIN_EMAIL:-}
export COMPOSE_HTTP_TIMEOUT=${COMPOSE_HTTP_TIMEOUT:-1800}
export DOCKER_CLIENT_TIMEOUT=${DOCKER_CLIENT_TIMEOUT:-1800}

preflight_deploy "${COMPOSE_FILES}" "${ALLOW_ENTERPRISE}" "${API_KEY}" "${AUTH_ENABLED}" "${ADMIN_PASSWORD}" "${ADMIN_EMAIL}"
API_KEY=${CORDUM_API_KEY:-${API_KEY:-}}
REDIS_PASSWORD_VAL=${REDIS_PASSWORD:-}
emit_env_source_banner
detect_env_divergence

# --- Capture pre-deploy baseline ---
if [[ -n "${ARTIFACTS_DIR}" ]]; then
  log "capturing pre-deploy baseline"
  capture_artifacts "${ARTIFACTS_DIR}" "predeploy"
fi

# --- TLS certificate generation ---
# Generate self-signed certs if they don't exist yet.
if [[ ! -f "./certs/ca/ca.crt" ]]; then
  if command -v cordumctl >/dev/null 2>&1; then
    log "generating TLS certificates"
    cordumctl generate-certs >/dev/null
  elif command -v go >/dev/null 2>&1; then
    log "generating TLS certificates"
    go run ./cmd/cordumctl generate-certs >/dev/null
  else
    log "warning: Go not found — cannot generate TLS certs"
    log "         install Go or run 'cordumctl generate-certs' manually"
  fi
fi

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
    capture_artifacts "${ARTIFACTS_DIR}" "deploy-failed"
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

# Credential summary for the banner — show the admin login when password auth
# is enabled (password masked; the full value lives in .env), otherwise point
# operators at the API key.
BANNER_AUTH_LC="$(printf '%s' "${AUTH_ENABLED:-false}" | tr '[:upper:]' '[:lower:]')"
if [[ "${BANNER_AUTH_LC}" == "true" || "${BANNER_AUTH_LC}" == "1" || "${BANNER_AUTH_LC}" == "yes" ]]; then
  if [[ "${ADMIN_PASSWORD}" == "${DEFAULT_ADMIN_PASSWORD}" ]]; then
    # Known dev default — safe to show in full so first login is frictionless.
    CREDS_SUMMARY="${CORDUM_ADMIN_USERNAME:-admin} / ${ADMIN_PASSWORD}  (default — CHANGE before prod)"
  else
    # Operator-supplied secret — mask it. It came from CORDUM_ADMIN_PASSWORD
    # (shell env or .env) and is NOT necessarily persisted to .env, so point
    # back at the source the operator set rather than promising it's in .env.
    CREDS_SUMMARY="${CORDUM_ADMIN_USERNAME:-admin} / •••••••• (the value you set in CORDUM_ADMIN_PASSWORD)"
  fi
else
  CREDS_SUMMARY="use your CORDUM_API_KEY (see .env)"
fi

echo ""
echo "  ┌─────────────────────────────────────────────────────────┐"
echo "  │  Cordum is running!                                     │"
echo "  ├─────────────────────────────────────────────────────────┤"
echo "  │  Dashboard:      http://localhost:8082                  │"
echo "  │  API Gateway:    ${API_BASE}                            │"
echo "  │  API Key:        ${API_KEY:0:8}... (see .env)           │"
echo "  ├─────────────────────────────────────────────────────────┤"
echo "  │  Login:          ${CREDS_SUMMARY}"
echo "  │  (auto-generated — rotate before exposing publicly)     │"
echo "  ├─────────────────────────────────────────────────────────┤"
echo "  │  Ports:                                                 │"
echo "  │    8082  Dashboard                                      │"
echo "  │    8081  API Gateway (HTTPS)                            │"
echo "  │    9080  gRPC Gateway                                   │"
echo "  │    4222  NATS                                           │"
echo "  │    6379  Redis                                          │"
echo "  │    9092  Gateway Metrics                                │"
echo "  │    9093  Workflow Engine Health                          │"
echo "  │   50051  Safety Kernel (gRPC)                           │"
echo "  │   50400  Context Engine (gRPC)                          │"
echo "  ├─────────────────────────────────────────────────────────┤"
echo "  │  Stop:  docker compose down                             │"
echo "  │  Logs:  docker compose logs -f api-gateway              │"
echo "  │  Reset: docker compose down -v                          │"
echo "  └─────────────────────────────────────────────────────────┘"
echo ""

# --- Capture artifacts ---
if [[ -n "${ARTIFACTS_DIR}" ]]; then
  capture_artifacts "${ARTIFACTS_DIR}" "postdeploy"
fi

# --- Smoke test ---
if [[ "${SKIP_SMOKE}" == "1" ]]; then
  log "skipping smoke test (--skip-smoke)"
else
  echo ""
  log "running smoke test"
  CORDUM_API_KEY="${API_KEY}" CORDUM_ORG_ID="${ORG_ID}" CORDUM_TENANT_ID="${TENANT_ID}" ./tools/scripts/platform_smoke.sh
fi

# --- Doctor (install verification) ---
# Optional final check — runs only when `cordumctl` is on PATH so fresh
# clones that haven't built the CLI yet still finish quickstart cleanly.
# --skip-smoke implies --skip-doctor so users who asked to skip
# verification get a uniformly verification-free run.
if [[ "${SKIP_SMOKE}" == "1" || "${SKIP_DOCTOR}" == "1" ]]; then
  log "skipping doctor verification"
elif ! cordumctl_cmd="$(resolve_cordumctl)"; then
  log "cordumctl not on PATH — skipping doctor (build it with: make build SERVICE=cordumctl)"
else
  echo ""
  log "running cordumctl doctor (post-install verification)"
  doctor_env=(CORDUM_API_KEY="${API_KEY}" CORDUM_TENANT_ID="${TENANT_ID}")
  if [[ -n "${TLS_CA}" ]]; then
    doctor_env+=(CORDUM_GATEWAY="https://127.0.0.1:8081")
    env "${doctor_env[@]}" "${cordumctl_cmd}" doctor --timeout 30 --cacert "${TLS_CA}" || \
      log "cordumctl doctor reported issues — see docs/troubleshooting/install.md"
  else
    doctor_env+=(CORDUM_GATEWAY="http://127.0.0.1:8081")
    env "${doctor_env[@]}" "${cordumctl_cmd}" doctor --timeout 30 || \
      log "cordumctl doctor reported issues — see docs/troubleshooting/install.md"
  fi
fi

echo ""
log "try:"
if [[ -n "${TLS_CA}" ]]; then
  echo "curl -sS --cacert ./certs/ca/ca.crt ${API_BASE}/api/v1/status -H \"X-API-Key: \$CORDUM_API_KEY\" -H \"X-Tenant-ID: ${TENANT_ID}\" | jq"
else
  echo "curl -sS ${API_BASE}/api/v1/status -H \"X-API-Key: \$CORDUM_API_KEY\" -H \"X-Tenant-ID: ${TENANT_ID}\" | jq"
fi

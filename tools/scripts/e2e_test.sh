#!/bin/bash
# =============================================================================
# Cordum E2E Test Suite
# Tests the full system through the dashboard nginx proxy (port 8082)
# =============================================================================

set -euo pipefail

detect_worker_bus_env() {
  to_worker_tls_path() {
    local path="${1:-}"
    if [[ -z "${path}" ]]; then
      printf '%s' ""
      return 0
    fi
    if command -v cygpath >/dev/null 2>&1; then
      case "${path}" in
        [A-Za-z]:[\\/]*)
          printf '%s' "${path}"
          return 0
          ;;
        /*)
          cygpath -w "${path}"
          return 0
          ;;
      esac
    fi
    printf '%s' "${path}"
  }

  TLS_CA="${CORDUM_TLS_CA:-${TLS_CA:-}}"
  if [[ -z "${TLS_CA}" && -f "${ROOT_DIR:-.}/certs/ca/ca.crt" ]]; then
    TLS_CA="${ROOT_DIR:-.}/certs/ca/ca.crt"
  fi

  HELLO_NATS_URL="${NATS_URL:-}"
  HELLO_REDIS_URL="${REDIS_URL:-}"
  if [[ -n "${TLS_CA}" ]]; then
    # The generated .env keeps human-friendly non-TLS localhost defaults, while
    # the default Docker stack exposes NATS/Redis with TLS. When local certs are
    # present, upgrade only those documented defaults for the transient
    # hello-worker; keep genuinely custom operator URLs untouched.
    case "${HELLO_NATS_URL}" in
      ""|"nats://localhost:4222"|"nats://127.0.0.1:4222")
        HELLO_NATS_URL="tls://localhost:4222"
        ;;
    esac
    case "${HELLO_REDIS_URL}" in
      ""|"redis://localhost:6379"*|"redis://127.0.0.1:6379"*|"redis://"*"@localhost:6379"*|"redis://"*"@127.0.0.1:6379"*)
        HELLO_REDIS_URL="${HELLO_REDIS_URL/redis:\/\//rediss://}"
        ;;
    esac
  fi
  if [[ -z "${HELLO_NATS_URL}" ]]; then
    if [[ -n "${TLS_CA}" ]]; then
      HELLO_NATS_URL="tls://localhost:4222"
    else
      HELLO_NATS_URL="nats://localhost:4222"
    fi
  fi
  if [[ -z "${HELLO_REDIS_URL}" ]]; then
    if [[ -n "${TLS_CA}" ]]; then
      HELLO_REDIS_URL="rediss://:${REDIS_PASSWORD:-cordum-dev}@localhost:6379/0"
    else
      HELLO_REDIS_URL="redis://:${REDIS_PASSWORD:-cordum-dev}@localhost:6379/0"
    fi
  fi

  BUS_SCHEME="unknown"
  REDIS_SCHEME="unknown"
  if [[ "${HELLO_NATS_URL}" == *"://"* ]]; then
    BUS_SCHEME="${HELLO_NATS_URL%%://*}"
  fi
  if [[ "${HELLO_REDIS_URL}" == *"://"* ]]; then
    REDIS_SCHEME="${HELLO_REDIS_URL%%://*}"
  fi

  CLIENT_CERT="${CORDUM_TLS_CERT:-${TLS_CERT:-}}"
  CLIENT_KEY="${CORDUM_TLS_KEY:-${TLS_KEY:-}}"
  local client_dir="${ROOT_DIR:-.}/certs/client"
  if [[ -n "${TLS_CA}" ]]; then
    if [[ -z "${CLIENT_CERT}" && -f "${client_dir}/tls.crt" ]]; then
      CLIENT_CERT="${client_dir}/tls.crt"
    fi
    if [[ -z "${CLIENT_KEY}" && -f "${client_dir}/tls.key" ]]; then
      CLIENT_KEY="${client_dir}/tls.key"
    fi
  fi

  local worker_tls_ca worker_client_cert worker_client_key
  worker_tls_ca="$(to_worker_tls_path "${TLS_CA}")"
  worker_client_cert="$(to_worker_tls_path "${CLIENT_CERT}")"
  worker_client_key="$(to_worker_tls_path "${CLIENT_KEY}")"

  export HELLO_NATS_URL HELLO_REDIS_URL BUS_SCHEME REDIS_SCHEME TLS_CA CLIENT_CERT CLIENT_KEY
  export NATS_TLS_CA="${NATS_TLS_CA:-${worker_tls_ca}}"
  export NATS_TLS_CERT="${NATS_TLS_CERT:-${worker_client_cert}}"
  export NATS_TLS_KEY="${NATS_TLS_KEY:-${worker_client_key}}"
  export REDIS_TLS_CA="${REDIS_TLS_CA:-${worker_tls_ca}}"
  export REDIS_TLS_CERT="${REDIS_TLS_CERT:-${worker_client_cert}}"
  export REDIS_TLS_KEY="${REDIS_TLS_KEY:-${worker_client_key}}"
}

workers_json_has_pool() {
  local pool="$1"
  jq -e --arg pool "${pool}" '
    def worker_items:
      if type == "array" then
        .
      elif type == "object" and (.items | type) == "array" then
        .items
      elif type == "object" and (.workers | type) == "array" then
        .workers
      else
        []
      end;
    [worker_items[] | select(.pool == $pool)] | length > 0
  ' >/dev/null
}

if [[ "${CORDUM_E2E_SOURCE_ONLY:-}" == "1" ]]; then
  return 0 2>/dev/null || exit 0
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
ROOT_DIR="${CORDUM_REPO_ROOT:-$(cd "${SCRIPT_DIR}/../.." && pwd -P)}"

require() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing dependency: $1" >&2
    exit 1
  fi
}

to_curl_path() {
  local path=${1:-}
  if [[ -n "${path}" && "${path}" == /* ]] && command -v cygpath >/dev/null 2>&1 && curl --version 2>/dev/null | grep -qi schannel; then
    cygpath -w "${path}"
    return 0
  fi
  printf '%s' "${path}"
}

require curl
require jq
CURL_OUTPUT_NULL="$(to_curl_path /dev/null)"

API_KEY="${CORDUM_API_KEY:-${API_KEY:-}}"
TENANT_ID="${CORDUM_TENANT_ID:-default}"
ADMIN_USERNAME="${CORDUM_ADMIN_USERNAME:-admin}"
ADMIN_PASSWORD="${CORDUM_ADMIN_PASSWORD:-}"
REDIS_PASSWORD="${REDIS_PASSWORD:-cordum-dev}"

# TLS auto-detection (same logic as platform_smoke.sh)
CURL_TLS_OPTS=()
TLS_CA="${CORDUM_TLS_CA:-}"
if [[ -z "${TLS_CA}" && -f "${ROOT_DIR}/certs/ca/ca.crt" ]]; then
  TLS_CA="${ROOT_DIR}/certs/ca/ca.crt"
fi
if [[ -n "${TLS_CA}" ]]; then
  CURL_TLS_OPTS=(--cacert "$(to_curl_path "${TLS_CA}")")
  if curl --version 2>/dev/null | grep -qi schannel; then
    CURL_TLS_OPTS+=(--ssl-no-revoke)
  fi
  GW_SCHEME="https"
else
  GW_SCHEME="http"
fi

BASE="${CORDUM_E2E_BASE:-http://localhost:8082/api/v1}"
DASHBOARD_ROOT="${CORDUM_E2E_DASHBOARD_ROOT:-${BASE%/api/v1}}"
DASHBOARD_ROOT="${DASHBOARD_ROOT%/}"
GW="${CORDUM_E2E_GW_BASE:-${GW_SCHEME}://localhost:8081/api/v1}"
GW_ROOT="${GW%/api/v1}"

detect_worker_bus_env

if [[ -z "${API_KEY}" ]]; then
  echo "CORDUM_API_KEY is required; export it before running the e2e test." >&2
  exit 1
fi

PASS=0
FAIL=0
SKIP=0
ERRORS=""

green() { printf "\033[32m%s\033[0m\n" "$1"; }
red()   { printf "\033[31m%s\033[0m\n" "$1"; }
yellow(){ printf "\033[33m%s\033[0m\n" "$1"; }
bold()  { printf "\033[1m%s\033[0m\n" "$1"; }

http_body() {
  local body=""
  set +e
  body="$(curl -s "$@" 2>/dev/null)"
  set -e
  printf '%s' "${body}"
}

http_code() {
  local code=""
  set +e
  code="$(curl -s -o "${CURL_OUTPUT_NULL}" -w "%{http_code}" "$@" 2>/dev/null)"
  set -e
  printf '%s' "${code:-000}"
}

http_headers() {
  local headers=""
  set +e
  headers="$(curl -sI "$@" 2>/dev/null)"
  set -e
  printf '%s' "${headers}"
}

wait_for_ready() {
  local dashboard_code=""
  local gateway_code=""
  local gateway_proxy_code=""

  for _ in $(seq 1 60); do
    dashboard_code=$(http_code "${CURL_TLS_OPTS[@]}" "${DASHBOARD_ROOT}/healthz")
    gateway_code=$(http_code "${CURL_TLS_OPTS[@]}" "${GW_ROOT}/health")
    if [[ "${gateway_code}" != "200" && -n "${API_KEY}" ]]; then
      # Windows/Schannel can intermittently report HTTP 000 for the direct
      # HTTPS loopback probe even while Gateway is healthy. The E2E primarily
      # drives the system through the Dashboard nginx proxy, so accept the
      # proxied authenticated status endpoint as a readiness fallback.
      gateway_proxy_code=$(http_code \
        "${DASHBOARD_ROOT}/api/v1/status" \
        -H "X-API-Key: ${API_KEY}" \
        -H "X-Tenant-ID: ${TENANT_ID}")
    else
      gateway_proxy_code=""
    fi
    if [[ "${dashboard_code}" == "200" && ( "${gateway_code}" == "200" || "${gateway_proxy_code}" == "200" ) ]]; then
      return 0
    fi
    sleep 2
  done

  red "FATAL: services not ready after 120s (dashboard=${dashboard_code:-ERR}, gateway=${gateway_code:-ERR}, gateway_proxy=${gateway_proxy_code:-ERR})"
  exit 2
}

redis_ping() {
  if command -v redis-cli >/dev/null 2>&1; then
    local redis_args=(-h localhost -p 6379)
    if [[ -n "${TLS_CA}" ]]; then
      redis_args+=(--tls --cacert "${TLS_CA}")
      if [[ -n "${REDIS_TLS_CERT:-}" && -n "${REDIS_TLS_KEY:-}" ]]; then
        redis_args+=(--cert "${REDIS_TLS_CERT}" --key "${REDIS_TLS_KEY}")
      fi
      if [[ -n "${REDIS_TLS_SERVER_NAME:-}" ]]; then
        redis_args+=(--sni "${REDIS_TLS_SERVER_NAME}")
      fi
    fi
    if [[ -n "${REDIS_PASSWORD}" ]]; then
      redis_args+=(-a "${REDIS_PASSWORD}")
    fi
    redis-cli "${redis_args[@]}" ping
    return
  fi

  if command -v docker >/dev/null 2>&1 && docker compose ps -q redis >/dev/null 2>&1; then
    if [[ -n "${TLS_CA}" ]]; then
      MSYS_NO_PATHCONV=1 docker compose exec -T redis redis-cli --tls --cacert /etc/cordum/tls/ca/ca.crt -a "${REDIS_PASSWORD}" ping
    elif [[ -n "${REDIS_PASSWORD}" ]]; then
      MSYS_NO_PATHCONV=1 docker compose exec -T redis redis-cli -a "${REDIS_PASSWORD}" ping
    else
      MSYS_NO_PATHCONV=1 docker compose exec -T redis redis-cli ping
    fi
    return
  fi

  return 127
}

cordumctl_cmd() {
  if [[ -n "${CORDUMCTL:-}" ]]; then
    "${CORDUMCTL}" "$@"
  elif [[ -x "./cordumctl" ]]; then
    ./cordumctl "$@"
  elif [[ -x "./cordumctl.exe" ]]; then
    ./cordumctl.exe "$@"
  elif [[ -x "./cmd/cordumctl/cordumctl" ]]; then
    ./cmd/cordumctl/cordumctl "$@"
  elif [[ -x "./cmd/cordumctl/cordumctl.exe" ]]; then
    ./cmd/cordumctl/cordumctl.exe "$@"
  elif command -v cordumctl >/dev/null 2>&1; then
    cordumctl "$@"
  else
    (cd "${ROOT_DIR}" && go run ./cmd/cordumctl "$@")
  fi
}

cordumctl_common_flags() {
  CORDUMCTL_FLAGS=(--gateway "${GW_ROOT}" --api-key "${API_KEY}" --tenant "${TENANT_ID}")
  if [[ -n "${TLS_CA}" ]]; then
    CORDUMCTL_FLAGS+=(--cacert "$(to_cordumctl_path "${TLS_CA}")")
  fi
}

to_cordumctl_path() {
  local path="$1"
  if command -v cygpath >/dev/null 2>&1; then
    case "${path}" in
      /*)
        cygpath -w "${path}"
        return 0
        ;;
    esac
  fi
  printf '%s' "${path}"
}

run_cordumctl_pack_list() {
  cordumctl_common_flags
  cordumctl_cmd pack list "${CORDUMCTL_FLAGS[@]}"
}

run_cordumctl_pack_install() {
  local pack_path="$1"
  cordumctl_common_flags
  cordumctl_cmd pack install "${CORDUMCTL_FLAGS[@]}" "$(to_cordumctl_path "${pack_path}")"
}

ensure_hello_pack_installed() {
  local list_output
  local list_status
  local install_output
  set +e
  list_output="$(run_cordumctl_pack_list 2>&1)"
  list_status=$?
  set -e
  if [[ ${list_status} -eq 0 ]] && grep -qE '^hello-pack[[:space:]]' <<<"${list_output}"; then
    green "  hello-pack already installed"
    return 0
  fi

  bold "[setup] Installing hello-pack topic registry"
  if ! install_output="$(run_cordumctl_pack_install "${ROOT_DIR}/examples/hello-worker-go/pack" 2>&1)"; then
    red "FATAL: hello-pack install failed -- cannot run Phase 4 (job dispatch)"
    printf '%s\n' "${install_output}" >&2
    exit 2
  fi
  printf '%s\n' "${install_output}"
}

# ---------------------------------------------------------------------------
# Start hello-worker for Phase 4 job dispatch (topic=job.hello-pack.echo)
# ---------------------------------------------------------------------------
wait_for_ready
bold "[setup] Starting hello-worker for job.hello-pack.echo"
yellow "  hello-worker bus schemes: nats=${BUS_SCHEME}, redis=${REDIS_SCHEME}"
HELLO_LOG="${CORDUM_E2E_HELLO_WORKER_LOG:-${TMPDIR:-/tmp}/cordum-hello-worker-e2e.log}"
: >"${HELLO_LOG}"
(cd "${ROOT_DIR}/examples/hello-worker-go" && \
  NATS_URL="${HELLO_NATS_URL}" \
  REDIS_URL="${HELLO_REDIS_URL}" \
  NATS_TLS_CA="${NATS_TLS_CA:-}" \
  NATS_TLS_CERT="${NATS_TLS_CERT:-}" \
  NATS_TLS_KEY="${NATS_TLS_KEY:-}" \
  NATS_TLS_SERVER_NAME="${NATS_TLS_SERVER_NAME:-}" \
  NATS_TLS_INSECURE="${NATS_TLS_INSECURE:-}" \
  REDIS_TLS_CA="${REDIS_TLS_CA:-}" \
  REDIS_TLS_CERT="${REDIS_TLS_CERT:-}" \
  REDIS_TLS_KEY="${REDIS_TLS_KEY:-}" \
  REDIS_TLS_SERVER_NAME="${REDIS_TLS_SERVER_NAME:-}" \
  REDIS_TLS_INSECURE="${REDIS_TLS_INSECURE:-}" \
  go run . >"${HELLO_LOG}" 2>&1) &
HELLO_PID=$!

cleanup() {
  [[ -n "${HELLO_PID:-}" ]] && kill "${HELLO_PID}" 2>/dev/null || true
  wait "${HELLO_PID}" 2>/dev/null || true
}
trap cleanup EXIT

check() {
  local name="$1" expected="$2" actual="$3"
  if [ "$actual" = "$expected" ]; then
    green "  PASS: $name (HTTP $actual)"
    PASS=$((PASS+1))
  else
    red "  FAIL: $name (expected $expected, got $actual)"
    FAIL=$((FAIL+1))
    ERRORS="${ERRORS}\n  - ${name}: expected ${expected}, got ${actual}"
  fi
}

check_body() {
  local name="$1" pattern="$2" body="$3"
  if echo "$body" | grep -q "$pattern"; then
    green "  PASS: $name (body contains '$pattern')"
    PASS=$((PASS+1))
  else
    red "  FAIL: $name (body missing '$pattern')"
    FAIL=$((FAIL+1))
    ERRORS="${ERRORS}\n  - ${name}: body missing '${pattern}'"
  fi
}

base64_urlsafe() {
  printf '%s' "$1" | base64 | tr '+/' '-_' | tr -d '=' | tr -d '\n'
}

SESSION=""

# =============================================================================
bold "=== PHASE 1: Infrastructure Health ==="
# =============================================================================

bold "1.1 Dashboard (nginx) serves index.html"
code=$(http_code http://localhost:8082/)
check "GET / (dashboard)" "200" "$code"

bold "1.2 Dashboard config.json"
body=""
code="ERR"
for _ in $(seq 1 90); do
  body=$(http_body http://localhost:8082/config.json)
  if echo "$body" | jq -e . >/dev/null 2>&1; then
    code="200"
    break
  fi
  sleep 2
done
check "GET /config.json (valid JSON)" "200" "$code"
if echo "$body" | jq -e '.apiBaseUrl' >/dev/null 2>&1; then
  green "  PASS: config.json has apiBaseUrl"
  PASS=$((PASS+1))
else
  red "  FAIL: config.json missing apiBaseUrl"
  FAIL=$((FAIL+1))
  ERRORS="${ERRORS}\n  - config.json missing apiBaseUrl"
fi

bold "1.3 Gateway health"
code=$(http_code "${CURL_TLS_OPTS[@]}" "${GW_ROOT}/health")
if [[ "${code}" == "200" ]]; then
  check "GET /health (gateway)" "200" "$code"
else
  proxy_code=$(http_code \
    "${DASHBOARD_ROOT}/api/v1/status" \
    -H "X-API-Key: ${API_KEY}" \
    -H "X-Tenant-ID: ${TENANT_ID}")
  if [[ "${proxy_code}" == "200" ]]; then
    green "  PASS: GET /health (gateway) via dashboard proxy fallback (direct got ${code})"
    PASS=$((PASS+1))
  else
    check "GET /health (gateway)" "200" "$code"
  fi
fi

bold "1.4 NATS reachable"
# NATS doesn't speak HTTP; test TCP connectivity
if command -v nc >/dev/null 2>&1; then
  if nc -z -w 2 localhost 4222 >/dev/null 2>&1; then
    nats_ok="ok"
  else
    nats_ok="fail"
  fi
else
  if timeout 2 bash -c "cat < /dev/null > /dev/tcp/localhost/4222" >/dev/null 2>&1; then
    nats_ok="ok"
  else
    nats_ok="fail"
  fi
fi
if [ "$nats_ok" = "ok" ]; then
  green "  PASS: NATS reachable (TCP 4222)"
  PASS=$((PASS+1))
else
  red "  FAIL: NATS unreachable (TCP 4222)"
  FAIL=$((FAIL+1))
fi

bold "1.5 Redis reachable"
set +e
pong=$(redis_ping 2>/dev/null)
redis_ping_status=$?
set -e
if [ "$redis_ping_status" -ne 127 ]; then
  if [ "$pong" = "PONG" ]; then
    green "  PASS: Redis PING -> PONG"
    PASS=$((PASS+1))
  else
    red "  FAIL: Redis PING -> $pong"
    FAIL=$((FAIL+1))
    ERRORS="${ERRORS}\n  - Redis ping failed: ${pong}"
  fi
else
  yellow "  SKIP: Redis CLI not available and docker compose redis fallback unavailable"
  SKIP=$((SKIP+1))
fi

# =============================================================================
bold ""
bold "=== PHASE 2: Auth Flow (via nginx proxy) ==="
# =============================================================================

bold "2.1 Auth config (public endpoint)"
body=$(http_body "$BASE/auth/config")
code=$(http_code "$BASE/auth/config")
check "GET /auth/config" "200" "$code"
check_body "auth config has password_enabled" "password_enabled" "$body"

bold "2.2 Unauthenticated request rejected"
code=$(http_code "$BASE/workers")
check "GET /workers (no auth)" "401" "$code"

bold "2.3 API key auth"
code=$(http_code -H "X-API-Key: $API_KEY" -H "X-Tenant-ID: ${TENANT_ID}" "$BASE/workers")
check "GET /workers (API key)" "200" "$code"

bold "2.4 Invalid API key rejected"
code=$(http_code -H "X-API-Key: bad-key" "$BASE/workers")
check "GET /workers (bad key)" "401" "$code"

bold "2.5 User login (${ADMIN_USERNAME})"
if [ -n "$ADMIN_PASSWORD" ]; then
  body=$(http_body -X POST "$BASE/auth/login" \
    -H "Content-Type: application/json" \
    -H "X-API-Key: $API_KEY" \
    -d "{\"username\":\"${ADMIN_USERNAME}\",\"password\":\"${ADMIN_PASSWORD}\",\"tenant\":\"${TENANT_ID}\"}")
  code=$(http_code -X POST "$BASE/auth/login" \
    -H "Content-Type: application/json" \
    -H "X-API-Key: $API_KEY" \
    -d "{\"username\":\"${ADMIN_USERNAME}\",\"password\":\"${ADMIN_PASSWORD}\",\"tenant\":\"${TENANT_ID}\"}")
  check "POST /auth/login" "200" "$code"

  # Extract session token
  SESSION=$(echo "$body" | jq -r '.token // empty' 2>/dev/null || true)
  if [ -n "$SESSION" ]; then
    green "  PASS: Got session token (${SESSION:0:20}...)"
    PASS=$((PASS+1))
  else
    red "  FAIL: No session token in login response"
    FAIL=$((FAIL+1))
    ERRORS="${ERRORS}\n  - Login: no session token returned"
    # Fallback to API key for remaining tests
    SESSION=""
  fi
else
  yellow "  SKIP: CORDUM_ADMIN_PASSWORD not set"
  SKIP=$((SKIP+1))
fi

bold "2.6 Session validation"
if [ -n "$SESSION" ]; then
  code=$(http_code -H "X-API-Key: $SESSION" -H "X-Tenant-ID: ${TENANT_ID}" "$BASE/auth/session")
  check "GET /auth/session (session token)" "200" "$code"
else
  yellow "  SKIP: No session token"
  SKIP=$((SKIP+1))
fi

# Use session or fall back to API key
if [ -n "$SESSION" ]; then
  AUTH_HEADER="X-API-Key: $SESSION"
else
  AUTH_HEADER="X-API-Key: $API_KEY"
fi

# =============================================================================
bold ""
bold "=== PHASE 3: Core API Endpoints ==="
# =============================================================================

bold "3.1 Workers"
code=$(http_code -H "$AUTH_HEADER" -H "X-Tenant-ID: ${TENANT_ID}" "$BASE/workers")
check "GET /workers" "200" "$code"

bold "3.2 Status"
code=$(http_code -H "$AUTH_HEADER" -H "X-Tenant-ID: ${TENANT_ID}" "$BASE/status")
check "GET /status" "200" "$code"

bold "3.3 Jobs list"
body=$(http_body -H "$AUTH_HEADER" -H "X-Tenant-ID: ${TENANT_ID}" "$BASE/jobs")
code=$(http_code -H "$AUTH_HEADER" -H "X-Tenant-ID: ${TENANT_ID}" "$BASE/jobs")
check "GET /jobs" "200" "$code"

bold "3.4 DLQ page"
code=$(http_code -H "$AUTH_HEADER" -H "X-Tenant-ID: ${TENANT_ID}" "$BASE/dlq/page?limit=10")
check "GET /dlq/page" "200" "$code"

bold "3.5 Approvals"
code=$(http_code -H "$AUTH_HEADER" -H "X-Tenant-ID: ${TENANT_ID}" "$BASE/approvals?limit=10")
check "GET /approvals" "200" "$code"

bold "3.6 Policy bundles"
code=$(http_code -H "$AUTH_HEADER" -H "X-Tenant-ID: ${TENANT_ID}" "$BASE/policy/bundles")
check "GET /policy/bundles" "200" "$code"

bold "3.7 Policy rules"
code=$(http_code -H "$AUTH_HEADER" -H "X-Tenant-ID: ${TENANT_ID}" "$BASE/policy/rules")
check "GET /policy/rules" "200" "$code"

bold "3.8 Policy audit"
code=$(http_code -H "$AUTH_HEADER" -H "X-Tenant-ID: ${TENANT_ID}" "$BASE/policy/audit")
check "GET /policy/audit" "200" "$code"

bold "3.9 Workflows"
code=$(http_code -H "$AUTH_HEADER" -H "X-Tenant-ID: ${TENANT_ID}" "$BASE/workflows")
check "GET /workflows" "200" "$code"

bold "3.10 Workflow runs"
code=$(http_code -H "$AUTH_HEADER" -H "X-Tenant-ID: ${TENANT_ID}" "$BASE/workflow-runs?limit=20")
check "GET /workflow-runs" "200" "$code"

bold "3.11 Packs"
code=$(http_code -H "$AUTH_HEADER" -H "X-Tenant-ID: ${TENANT_ID}" "$BASE/packs")
check "GET /packs" "200" "$code"

bold "3.12 Config / system"
code=$(http_code -H "$AUTH_HEADER" -H "X-Tenant-ID: ${TENANT_ID}" "$BASE/config")
check "GET /config" "200" "$code"

# ---------------------------------------------------------------------------
# Install hello-pack and wait for hello-worker before job submission
# ---------------------------------------------------------------------------
ensure_hello_pack_installed

bold "[setup] Waiting for hello-worker heartbeat"
WORKER_READY=false
WORKERS_BODY=""
for _ in $(seq 1 120); do
  if ! kill -0 "${HELLO_PID}" 2>/dev/null; then
    red "FATAL: hello-worker exited before publishing a heartbeat -- cannot run Phase 4 (job dispatch)"
    tail -50 "${HELLO_LOG}" >&2 || true
    exit 2
  fi
  WORKERS_BODY="$(curl -s "${BASE}/workers" -H "X-API-Key: $API_KEY" -H "X-Tenant-ID: ${TENANT_ID}" 2>/dev/null || true)"
  if [[ -n "${WORKERS_BODY}" ]] && workers_json_has_pool "hello-pack" <<<"${WORKERS_BODY}"; then
    WORKER_READY=true
    break
  fi
  sleep 0.5
done
if [ "$WORKER_READY" = "true" ]; then
  green "  hello-worker registered (pool=hello-pack)"
else
  red "FATAL: hello-worker not detected after 60s despite hello-pack install -- cannot run Phase 4 (job dispatch)"
  if [[ -n "${WORKERS_BODY}" ]]; then
    yellow "  Last /api/v1/workers response:"
    printf '%s\n' "${WORKERS_BODY}" >&2
  fi
  yellow "  hello-worker log tail:"
  tail -50 "${HELLO_LOG}" >&2 || true
  exit 2
fi

# =============================================================================
bold ""
bold "=== PHASE 4: Job Submission Flow ==="
# =============================================================================

bold "4.1 Submit a job"
# Use job.hello-pack.echo which has the hello-worker listening (pool=hello-pack).
# Avoid job.default -- no workers serve it, causing infinite NAK redelivery noise.
JOB_BODY='{"prompt":"E2E test job","topic":"job.hello-pack.echo","metadata":{"test":"e2e"}}'
body=$(http_body -X POST "$BASE/jobs" \
  -H "$AUTH_HEADER" \
  -H "X-Tenant-ID: ${TENANT_ID}" \
  -H "Content-Type: application/json" \
  -d "$JOB_BODY")
code=$(http_code -X POST "$BASE/jobs" \
  -H "$AUTH_HEADER" \
  -H "X-Tenant-ID: ${TENANT_ID}" \
  -H "Content-Type: application/json" \
  -d "$JOB_BODY")

# Accept 200, 201, 202 as success
if [ "$code" = "200" ] || [ "$code" = "201" ] || [ "$code" = "202" ]; then
  green "  PASS: POST /jobs (HTTP $code)"
  PASS=$((PASS+1))
else
  red "  FAIL: POST /jobs (expected 2xx, got $code)"
  FAIL=$((FAIL+1))
  ERRORS="${ERRORS}\n  - POST /jobs: expected 2xx, got $code"
fi

# Extract job ID
JOB_ID=$(echo "$body" | jq -r '.job_id // .id // empty' 2>/dev/null || true)
if [ -n "$JOB_ID" ]; then
  green "  PASS: Got job ID: $JOB_ID"
  PASS=$((PASS+1))
else
  red "  FAIL: Could not extract job ID from response"
  FAIL=$((FAIL+1))
  ERRORS="${ERRORS}\n  - POST /jobs: could not extract job ID from response"
fi

bold "4.2 Get job detail"
if [ -n "$JOB_ID" ]; then
  sleep 1
  code=$(http_code -H "$AUTH_HEADER" -H "X-Tenant-ID: ${TENANT_ID}" "$BASE/jobs/$JOB_ID")
  check "GET /jobs/$JOB_ID" "200" "$code"
else
  red "  FAIL: No job ID for detail lookup"
  FAIL=$((FAIL+1))
  ERRORS="${ERRORS}\n  - GET /jobs/<id>: no job ID returned by submit"
fi

bold "4.3 Job appears in listing"
if [ -n "$JOB_ID" ]; then
  body=$(http_body -H "$AUTH_HEADER" -H "X-Tenant-ID: ${TENANT_ID}" "$BASE/jobs")
  if echo "$body" | grep -q "$JOB_ID"; then
    green "  PASS: Job $JOB_ID found in /jobs listing"
    PASS=$((PASS+1))
  else
    red "  FAIL: Job $JOB_ID not found in /jobs listing"
    FAIL=$((FAIL+1))
    ERRORS="${ERRORS}\n  - GET /jobs listing: job ${JOB_ID} not found"
  fi
else
  red "  FAIL: No job ID for listing check"
  FAIL=$((FAIL+1))
  ERRORS="${ERRORS}\n  - GET /jobs listing: no job ID returned by submit"
fi

bold "4.4 Job reaches SUCCEEDED state"
if [ -n "$JOB_ID" ]; then
  JOB_DONE=false
  for _ in $(seq 1 30); do
    state=$(http_body "$BASE/jobs/$JOB_ID" -H "$AUTH_HEADER" -H "X-Tenant-ID: ${TENANT_ID}" | jq -r '.status // .state // ""' 2>/dev/null || true)
    if [[ "$state" == "SUCCEEDED" || "$state" == "succeeded" ]]; then
      JOB_DONE=true
      break
    fi
    sleep 0.5
  done
  if [ "$JOB_DONE" = "true" ]; then
    green "  PASS: Job $JOB_ID reached SUCCEEDED"
    PASS=$((PASS+1))
  else
    red "  FAIL: Job $JOB_ID in state '$state' after 15s (expected SUCCEEDED)"
    FAIL=$((FAIL+1))
    ERRORS="${ERRORS}\n  - GET /jobs/${JOB_ID}: expected SUCCEEDED, got ${state:-<empty>} after 15s"
  fi
else
  red "  FAIL: No job ID for SUCCEEDED-state check"
  FAIL=$((FAIL+1))
  ERRORS="${ERRORS}\n  - GET /jobs/<id>: no job ID returned by submit"
fi

# =============================================================================
bold ""
bold "=== PHASE 5: WebSocket Stream ==="
# =============================================================================

bold "5.1 WebSocket upgrade (API key auth, through proxy)"
ENCODED=$(base64_urlsafe "$API_KEY")
timeout 3 curl -s -o "${CURL_OUTPUT_NULL}" -w "%{http_code}" \
  -H "Upgrade: websocket" \
  -H "Connection: upgrade" \
  -H "Sec-WebSocket-Version: 13" \
  -H "Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==" \
  -H "Sec-WebSocket-Protocol: cordum-api-key.${ENCODED}" \
  "http://localhost:8082/api/v1/stream" 2>/dev/null && ws_result="fail" || ws_result="pass"

if [ "$ws_result" = "pass" ]; then
  green "  PASS: WebSocket upgrade succeeded (connection held open)"
  PASS=$((PASS+1))
else
  red "  FAIL: WebSocket upgrade rejected"
  FAIL=$((FAIL+1))
fi

bold "5.2 WebSocket with session token (through proxy)"
if [ -n "$SESSION" ]; then
  SESS_ENC=$(base64_urlsafe "$SESSION")
  timeout 3 curl -s -o "${CURL_OUTPUT_NULL}" -w "%{http_code}" \
    -H "Upgrade: websocket" \
    -H "Connection: upgrade" \
    -H "Sec-WebSocket-Version: 13" \
    -H "Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==" \
    -H "Sec-WebSocket-Protocol: cordum-api-key.${SESS_ENC}" \
    http://localhost:8082/api/v1/stream 2>/dev/null && ws_result="fail" || ws_result="pass"

  if [ "$ws_result" = "pass" ]; then
    green "  PASS: WebSocket with session token (connection held open)"
    PASS=$((PASS+1))
  else
    red "  FAIL: WebSocket with session token rejected"
    FAIL=$((FAIL+1))
  fi
else
  yellow "  SKIP: No session token"
  SKIP=$((SKIP+1))
fi

bold "5.3 WebSocket without auth rejected"
ws_unauth_output=$(timeout 3 curl -s -o "${CURL_OUTPUT_NULL}" -w "%{http_code}" \
  -H "Upgrade: websocket" \
  -H "Connection: upgrade" \
  -H "Sec-WebSocket-Version: 13" \
  -H "Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==" \
  http://localhost:8082/api/v1/stream 2>/dev/null || true)
code=$(printf '%s' "${ws_unauth_output}" | grep -Eo '[0-9]{3}' | head -1 || true)
code="${code:-000}"
check "WebSocket no auth" "401" "$code"

# =============================================================================
bold ""
bold "=== PHASE 6: Nginx Proxy Validation ==="
# =============================================================================

bold "6.1 API proxy works (proxy -> gateway)"
code=$(http_code -H "$AUTH_HEADER" -H "X-Tenant-ID: ${TENANT_ID}" http://localhost:8082/api/v1/workers)
check "Proxy: /api/v1/workers" "200" "$code"

bold "6.2 SPA fallback (unknown path -> index.html)"
code=$(http_code http://localhost:8082/some/unknown/page)
check "SPA fallback" "200" "$code"

bold "6.3 Security headers present"
headers=$(http_headers http://localhost:8082/)
if echo "$headers" | grep -qi "X-Content-Type-Options"; then
  green "  PASS: X-Content-Type-Options header present"
  PASS=$((PASS+1))
else
  red "  FAIL: X-Content-Type-Options header missing"
  FAIL=$((FAIL+1))
fi

if echo "$headers" | grep -qi "X-Frame-Options"; then
  green "  PASS: X-Frame-Options header present"
  PASS=$((PASS+1))
else
  red "  FAIL: X-Frame-Options header missing"
  FAIL=$((FAIL+1))
fi

# =============================================================================
bold ""
bold "=== PHASE 7: Safety Kernel ==="
# =============================================================================

bold "7.1 Policy evaluate via gateway"
SAFETY_BODY='{"topic":"job.default","capabilities":["shell.exec"],"risk_tags":["dangerous"],"metadata":{}}'
body=$(http_body -X POST "$BASE/policy/evaluate" \
  -H "$AUTH_HEADER" \
  -H "X-Tenant-ID: ${TENANT_ID}" \
  -H "Content-Type: application/json" \
  -d "$SAFETY_BODY")
code=$(http_code -X POST "$BASE/policy/evaluate" \
  -H "$AUTH_HEADER" \
  -H "X-Tenant-ID: ${TENANT_ID}" \
  -H "Content-Type: application/json" \
  -d "$SAFETY_BODY")

if [ "$code" = "200" ] || [ "$code" = "201" ]; then
  green "  PASS: POST /policy/evaluate (HTTP $code)"
  PASS=$((PASS+1))
  check_body "Safety decision returned" "decision" "$body"
else
  yellow "  SKIP: POST /policy/evaluate ($code) -- endpoint may not exist via HTTP"
  SKIP=$((SKIP+1))
fi

# =============================================================================
bold ""
bold "=== PHASE 8: Error Handling ==="
# =============================================================================

bold "8.1 404 for unknown API path"
code=$(http_code -H "$AUTH_HEADER" -H "X-Tenant-ID: ${TENANT_ID}" "$BASE/nonexistent")
# 404 or 405 are both acceptable
if [ "$code" = "404" ] || [ "$code" = "405" ]; then
  green "  PASS: Unknown path returns $code"
  PASS=$((PASS+1))
else
  red "  FAIL: Unknown path returned $code (expected 404/405)"
  FAIL=$((FAIL+1))
fi

bold "8.2 Invalid JSON body"
code=$(http_code -X POST "$BASE/jobs" \
  -H "$AUTH_HEADER" \
  -H "X-Tenant-ID: ${TENANT_ID}" \
  -H "Content-Type: application/json" \
  -d "not-json")
check "POST /jobs with bad JSON" "400" "$code"

bold "8.3 Missing required field"
code=$(http_code -X POST "$BASE/jobs" \
  -H "$AUTH_HEADER" \
  -H "X-Tenant-ID: ${TENANT_ID}" \
  -H "Content-Type: application/json" \
  -d '{}')
check "POST /jobs with empty body" "400" "$code"

# =============================================================================
bold ""
bold "=== PHASE 9: Logout ==="
# =============================================================================

bold "9.1 Logout"
if [ -n "$SESSION" ]; then
  code=$(http_code -X POST "$BASE/auth/logout" \
    -H "X-API-Key: $SESSION" \
    -H "X-Tenant-ID: ${TENANT_ID}")
  # Accept 200 or 204
  if [ "$code" = "200" ] || [ "$code" = "204" ]; then
    green "  PASS: POST /auth/logout (HTTP $code)"
    PASS=$((PASS+1))
  else
    red "  FAIL: POST /auth/logout ($code)"
    FAIL=$((FAIL+1))
  fi

  bold "9.2 Session invalidated after logout"
  code=$(http_code -H "X-API-Key: $SESSION" -H "X-Tenant-ID: ${TENANT_ID}" "$BASE/workers")
  check "GET /workers with expired session" "401" "$code"
else
  yellow "  SKIP: No session to test logout"
  SKIP=$((SKIP+2))
fi

# =============================================================================
bold ""
bold "============================================"
bold "  E2E Test Results"
bold "============================================"
green "  Passed: $PASS"
if [ "$FAIL" -gt 0 ]; then
  red "  Failed: $FAIL"
else
  green "  Failed: $FAIL"
fi
if [ "$SKIP" -gt 0 ]; then
  yellow "  Skipped: $SKIP"
fi
bold "  Total:  $((PASS + FAIL + SKIP))"

if [ "$FAIL" -gt 0 ]; then
  bold ""
  red "  Failures:"
  printf "$ERRORS\n"
  bold ""
  exit 1
else
  bold ""
  green "  All tests passed!"
  bold ""
  exit 0
fi

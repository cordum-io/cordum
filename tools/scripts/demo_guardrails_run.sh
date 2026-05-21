#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"

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
API_KEY=${CORDUM_API_KEY:-${API_KEY:-}}
if [[ -z "${API_KEY}" ]]; then
  echo "CORDUM_API_KEY is required; export it before running the demo." >&2
  exit 1
fi
ORG_ID=${CORDUM_ORG_ID:-${CORDUM_TENANT_ID:-default}}
TENANT_ID=${CORDUM_TENANT_ID:-${ORG_ID}}

CURL_TLS_OPTS=()
TLS_CA="${CORDUM_TLS_CA:-}"
if [[ -z "${TLS_CA}" && "${API_BASE}" == https://* && -f "${ROOT_DIR}/certs/ca/ca.crt" ]]; then
  TLS_CA="${ROOT_DIR}/certs/ca/ca.crt"
fi
if [[ "${CORDUM_TLS_INSECURE:-}" =~ ^(1|true|TRUE|yes|YES)$ ]]; then
  CURL_TLS_OPTS=(-k)
elif [[ -n "${TLS_CA}" ]]; then
  CURL_TLS_OPTS=(--cacert "${TLS_CA}")
  if curl --version 2>/dev/null | grep -qi schannel; then
    CURL_TLS_OPTS+=(--ssl-no-revoke)
  fi
  export CORDUM_TLS_CA="${TLS_CA}"
fi

# absolutise_path resolves a relative TLS path against ROOT_DIR so the
# worker process (which runs from a different CWD) sees the same file.
# POSIX absolute (/) and Windows absolute (C:\, D:/) paths are returned
# unchanged.
absolutise_path() {
  local p="${1:-}"
  if [[ -z "${p}" ]]; then
    printf ''
    return 0
  fi
  if [[ "${p}" = /* || "${p}" =~ ^[A-Za-z]:[\\/] ]]; then
    printf '%s' "${p}"
    return 0
  fi
  local dir base
  dir="$(dirname "${p}")"
  base="$(basename "${p}")"
  if ( cd "${ROOT_DIR}" && cd "${dir}" >/dev/null 2>&1 ); then
    local abs_dir
    abs_dir="$( cd "${ROOT_DIR}" && cd "${dir}" && pwd -P )"
    printf '%s/%s' "${abs_dir}" "${base}"
  else
    printf '%s' "${p}"
  fi
}

# nats_tls_active / redis_tls_active return 0 (success) when any TLS
# signal is in play for that transport. They let each URL's scheme be
# derived independently so NATS can be tls:// while Redis stays
# redis:// when only NATS_TLS_CA is set (or vice versa). The union
# logic: TLS_CA set OR CORDUM_TLS_INSECURE truthy OR the per-service
# NATS_TLS_CA / REDIS_TLS_CA env is non-empty.
nats_tls_active() {
  [[ -n "${TLS_CA}" ]] && return 0
  [[ "${CORDUM_TLS_INSECURE:-}" =~ ^(1|true|TRUE|yes|YES)$ ]] && return 0
  [[ -n "${NATS_TLS_CA:-}" ]] && return 0
  return 1
}

redis_tls_active() {
  [[ -n "${TLS_CA}" ]] && return 0
  [[ "${CORDUM_TLS_INSECURE:-}" =~ ^(1|true|TRUE|yes|YES)$ ]] && return 0
  [[ -n "${REDIS_TLS_CA:-}" ]] && return 0
  return 1
}

TLS_CA="$(absolutise_path "${TLS_CA}")"
if [[ -n "${TLS_CA}" ]]; then
  export CORDUM_TLS_CA="${TLS_CA}"
fi

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

NATS_URL=${NATS_URL:-}
REDIS_URL=${REDIS_URL:-}
if [[ -z "${NATS_URL}" ]]; then
  if nats_tls_active; then
    NATS_URL="tls://localhost:4222"
  else
    NATS_URL="nats://localhost:4222"
  fi
fi
if [[ -z "${REDIS_URL}" ]]; then
  if redis_tls_active; then
    REDIS_URL="rediss://:${REDIS_PASSWORD:-cordum-dev}@localhost:6379/0"
  else
    REDIS_URL="redis://:${REDIS_PASSWORD:-cordum-dev}@localhost:6379/0"
  fi
fi

CLIENT_CERT="${CORDUM_TLS_CERT:-}"
CLIENT_KEY="${CORDUM_TLS_KEY:-}"
if [[ -n "${TLS_CA}" ]]; then
  if [[ -z "${CLIENT_CERT}" && -f "${ROOT_DIR}/certs/client/tls.crt" ]]; then
    CLIENT_CERT="${ROOT_DIR}/certs/client/tls.crt"
  fi
  if [[ -z "${CLIENT_KEY}" && -f "${ROOT_DIR}/certs/client/tls.key" ]]; then
    CLIENT_KEY="${ROOT_DIR}/certs/client/tls.key"
  fi
fi
CLIENT_CERT="$(absolutise_path "${CLIENT_CERT}")"
CLIENT_KEY="$(absolutise_path "${CLIENT_KEY}")"
NATS_TLS_CA="${NATS_TLS_CA:-$(to_worker_tls_path "${TLS_CA}")}"
NATS_TLS_CERT="${NATS_TLS_CERT:-$(to_worker_tls_path "${CLIENT_CERT}")}"
NATS_TLS_KEY="${NATS_TLS_KEY:-$(to_worker_tls_path "${CLIENT_KEY}")}"
REDIS_TLS_CA="${REDIS_TLS_CA:-$(to_worker_tls_path "${TLS_CA}")}"
REDIS_TLS_CERT="${REDIS_TLS_CERT:-$(to_worker_tls_path "${CLIENT_CERT}")}"
REDIS_TLS_KEY="${REDIS_TLS_KEY:-$(to_worker_tls_path "${CLIENT_KEY}")}"
NATS_TLS_INSECURE="${NATS_TLS_INSECURE:-${CORDUM_TLS_INSECURE:-}}"
REDIS_TLS_INSECURE="${REDIS_TLS_INSECURE:-${CORDUM_TLS_INSECURE:-}}"
WORKER_LOG="$(mktemp -t demo-guardrails-worker.XXXXXX.log)"

cleanup() {
  if [[ -n "${WORKER_PID:-}" ]]; then
    kill "${WORKER_PID}" >/dev/null 2>&1 || true
    wait "${WORKER_PID}" >/dev/null 2>&1 || true
  fi
  rm -f "${WORKER_LOG}" >/dev/null 2>&1 || true
}
trap cleanup EXIT

echo "[guardrails] checking gateway"
curl -sS "${CURL_TLS_OPTS[@]}" "${API_BASE}/api/v1/status" -H "X-API-Key: ${API_KEY}" -H "X-Tenant-ID: ${TENANT_ID}" >/dev/null

echo "[guardrails] starting demo worker"
(cd "${ROOT_DIR}/examples/demo-guardrails/worker" && \
  NATS_URL="${NATS_URL}" \
  REDIS_URL="${REDIS_URL}" \
  NATS_TLS_CA="${NATS_TLS_CA:-}" \
  NATS_TLS_CERT="${NATS_TLS_CERT:-}" \
  NATS_TLS_KEY="${NATS_TLS_KEY:-}" \
  NATS_TLS_INSECURE="${NATS_TLS_INSECURE:-}" \
  REDIS_TLS_CA="${REDIS_TLS_CA:-}" \
  REDIS_TLS_CERT="${REDIS_TLS_CERT:-}" \
  REDIS_TLS_KEY="${REDIS_TLS_KEY:-}" \
  REDIS_TLS_INSECURE="${REDIS_TLS_INSECURE:-}" \
  go run .) >"${WORKER_LOG}" 2>&1 &
WORKER_PID=$!

echo "[guardrails] waiting for worker heartbeat"
worker_ready=0
for _ in {1..120}; do
  # Check for the demo worker pool specifically to avoid false positives.
  if curl -fsS "${CURL_TLS_OPTS[@]}" --max-time 2 "${API_BASE}/api/v1/workers" \
    -H "X-API-Key: ${API_KEY}" -H "X-Tenant-ID: ${TENANT_ID}" 2>/dev/null \
    | jq -e '
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
      [worker_items[] | select(.pool == "demo-guardrails")] | length > 0
    ' >/dev/null 2>&1; then
    worker_ready=1
    break
  fi
  sleep 0.5
done
if [[ "${worker_ready}" != "1" ]]; then
  echo "worker did not register in time" >&2
  echo "[guardrails] worker log (tail):" >&2
  tail -50 "${WORKER_LOG}" >&2 2>/dev/null || true
  exit 1
fi

echo "[guardrails] running demo"
CORDUM_API_KEY="${API_KEY}" \
CORDUM_ORG_ID="${ORG_ID}" \
CORDUM_TENANT_ID="${TENANT_ID}" \
CORDUM_API_BASE="${API_BASE}" \
CORDUM_TLS_CA="${CORDUM_TLS_CA:-}" \
CORDUM_TLS_INSECURE="${CORDUM_TLS_INSECURE:-}" \
CORDUMCTL="${CORDUMCTL:-}" \
CORDUMCTL_BIN="${CORDUMCTL_BIN:-}" \
"${ROOT_DIR}/tools/scripts/demo_guardrails.sh"

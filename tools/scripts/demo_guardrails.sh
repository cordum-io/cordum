#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"

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
  if [[ -x "${ROOT_DIR}/bin/cordumctl" ]]; then
    printf '%s' "${ROOT_DIR}/bin/cordumctl"
    return 0
  fi
  if [[ -x "${ROOT_DIR}/cordumctl" ]]; then
    printf '%s' "${ROOT_DIR}/cordumctl"
    return 0
  fi
  if [[ -x "${ROOT_DIR}/cmd/cordumctl/cordumctl" ]]; then
    printf '%s' "${ROOT_DIR}/cmd/cordumctl/cordumctl"
    return 0
  fi
  return 1
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

CTL_BIN="$(resolve_cordumctl || true)"
if ! command -v curl >/dev/null 2>&1; then
  echo "curl is required" >&2
  exit 1
fi
if ! command -v jq >/dev/null 2>&1; then
  echo "jq is required" >&2
  exit 1
fi

API_BASE=${CORDUM_API_BASE:-http://localhost:8081}
API_KEY=${CORDUM_API_KEY:-${API_KEY:-}}
if [[ -z "${API_KEY}" ]]; then
  echo "CORDUM_API_KEY is required; export it before running the demo." >&2
  exit 1
fi
export CORDUM_GATEWAY=${CORDUM_GATEWAY:-${API_BASE}}
ORG_ID=${CORDUM_ORG_ID:-${CORDUM_TENANT_ID:-default}}
TENANT_ID=${CORDUM_TENANT_ID:-${ORG_ID}}
export CORDUM_TENANT_ID=${TENANT_ID}
auth_header=("-H" "X-API-Key: ${API_KEY}" "-H" "X-Tenant-ID: ${TENANT_ID}")

CURL_TLS_OPTS=()
if [[ "${CORDUM_TLS_INSECURE:-}" =~ ^(1|true|TRUE|yes|YES)$ ]]; then
  CURL_TLS_OPTS=(-k)
else
  TLS_CA="${CORDUM_TLS_CA:-}"
  if [[ -z "${TLS_CA}" && "${API_BASE}" == https://* && -f "${ROOT_DIR}/certs/ca/ca.crt" ]]; then
    TLS_CA="${ROOT_DIR}/certs/ca/ca.crt"
  fi
  if [[ -n "${TLS_CA}" ]]; then
    CURL_TLS_OPTS=(--cacert "${TLS_CA}")
    if curl --version 2>/dev/null | grep -qi schannel; then
      CURL_TLS_OPTS+=(--ssl-no-revoke)
    fi
    export CORDUM_TLS_CA="${TLS_CA}"
  fi
fi

if [[ -z "${CTL_BIN}" || ! -x "${CTL_BIN}" ]]; then
  echo "cordumctl not found; build with make build" >&2
  exit 1
fi

"${CTL_BIN}" pack install --upgrade "$(to_cordumctl_path "${ROOT_DIR}/examples/demo-guardrails")"

echo "[demo] starting approval workflow run"
approval_run=$(curl -sS "${CURL_TLS_OPTS[@]}" -X POST "${API_BASE}/api/v1/workflows/demo-guardrails.approval/runs?org_id=${ORG_ID}" \
  "${auth_header[@]}" \
  -H "Content-Type: application/json" \
  -d '{"message":"Ship with guardrails","actor":"demo"}' | jq -r '.run_id')
if [[ -z "${approval_run}" || "${approval_run}" == "null" ]]; then
  echo "failed to start approval run" >&2
  exit 1
fi

approval_job=""
for _ in {1..20}; do
  approval_job=$(curl -sS "${CURL_TLS_OPTS[@]}" "${auth_header[@]}" "${API_BASE}/api/v1/workflow-runs/${approval_run}?org_id=${ORG_ID}" | jq -r '.steps.write.job_id // empty')
  if [[ -n "${approval_job}" ]]; then
    break
  fi
  sleep 0.3
done
if [[ -z "${approval_job}" ]]; then
  echo "approval job not found" >&2
  exit 1
fi

if ! approval_out=$("${CTL_BIN}" approval job --approve "${approval_job}" 2>&1); then
  if grep -qi "job not awaiting approval" <<<"${approval_out}"; then
    echo "[demo] approval already satisfied for ${approval_job}"
  else
    echo "${approval_out}" >&2
    exit 1
  fi
fi

status=""
for _ in {1..25}; do
  status=$(curl -sS "${CURL_TLS_OPTS[@]}" "${auth_header[@]}" "${API_BASE}/api/v1/workflow-runs/${approval_run}?org_id=${ORG_ID}" | jq -r '.status')
  if [[ "${status}" == "succeeded" ]]; then
    break
  fi
  sleep 0.4
done

echo "[demo] approval run status: ${status}"
if [[ "${status}" != "succeeded" ]]; then
  echo "approval run did not succeed (status=${status})" >&2
  exit 1
fi

echo "[demo] submitting dangerous job"
danger_body="$(mktemp)"
danger_status=$(curl -sS "${CURL_TLS_OPTS[@]}" -o "${danger_body}" -w "%{http_code}" \
  -X POST "${API_BASE}/api/v1/jobs" \
  "${auth_header[@]}" \
  -H "Content-Type: application/json" \
  -d '{"topic":"job.demo-guardrails.dangerous","prompt":"rm -rf /"}')
if [[ "${danger_status}" != "403" ]]; then
  echo "unexpected dangerous submit status ${danger_status}: $(cat "${danger_body}")" >&2
  rm -f "${danger_body}"
  exit 1
fi

danger_job=$(jq -r '.job_id // empty' "${danger_body}")
rm -f "${danger_body}"
if [[ -z "${danger_job}" ]]; then
  echo "failed to persist denied dangerous job" >&2
  exit 1
fi

sleep 0.5

dlq_job=""
for _ in {1..20}; do
  dlq_job=$(curl -sS "${CURL_TLS_OPTS[@]}" "${auth_header[@]}" "${API_BASE}/api/v1/dlq/page?org_id=${ORG_ID}&limit=20" | jq -r --arg job_id "${danger_job}" '.items[]? | select(.job_id == $job_id) | .job_id' | head -n 1)
  if [[ -n "${dlq_job}" ]]; then
    break
  fi
  sleep 0.3
done
if [[ -z "${dlq_job}" ]]; then
  echo "denied job ${danger_job} not found in DLQ" >&2
  exit 1
fi

echo "[demo] applying remediation to denied job"
remediate_resp=$(curl -sS "${CURL_TLS_OPTS[@]}" -X POST "${API_BASE}/api/v1/jobs/${danger_job}/remediate" \
  "${auth_header[@]}" \
  -H "Content-Type: application/json" \
  -d '{}' )

echo "[demo] remediation result: ${remediate_resp}"

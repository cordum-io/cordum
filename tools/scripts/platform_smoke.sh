#!/usr/bin/env bash
set -euo pipefail

if ! command -v curl >/dev/null 2>&1; then
  echo "curl is required" >&2
  exit 1
fi
if ! command -v jq >/dev/null 2>&1; then
  echo "jq is required" >&2
  exit 1
fi

API_KEY=${CORDUM_API_KEY:-${API_KEY:-}}
if [[ -z "${API_KEY}" ]]; then
  echo "CORDUM_API_KEY is required; export it before running the smoke test." >&2
  exit 1
fi
ORG_ID=${CORDUM_ORG_ID:-${CORDUM_TENANT_ID:-default}}
TENANT_ID=${CORDUM_TENANT_ID:-${ORG_ID}}

# TLS auto-detection
CURL_TLS_OPTS=()
TLS_CA="${CORDUM_TLS_CA:-}"
if [[ -z "${TLS_CA}" && -f "./certs/ca/ca.crt" ]]; then
  TLS_CA="./certs/ca/ca.crt"
fi
if [[ -n "${TLS_CA}" ]]; then
  CURL_TLS_OPTS=(--cacert "${TLS_CA}")
  if curl --version 2>/dev/null | grep -qi schannel; then
    CURL_TLS_OPTS+=(--ssl-no-revoke)
  fi
  API_BASE="${CORDUM_API_BASE:-https://localhost:8081}"
else
  API_BASE="${CORDUM_API_BASE:-http://localhost:8081}"
fi

auth_header=("-H" "X-API-Key: ${API_KEY}" "-H" "X-Tenant-ID: ${TENANT_ID}")
json_header=("-H" "Content-Type: application/json")

log() {
  echo "[platform_smoke] $*"
}

probe_json_field() {
  local method=$1
  local path=$2
  local expected_status=$3
  local jq_expr=$4
  local label=$5

  local body_file
  body_file=$(mktemp)
  trap 'rm -f "${body_file}"' RETURN

  log "probing ${label}: ${method} ${path}"
  local status
  status=$(curl -sS "${CURL_TLS_OPTS[@]}" "${auth_header[@]}" \
    -o "${body_file}" -w "%{http_code}" \
    -X "${method}" "${API_BASE}${path}")

  if [[ "${status}" != "${expected_status}" ]]; then
    echo "${label} returned HTTP ${status}, want ${expected_status}" >&2
    cat "${body_file}" >&2
    exit 1
  fi
  if ! jq -e "${jq_expr}" "${body_file}" >/dev/null; then
    echo "${label} response missing expected field check: ${jq_expr}" >&2
    cat "${body_file}" >&2
    exit 1
  fi

  rm -f "${body_file}"
  trap - RETURN
}

log "creating workflow"
workflow_payload=$(cat <<JSON
{
  "name": "platform-smoke",
  "org_id": "${ORG_ID}",
  "steps": {
    "approve": {
      "type": "approval",
      "name": "Approve"
    }
  }
}
JSON
)

wf_id=$(curl -sS "${CURL_TLS_OPTS[@]}" "${auth_header[@]}" "${json_header[@]}" \
  -X POST "${API_BASE}/api/v1/workflows" \
  -d "${workflow_payload}" | jq -r '.id')

if [[ -z "${wf_id}" || "${wf_id}" == "null" ]]; then
  echo "failed to create workflow" >&2
  exit 1
fi
log "workflow id: ${wf_id}"

log "starting run"
run_id=$(curl -sS "${CURL_TLS_OPTS[@]}" "${auth_header[@]}" "${json_header[@]}" \
  -X POST "${API_BASE}/api/v1/workflows/${wf_id}/runs" \
  -d '{}' | jq -r '.run_id')

if [[ -z "${run_id}" || "${run_id}" == "null" ]]; then
  echo "failed to start run" >&2
  exit 1
fi
log "run id: ${run_id}"

log "waiting for workflow engine to initialize step"
job_id=""
for _ in {1..20}; do
  job_id=$(curl -sS "${CURL_TLS_OPTS[@]}" "${auth_header[@]}" \
    "${API_BASE}/api/v1/workflow-runs/${run_id}" | jq -r '.steps.approve.job_id // empty')
  if [[ -n "${job_id}" ]]; then
    break
  fi
  sleep 0.5
done
if [[ -z "${job_id}" ]]; then
  echo "workflow engine did not initialize step within 10s" >&2
  exit 1
fi
log "step job id: ${job_id}"

log "approving step"
approve_body_file=$(mktemp)
if ! approve_status=$(curl -sS "${CURL_TLS_OPTS[@]}" "${auth_header[@]}" "${json_header[@]}" \
  -o "${approve_body_file}" -w "%{http_code}" \
  -X POST "${API_BASE}/api/v1/approvals/${job_id}/approve" \
  -d '{"reason":"platform smoke approval"}'); then
  echo "approval request failed" >&2
  cat "${approve_body_file}" >&2 2>/dev/null || true
  rm -f "${approve_body_file}"
  exit 1
fi
if [[ "${approve_status}" != "200" && "${approve_status}" != "204" && "${approve_status}" != "409" ]]; then
  echo "approval request returned HTTP ${approve_status}, want 200/204/409" >&2
  cat "${approve_body_file}" >&2
  rm -f "${approve_body_file}"
  exit 1
fi
rm -f "${approve_body_file}"

status=""
for _ in {1..20}; do
  status=$(curl -sS "${CURL_TLS_OPTS[@]}" "${auth_header[@]}" "${API_BASE}/api/v1/workflow-runs/${run_id}" | jq -r '.status')
  if [[ "${status}" == "succeeded" ]]; then
    break
  fi
  sleep 0.5
done
log "run status: ${status}"

if [[ "${status}" != "succeeded" ]]; then
  echo "expected run status 'succeeded', got '${status}'" >&2
  exit 1
fi

log "verifying audit chain"
audit_resp=""
audit_ok=""
for attempt in {1..10}; do
  if audit_resp=$(curl -fsS "${CURL_TLS_OPTS[@]}" "${auth_header[@]}" \
    "${API_BASE}/api/v1/audit/verify?tenant=${TENANT_ID}"); then
    if echo "${audit_resp}" | jq -e '.status == "ok" and .total_events > 0 and (.gaps | length) == 0' >/dev/null; then
      audit_ok="true"
      break
    fi
  else
    audit_resp=""
  fi
  log "audit verify not ready (attempt ${attempt}/10); retrying"
  sleep 0.5
done
if [[ -z "${audit_ok}" ]]; then
  echo "audit/verify did not return healthy chain; got: ${audit_resp}" >&2
  exit 1
fi
log "audit verify passed"
probe_json_field "GET" "/api/v1/governance/health?tenant=${TENANT_ID}" "200" '(.grade | test("^[ABCDF]$")) and (.factors | has("denial_rate") and has("approval_latency_p95") and has("policy_coverage") and has("chain_integrity"))' "governance health"


log "deleting run"
curl -sS "${CURL_TLS_OPTS[@]}" "${auth_header[@]}" -X DELETE "${API_BASE}/api/v1/workflow-runs/${run_id}" >/dev/null

log "deleting workflow"
curl -sS "${CURL_TLS_OPTS[@]}" "${auth_header[@]}" -X DELETE "${API_BASE}/api/v1/workflows/${wf_id}" >/dev/null

log "done"

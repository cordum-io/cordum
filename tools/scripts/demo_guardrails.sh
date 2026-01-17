#!/usr/bin/env bash
set -euo pipefail

if command -v cordumctl >/dev/null 2>&1; then
  CTL_BIN="cordumctl"
else
  CTL_BIN="./cmd/cordumctl/cordumctl"
fi
if ! command -v curl >/dev/null 2>&1; then
  echo "curl is required" >&2
  exit 1
fi
if ! command -v jq >/dev/null 2>&1; then
  echo "jq is required" >&2
  exit 1
fi

API_BASE=${CORDUM_API_BASE:-http://localhost:8081}
API_KEY=${CORDUM_API_KEY:-${CORDUM_SUPER_SECRET_API_TOKEN:-super-secret-key}}
auth_header=("-H" "X-API-Key: ${API_KEY}")

if [[ ! -x "${CTL_BIN}" ]]; then
  echo "cordumctl not found; build with make build" >&2
  exit 1
fi

"${CTL_BIN}" pack install ./examples/demo-guardrails --upgrade

echo "[demo] starting approval workflow run"
approval_run=$(curl -sS -X POST "${API_BASE}/api/v1/workflows/demo-guardrails.approval/runs" \
  "${auth_header[@]}" \
  -H "Content-Type: application/json" \
  -d '{"message":"Ship with guardrails","actor":"demo"}' | jq -r '.run_id')
if [[ -z "${approval_run}" || "${approval_run}" == "null" ]]; then
  echo "failed to start approval run" >&2
  exit 1
fi

approval_job=""
for _ in {1..20}; do
  approval_job=$(curl -sS "${auth_header[@]}" "${API_BASE}/api/v1/approvals?limit=1" | jq -r '.items[0].job_id // empty')
  if [[ -n "${approval_job}" ]]; then
    break
  fi
  sleep 0.3
done
if [[ -z "${approval_job}" ]]; then
  echo "approval job not found" >&2
  exit 1
fi

"${CTL_BIN}" approval job --approve "${approval_job}"

status=""
for _ in {1..25}; do
  status=$(curl -sS "${auth_header[@]}" "${API_BASE}/api/v1/workflow-runs/${approval_run}" | jq -r '.status')
  if [[ "${status}" == "succeeded" ]]; then
    break
  fi
  sleep 0.4
done

echo "[demo] approval run status: ${status}"

echo "[demo] submitting dangerous job"
danger_job=$(${CTL_BIN} job submit --topic job.demo-guardrails.dangerous --input '{"message":"rm -rf /","actor":"demo"}' --json | jq -r '.job_id')
if [[ -z "${danger_job}" || "${danger_job}" == "null" ]]; then
  echo "failed to submit dangerous job" >&2
  exit 1
fi

sleep 0.5

dlq_job=""
for _ in {1..20}; do
  dlq_job=$(curl -sS "${auth_header[@]}" "${API_BASE}/api/v1/dlq/page?limit=1" | jq -r '.items[0].job_id // empty')
  if [[ -n "${dlq_job}" ]]; then
    break
  fi
  sleep 0.3
done
if [[ -z "${dlq_job}" ]]; then
  echo "denied job not found in DLQ" >&2
  exit 1
fi

echo "[demo] applying remediation to denied job"
remediate_resp=$(curl -sS -X POST "${API_BASE}/api/v1/jobs/${dlq_job}/remediate" \
  "${auth_header[@]}" \
  -H "Content-Type: application/json" \
  -d '{}' )

echo "[demo] remediation result: ${remediate_resp}"

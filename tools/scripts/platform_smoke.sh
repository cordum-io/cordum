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

API_BASE=${CORETEX_API_BASE:-http://localhost:8081}
API_KEY=${CORETEX_API_KEY:-${CORETEX_SUPER_SECRET_API_TOKEN:-${API_KEY:-super-secret-key}}}

auth_header=("-H" "X-API-Key: ${API_KEY}")
json_header=("-H" "Content-Type: application/json")

log() {
  echo "[platform_smoke] $*"
}

log "creating workflow"
workflow_payload=$(cat <<'JSON'
{
  "name": "platform-smoke",
  "org_id": "default",
  "steps": {
    "approve": {
      "type": "approval",
      "name": "Approve"
    }
  }
}
JSON
)

wf_id=$(curl -sS "${auth_header[@]}" "${json_header[@]}" \
  -X POST "${API_BASE}/api/v1/workflows" \
  -d "${workflow_payload}" | jq -r '.id')

if [[ -z "${wf_id}" || "${wf_id}" == "null" ]]; then
  echo "failed to create workflow" >&2
  exit 1
fi
log "workflow id: ${wf_id}"

log "starting run"
run_id=$(curl -sS "${auth_header[@]}" "${json_header[@]}" \
  -X POST "${API_BASE}/api/v1/workflows/${wf_id}/runs" \
  -d '{}' | jq -r '.run_id')

if [[ -z "${run_id}" || "${run_id}" == "null" ]]; then
  echo "failed to start run" >&2
  exit 1
fi
log "run id: ${run_id}"

log "approving step"
curl -sS "${auth_header[@]}" "${json_header[@]}" \
  -X POST "${API_BASE}/api/v1/workflows/${wf_id}/runs/${run_id}/steps/approve/approve" \
  -d '{"approved": true}' >/dev/null

status=""
for _ in {1..10}; do
  status=$(curl -sS "${auth_header[@]}" "${API_BASE}/api/v1/workflow-runs/${run_id}" | jq -r '.status')
  if [[ "${status}" == "succeeded" ]]; then
    break
  fi
  sleep 0.2
done
log "run status: ${status}"

log "deleting run"
curl -sS "${auth_header[@]}" -X DELETE "${API_BASE}/api/v1/workflow-runs/${run_id}" >/dev/null

log "deleting workflow"
curl -sS "${auth_header[@]}" -X DELETE "${API_BASE}/api/v1/workflows/${wf_id}" >/dev/null

log "done"

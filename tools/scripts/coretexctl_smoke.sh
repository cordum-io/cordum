#!/usr/bin/env bash
set -euo pipefail

if ! command -v coretexctl >/dev/null 2>&1; then
  echo "coretexctl is required on PATH (build via make build)" >&2
  exit 1
fi
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
export CORETEX_GATEWAY=${CORETEX_GATEWAY:-${API_BASE}}
export CORETEX_API_KEY=${CORETEX_API_KEY:-${API_KEY}}
auth_header=("-H" "X-API-Key: ${API_KEY}")

tmpdir=$(mktemp -d)
cleanup() {
  rm -rf "${tmpdir}"
}
trap cleanup EXIT

workflow_file="${tmpdir}/workflow.json"
cat > "${workflow_file}" <<'JSON'
{
  "name": "coretexctl-smoke",
  "org_id": "default",
  "steps": {
    "approve": {
      "type": "approval",
      "name": "Approve"
    }
  }
}
JSON

echo "[coretexctl_smoke] creating workflow"
wf_id=$(coretexctl workflow create --file "${workflow_file}")
if [[ -z "${wf_id}" ]]; then
  echo "failed to create workflow" >&2
  exit 1
fi
echo "[coretexctl_smoke] workflow id: ${wf_id}"

echo "[coretexctl_smoke] starting run"
run_id=$(coretexctl run start "${wf_id}")
if [[ -z "${run_id}" ]]; then
  echo "failed to start run" >&2
  exit 1
fi
echo "[coretexctl_smoke] run id: ${run_id}"

echo "[coretexctl_smoke] approving step"
coretexctl approval step --approve "${wf_id}" "${run_id}" "approve"

status=""
for _ in {1..10}; do
  status=$(curl -sS "${auth_header[@]}" "${API_BASE}/api/v1/workflow-runs/${run_id}" | jq -r '.status')
  if [[ "${status}" == "succeeded" ]]; then
    break
  fi
  sleep 0.2
done
echo "[coretexctl_smoke] run status: ${status}"

echo "[coretexctl_smoke] deleting run"
coretexctl run delete "${run_id}"

echo "[coretexctl_smoke] deleting workflow"
coretexctl workflow delete "${wf_id}"

echo "[coretexctl_smoke] done"

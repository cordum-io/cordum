#!/usr/bin/env bash
set -euo pipefail

if command -v cordumctl >/dev/null 2>&1; then
  CTL_BIN="cordumctl"
else
  echo "cordumctl is required on PATH (build via make build)" >&2
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

API_BASE=${CORDUM_API_BASE:-http://localhost:8081}
API_KEY=${CORDUM_API_KEY:-${CORDUM_SUPER_SECRET_API_TOKEN:-${API_KEY:-super-secret-key}}}
export CORDUM_GATEWAY=${CORDUM_GATEWAY:-${API_BASE}}
export CORDUM_API_KEY=${CORDUM_API_KEY:-${API_KEY}}
auth_header=("-H" "X-API-Key: ${API_KEY}")

tmpdir=$(mktemp -d)
cleanup() {
  rm -rf "${tmpdir}"
}
trap cleanup EXIT

workflow_file="${tmpdir}/workflow.json"
cat > "${workflow_file}" <<'JSON'
{
  "name": "cordumctl-smoke",
  "org_id": "default",
  "steps": {
    "approve": {
      "type": "approval",
      "name": "Approve"
    }
  }
}
JSON

echo "[cordumctl_smoke] creating workflow"
wf_id=$("${CTL_BIN}" workflow create --file "${workflow_file}")
if [[ -z "${wf_id}" ]]; then
  echo "failed to create workflow" >&2
  exit 1
fi
echo "[cordumctl_smoke] workflow id: ${wf_id}"

echo "[cordumctl_smoke] starting run"
run_id=$("${CTL_BIN}" run start "${wf_id}")
if [[ -z "${run_id}" ]]; then
  echo "failed to start run" >&2
  exit 1
fi
echo "[cordumctl_smoke] run id: ${run_id}"

echo "[cordumctl_smoke] approving step"
"${CTL_BIN}" approval step --approve "${wf_id}" "${run_id}" "approve"

status=""
for _ in {1..10}; do
  status=$(curl -sS "${auth_header[@]}" "${API_BASE}/api/v1/workflow-runs/${run_id}" | jq -r '.status')
  if [[ "${status}" == "succeeded" ]]; then
    break
  fi
  sleep 0.2
done
echo "[cordumctl_smoke] run status: ${status}"

echo "[cordumctl_smoke] deleting run"
"${CTL_BIN}" run delete "${run_id}"

echo "[cordumctl_smoke] deleting workflow"
"${CTL_BIN}" workflow delete "${wf_id}"

echo "[cordumctl_smoke] done"

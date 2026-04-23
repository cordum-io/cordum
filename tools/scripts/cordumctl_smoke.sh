#!/usr/bin/env bash
set -euo pipefail

if [[ -n "${CORDUMCTL_BIN:-}" ]]; then
  CTL_BIN="${CORDUMCTL_BIN}"
elif [[ -n "${CORDUMCTL:-}" ]]; then
  CTL_BIN="${CORDUMCTL}"
elif command -v cordumctl >/dev/null 2>&1; then
  CTL_BIN="cordumctl"
else
  echo "cordumctl is required on PATH (build via make build) or set CORDUMCTL/CORDUMCTL_BIN" >&2
  exit 1
fi
if ! command -v jq >/dev/null 2>&1; then
  echo "jq is required" >&2
  exit 1
fi

API_BASE=${CORDUM_API_BASE:-http://localhost:8081}
API_KEY=${CORDUM_API_KEY:-${API_KEY:-}}
if [[ -z "${API_KEY}" ]]; then
  echo "CORDUM_API_KEY is required; export it before running the smoke test." >&2
  exit 1
fi
ORG_ID=${CORDUM_ORG_ID:-${CORDUM_TENANT_ID:-default}}
TENANT_ID=${CORDUM_TENANT_ID:-${ORG_ID}}
export CORDUM_GATEWAY=${CORDUM_GATEWAY:-${API_BASE}}
export CORDUM_API_KEY=${CORDUM_API_KEY:-${API_KEY}}
export CORDUM_TENANT_ID=${TENANT_ID}
if [[ -z "${CORDUM_TLS_CA:-}" && "${API_BASE}" == https://* ]]; then
  repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
  if [[ -f "${repo_root}/certs/ca/ca.crt" ]]; then
    export CORDUM_TLS_CA="${repo_root}/certs/ca/ca.crt"
  fi
fi

tmpdir=$(mktemp -d)
cleanup() {
  rm -rf "${tmpdir}"
}
trap cleanup EXIT

workflow_file="${tmpdir}/workflow.json"
cat > "${workflow_file}" <<JSON
{
  "name": "cordumctl-smoke",
  "org_id": "${ORG_ID}",
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

approval_job=""
for _ in {1..20}; do
  approval_job=$("${CTL_BIN}" run get "${run_id}" | jq -r '.steps.approve.job_id // empty')
  if [[ -n "${approval_job}" ]]; then
    break
  fi
  sleep 0.2
done
if [[ -z "${approval_job}" ]]; then
  echo "failed to locate approval job for run ${run_id}" >&2
  exit 1
fi

echo "[cordumctl_smoke] approving job ${approval_job}"
"${CTL_BIN}" approval job "${approval_job}" --approve

status=""
for _ in {1..20}; do
  status=$("${CTL_BIN}" run get "${run_id}" | jq -r '.status // empty')
  if [[ "${status}" == "succeeded" ]]; then
    break
  fi
  sleep 0.5
done
echo "[cordumctl_smoke] run status: ${status}"
if [[ "${status}" != "succeeded" ]]; then
  echo "expected run ${run_id} to succeed after approval, got ${status}" >&2
  exit 1
fi

echo "[cordumctl_smoke] deleting run"
"${CTL_BIN}" run delete "${run_id}"

echo "[cordumctl_smoke] deleting workflow"
"${CTL_BIN}" workflow delete "${wf_id}"

echo "[cordumctl_smoke] done"

#!/usr/bin/env bash
# =============================================================================
# integration_test.sh — end-to-end test for the demo-mock-bank pack.
#
# Verifies all three verdict paths through the demo-mock-bank.transfer
# workflow against a live stack:
#   $40   → execute_low    → succeeded        (bank-transfer-allow,   ≤ 10 s)
#   $200  → execute_review → succeeded after  (bank-transfer-review,  ≤ 15 s
#                            cordumctl approval job <id> --approve     after approve)
#   $5000 → execute_high   → denied           (bank-transfer-blocked, ≤ 10 s)
#
# Pre-conditions (NOT bootstrapped here):
#   1. Core stack healthy: `make dev-up`.
#   2. Demo profile up:    `docker compose --profile demo up -d`.
#   3. Pack installed:     the script installs / upgrades `./pack`.
#   4. CORDUM_INTEGRATION=1 exported (no-op otherwise).
#   5. cordumctl on PATH or at CORDUMCTL_BIN.
#   6. CORDUM_API_KEY exported (or inherited from `.env`).
#   7. CORDUM_API_BASE defaults to https://127.0.0.1:8081 (HTTPS with the
#      self-signed cert from `cordumctl generate-certs`).
#
# Exit codes:
#   0  pass — all three verdict paths reached their expected terminal state.
#   1  assertion failed (which amount / which expected state is printed).
#   2  prerequisite missing (cordumctl, pack, jq, API key, no workers).
# =============================================================================
set -euo pipefail

if [[ "${CORDUM_INTEGRATION:-0}" != "1" ]]; then
  echo "demo/mock-bank/integration_test: CORDUM_INTEGRATION=1 required (no-op)."
  exit 0
fi

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cordumctl_bin="${CORDUMCTL_BIN:-${repo_root}/bin/cordumctl}"
if [[ ! -x "${cordumctl_bin}" ]]; then
  if command -v cordumctl >/dev/null 2>&1; then
    cordumctl_bin="$(command -v cordumctl)"
  else
    echo "ERROR: ${cordumctl_bin} not found; build it with 'make build SERVICE=cordumctl' or set CORDUMCTL_BIN." >&2
    exit 2
  fi
fi

pack_dir="${repo_root}/demo/mock-bank/pack"
if [[ ! -f "${pack_dir}/pack.yaml" ]]; then
  echo "ERROR: ${pack_dir}/pack.yaml not found — wrong working directory?" >&2
  exit 2
fi

if ! command -v jq >/dev/null 2>&1; then
  echo "ERROR: jq is required (install via apt/brew). Aborting." >&2
  exit 2
fi

api_base="${CORDUM_API_BASE:-https://127.0.0.1:8081}"
api_key="${CORDUM_API_KEY:-}"
if [[ -z "${api_key}" ]]; then
  echo "ERROR: CORDUM_API_KEY must be set (usually from .env)." >&2
  exit 2
fi

log_file="$(mktemp -t demo-mock-bank-XXXXXX.log)"
trap 'echo "[integration] artifacts at ${log_file}"' EXIT

echo "[integration] installing mock-bank pack ..."
"${cordumctl_bin}" pack install "${pack_dir}" --upgrade

# --- helpers ----------------------------------------------------------------

# curl_api <method> <path>  —  auth-wrapped, tolerates self-signed TLS.
curl_api() {
  local method="$1" path="$2"
  curl -skf -X "${method}" \
    -H "X-API-Key: ${api_key}" \
    -H "Accept: application/json" \
    "${api_base}${path}"
}

# wait_for_workers — block up to WAIT_WORKERS_SEC (default 30) until at
# least one megacorp-transfer-agent-* worker reports online.
wait_for_workers() {
  local timeout="${WAIT_WORKERS_SEC:-30}"
  local deadline=$(( $(date +%s) + timeout ))
  echo "[integration] waiting for transfer agents (up to ${timeout}s) ..."
  while (( $(date +%s) < deadline )); do
    local online
    online=$(curl_api GET "/api/v1/workers" 2>/dev/null \
      | jq -r '[.items[]? | select(.worker_id | startswith("megacorp-transfer-agent-")) | select(.online == true)] | length')
    if [[ "${online:-0}" -ge 1 ]]; then
      echo "[integration]   ${online} transfer agent(s) online."
      return 0
    fi
    sleep 2
  done
  echo "FAIL: no megacorp-transfer-agent-* online after ${timeout}s — demo profile up?" >&2
  return 1
}

# start_run <amount>  —  prints the run id to stdout.
start_run() {
  local amount="$1"
  "${cordumctl_bin}" run start demo-mock-bank.transfer --input \
    "{\"amount\":${amount},\"currency\":\"USD\",\"customer\":\"Alice\",\"reason\":\"integration test\",\"requested_by\":\"qa\"}"
}

# run_json <run_id>  —  JSON run snapshot via cordumctl run get.
run_json() {
  "${cordumctl_bin}" run get "$1"
}

# wait_for_status <run_id> <desired_status> <timeout_s>
# desired_status is a pipe-separated list (e.g. succeeded|failed|denied|require_approval).
wait_for_status() {
  local run_id="$1" want="$2" timeout="$3"
  local deadline=$(( $(date +%s) + timeout ))
  local last=""
  while (( $(date +%s) < deadline )); do
    last=$(run_json "${run_id}" | jq -r '.status // "unknown"')
    if [[ "${last}" =~ ^(${want})$ ]]; then
      echo "${last}"
      return 0
    fi
    sleep 1
  done
  echo "FAIL: run ${run_id} did not reach ${want} within ${timeout}s (last=${last})" >&2
  return 1
}

# wait_for_step_status <run_id> <step_id> <desired_status> <timeout_s>
wait_for_step_status() {
  local run_id="$1" step_id="$2" want="$3" timeout="$4"
  local deadline=$(( $(date +%s) + timeout ))
  local last=""
  while (( $(date +%s) < deadline )); do
    last=$(run_json "${run_id}" | jq -r ".steps.${step_id}.status // \"unknown\"")
    if [[ "${last}" =~ ^(${want})$ ]]; then
      echo "${last}"
      return 0
    fi
    sleep 1
  done
  echo "FAIL: step ${step_id} on run ${run_id} did not reach ${want} within ${timeout}s (last=${last})" >&2
  return 1
}

# --- preflight --------------------------------------------------------------

wait_for_workers || exit 1

run_start_epoch="$(date +%s)"

# --- $40: allow path --------------------------------------------------------

echo "[integration] case A: amount=40 (allow, bank-transfer-allow)"
allow_run_id=$(start_run 40)
echo "[integration]   run_id=${allow_run_id}"
wait_for_status "${allow_run_id}" 'succeeded' 10 >/dev/null || exit 1
step_status=$(run_json "${allow_run_id}" | jq -r '.steps.execute_low.status')
if [[ "${step_status}" != "succeeded" ]]; then
  echo "FAIL: allow path execute_low.status=${step_status}, want succeeded" >&2
  exit 1
fi

# --- $200: require_approval -> succeeded after approve ----------------------

echo "[integration] case B: amount=200 (require_approval, bank-transfer-review)"
review_run_id=$(start_run 200)
echo "[integration]   run_id=${review_run_id}"
wait_for_step_status "${review_run_id}" 'execute_review' 'require_approval|awaiting_approval|approval_pending' 10 >/dev/null || exit 1
review_job_id=$(run_json "${review_run_id}" | jq -r '.steps.execute_review.job_id // empty')
if [[ -z "${review_job_id}" ]]; then
  echo "FAIL: execute_review.job_id missing on run ${review_run_id}" >&2
  exit 1
fi
echo "[integration]   approving job_id=${review_job_id}"
"${cordumctl_bin}" approval job "${review_job_id}" --approve
wait_for_status "${review_run_id}" 'succeeded' 15 >/dev/null || exit 1

# --- $5000: deny ------------------------------------------------------------

echo "[integration] case C: amount=5000 (deny, bank-transfer-blocked)"
deny_run_id=$(start_run 5000)
echo "[integration]   run_id=${deny_run_id}"
wait_for_status "${deny_run_id}" 'denied|failed' 10 >/dev/null || exit 1
# Accept either the canonical 'denied' verdict or a 'failed' run where the
# blocked step carries the rule id. The rule id is the sticky signal.
deny_rule=$(run_json "${deny_run_id}" \
  | jq -r '(.steps.execute_high.violations[0].rule_id
            // .steps.execute_high.reason
            // .steps.execute_high.error.rule_id
            // .error.rule_id
            // "missing")')
if [[ "${deny_rule}" != *"bank-transfer-blocked"* ]]; then
  echo "FAIL: deny path rule id=\"${deny_rule}\", want contains bank-transfer-blocked" >&2
  echo "      run snapshot:" >&2
  run_json "${deny_run_id}" | jq '.' >&2
  exit 1
fi

# --- done ------------------------------------------------------------------

elapsed=$(( $(date +%s) - run_start_epoch ))
echo "[integration] PASS: allow/approval/deny all reached terminal within budget (T=${elapsed}s)"

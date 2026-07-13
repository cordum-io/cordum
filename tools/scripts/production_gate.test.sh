#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
SCRIPT="${ROOT}/tools/scripts/production_gate.sh"
SANDBOX="$(mktemp -d -t production-gate-test.XXXXXX)"
trap 'rm -rf "${SANDBOX}"' EXIT

extract_function() {
  local fn="$1"
  awk -v fn="${fn}" '
    $0 == fn "() {" {emit=1}
    emit {print}
    emit && $0 == "}" {exit}
  ' "${SCRIPT}"
}

extract_full_function() {
  local fn="$1"
  awk -v fn="${fn}" '
    $0 == fn "() {" {emit=1; seen=1}
    emit && seen && $0 != fn "() {" && $0 ~ /^[A-Za-z_][A-Za-z0-9_]*\(\) \{$/ {exit}
    emit {print}
  ' "${SCRIPT}"
}

HELPER="${SANDBOX}/production_gate_functions.sh"
{
  echo 'set -euo pipefail'
  echo 'die() { echo "die: $*" >&2; return 1; }'
  echo 'now_ms() { date +%s%3N; }'
  echo 'sanitize_message() { local msg="${1:-}"; msg="${msg//$'\''\n'\''/ }"; printf "%s" "${msg}"; }'
  extract_function ensure_compose_cmd
  extract_function run_gate
  extract_function cleanup_gate14_snapshot
  extract_function validate_gate14_publish_response
} >"${HELPER}"
# shellcheck source=/dev/null
source "${HELPER}"

declare -A GATE_STATUS
declare -A GATE_DURATION_MS
declare -A GATE_MESSAGE
declare -A GATE_NAME
PASS=0
FAIL=0

assert_eq() {
  local name="$1"
  local got="$2"
  local want="$3"
  if [[ "${got}" == "${want}" ]]; then
    echo "PASS: ${name}"
    PASS=$((PASS + 1))
  else
    echo "FAIL: ${name}: got '${got}', want '${want}'" >&2
    FAIL=$((FAIL + 1))
  fi
}

assert_contains() {
  local name="$1"
  local haystack="$2"
  local needle="$3"
  if [[ "${haystack}" == *"${needle}"* ]]; then
    echo "PASS: ${name}"
    PASS=$((PASS + 1))
  else
    echo "FAIL: ${name}: missing '${needle}' in '${haystack}'" >&2
    FAIL=$((FAIL + 1))
  fi
}

assert_not_contains() {
  local name="$1"
  local haystack="$2"
  local needle="$3"
  if [[ "${haystack}" != *"${needle}"* ]]; then
    echo "PASS: ${name}"
    PASS=$((PASS + 1))
  else
    echo "FAIL: ${name}: unexpected '${needle}' in '${haystack}'" >&2
    FAIL=$((FAIL + 1))
  fi
}

bank_body_fn="$(extract_function bank_validator_job_body)"
assert_contains "bank_validator_job_body uses scoped reliability label" "${bank_body_fn}" 'production_gate: "reliability"'
assert_not_contains "bank_validator_job_body does not spoof reserved source label" "${bank_body_fn}" '"_source": "workflow"'
pid_file_line="$(grep -E '^MOCK_BANK_PID_FILE=' "${SCRIPT}")"
assert_contains "mock-bank PID file is per production_gate process" "${pid_file_line}" 'production-gate-mock-bank.${BASHPID:-$$}.pid'
workflow_fn="$(extract_function create_bank_validator_probe_workflow)"
assert_contains "workflow probe dispatches through workflow source" "${workflow_fn}" 'type: "worker"'
assert_contains "workflow probe targets bank validator topic" "${workflow_fn}" 'topic: "job.bank-validators.process"'
worker_start_fn="$(extract_function ensure_mock_bank_worker)"
assert_contains "mock-bank worker start writes background PID from launcher" "${worker_start_fn}" 'echo "$!" >"${MOCK_BANK_PID_FILE}"'
assert_not_contains "mock-bank worker start avoids command substitution hang" "${worker_start_fn}" 'MOCK_BANK_WORKER_PID="$(cd "${ROOT_DIR}"'
assert_contains "mock-bank worker default does not trust stale registry entries" "${worker_start_fn}" 'CORDUM_PRODUCTION_GATE_REUSE_MOCK_BANK_WORKER'
cleanup_fn="$(extract_function cleanup)"
assert_contains "mock-bank cleanup only kills owned worker" "${cleanup_fn}" 'MOCK_BANK_WORKER_STARTED:-0'

gate_4_fn="$(extract_full_function gate_4_policy)"
assert_contains "gate 4 invokes non-executable remediation script through bash" "${gate_4_fn}" 'bash "${SCRIPT_DIR}/demo_guardrails_run.sh"'

ensure_agent_fn="$(extract_full_function ensure_mcp_gate_agent)"
assert_contains "gate 8 agent creation includes bounded status/body diagnostics" "${ensure_agent_fn}" 'format_api_failure'

gate_14_fn="$(extract_full_function gate_14_policy_lifecycle)"
assert_contains "gate 14 publishes the selected existing bundle explicitly" "${gate_14_fn}" 'bundle_ids'
assert_contains "gate 14 publish failure includes bounded status/body diagnostics" "${gate_14_fn}" 'format_api_failure'
assert_contains "gate 14 arms rollback before publish" "${gate_14_fn}" 'trap cleanup_gate14_snapshot EXIT'
assert_contains "gate 14 verifies the selected bundle became active" "${gate_14_fn}" 'validate_gate14_publish_response'
assert_contains "gate 14 verifies an observable publish marker" "${gate_14_fn}" 'bundle_message}" == "${publish_marker}'

if declare -F cleanup_gate14_snapshot >/dev/null 2>&1; then
  cleanup_log="${SANDBOX}/gate14-cleanup.log"
  cleanup() { echo cleanup >>"${cleanup_log}"; }
  rollback_gate14_snapshot() { echo "rollback:$1" >>"${cleanup_log}"; return "${ROLLBACK_RC}"; }

  : >"${cleanup_log}"
  GATE14_ROLLBACK_SNAPSHOT_ID="snapshot-ok"
  ROLLBACK_RC=0
  cleanup_gate14_snapshot
  assert_eq "gate 14 exit cleanup rolls back an armed snapshot" \
    "$(head -n 1 "${cleanup_log}")" "rollback:snapshot-ok"
  assert_eq "gate 14 exit cleanup disarms after successful rollback" \
    "${GATE14_ROLLBACK_SNAPSHOT_ID}" ""
  assert_eq "gate 14 exit cleanup preserves general cleanup" \
    "$(tail -n 1 "${cleanup_log}")" "cleanup"

  : >"${cleanup_log}"
  GATE14_ROLLBACK_SNAPSHOT_ID="snapshot-failed"
  ROLLBACK_RC=1
  cleanup_gate14_snapshot 2>/dev/null
  assert_eq "gate 14 retains failed rollback for diagnostics" \
    "${GATE14_ROLLBACK_SNAPSHOT_ID}" "snapshot-failed"
else
  echo "FAIL: gate 14 cleanup behavior: cleanup_gate14_snapshot is not defined" >&2
  FAIL=$((FAIL + 1))
fi

if declare -F validate_gate14_publish_response >/dev/null 2>&1; then
  valid_publish='{"published":["secops/output"],"snapshot_before":"before","snapshot_after":"after"}'
  validate_gate14_publish_response "${valid_publish}" "secops/output" >/dev/null
  assert_eq "gate 14 accepts a real selected-bundle mutation" "$?" "0"
  invalid_publish='{"published":["other"],"snapshot_before":"same","snapshot_after":"same"}'
  invalid_rc=0
  validate_gate14_publish_response "${invalid_publish}" "secops/output" >/dev/null 2>&1 || invalid_rc=$?
  assert_eq "gate 14 rejects a publish no-op response" "${invalid_rc}" "1"
else
  echo "FAIL: gate 14 publish validation: validate_gate14_publish_response is not defined" >&2
  FAIL=$((FAIL + 1))
fi

gate_16_fn="$(extract_full_function gate_16_degradation)"
assert_contains "gate 16 key creation failure includes bounded status/body diagnostics" "${gate_16_fn}" 'format_api_failure'
assert_contains "gate 16 never logs a successful credential response" "${gate_16_fn}" 'credential response omitted because it may contain an API key'

gate_19_fn="$(extract_full_function gate_19_ha)"
assert_not_contains "gate 19 avoids errexit-unsafe post-increment expressions" "${gate_19_fn}" '++'
assert_contains "gate 19 submits policy-scoped validator jobs" "${gate_19_fn}" 'bank_validator_job_body'
assert_not_contains "gate 19 does not count denied or failed jobs as HA success" "${gate_19_fn}" 'SUCCEEDED|FAILED|DENIED|CANCELLED|TIMEOUT|OUTPUT_QUARANTINED)'
scheduler_replica_block="$(printf '%s\n' "${gate_19_fn}" | awk '
  /if \(\( sched_count < 2 \)\); then/ {emit=1}
  emit {print}
  emit && /^    fi$/ {exit}
')"
assert_contains "gate 19 fails when the second scheduler replica is absent" \
  "${scheduler_replica_block}" 'ha_failed=1'

gate_errexit_probe() {
  echo "before failure"
  false
  echo "after failure"
}

gate_explicit_failure() {
  echo "stdout detail"
  echo "stderr detail" >&2
  return 17
}

stream_file="${SANDBOX}/run-gate-stream.log"
run_gate 42 gate_errexit_probe "Errexit Probe" >"${stream_file}" 2>&1
stream_output="$(cat "${stream_file}")"
assert_eq "run_gate marks middle-command failure as FAIL" "${GATE_STATUS[42]}" "FAIL"
assert_contains "run_gate streams output before failure" "${stream_output}" "before failure"
assert_not_contains "run_gate stops after failing command" "${stream_output}" "after failure"
assert_contains "run_gate stores failure message" "${GATE_MESSAGE[42]}" "before failure"

run_gate 43 gate_explicit_failure "Explicit Failure" >"${stream_file}" 2>&1
stream_output="$(cat "${stream_file}")"
assert_eq "run_gate marks explicit nonzero return as FAIL" "${GATE_STATUS[43]}" "FAIL"
assert_contains "run_gate streams stdout" "${stream_output}" "stdout detail"
assert_contains "run_gate streams stderr" "${stream_output}" "stderr detail"
assert_contains "run_gate captures stderr in message" "${GATE_MESSAGE[43]}" "stderr detail"

FAKE_BIN="${SANDBOX}/bin"
mkdir -p "${FAKE_BIN}"
cat >"${FAKE_BIN}/docker" <<'FAKEDOCKER'
#!/usr/bin/env bash
set -euo pipefail
if [[ "${1:-}" == "compose" && "${2:-}" == "version" ]]; then
  exit 0
fi
printf '%s\n' "$*" >>"${FAKE_DOCKER_LOG}"
FAKEDOCKER
chmod +x "${FAKE_BIN}/docker"

PATH="${FAKE_BIN}:${PATH}"
FAKE_DOCKER_LOG="${SANDBOX}/docker.log"
export FAKE_DOCKER_LOG

ROOT_DIR="/repo/root"
COMPOSE_CMD=()
CORDUM_COMPOSE_PROJECT_DIR="/repo/root"
CORDUM_COMPOSE_PROJECT_NAME="porttest"
CORDUM_COMPOSE_FILES="docker-compose.porttest.yml;docker-compose.ci.yml"
ensure_compose_cmd
compose_joined="$(printf '<%s>' "${COMPOSE_CMD[@]}")"
assert_eq "ensure_compose_cmd uses docker compose" "${COMPOSE_CMD[0]} ${COMPOSE_CMD[1]}" "docker compose"
assert_contains "ensure_compose_cmd keeps project name" "${compose_joined}" "<--project-name><porttest>"
assert_contains "ensure_compose_cmd adds first compose file" "${compose_joined}" "<-f><docker-compose.porttest.yml>"
assert_contains "ensure_compose_cmd adds second compose file" "${compose_joined}" "<-f><docker-compose.ci.yml>"

COMPOSE_CMD=()
unset CORDUM_COMPOSE_PROJECT_NAME CORDUM_COMPOSE_FILES
COMPOSE_FILE='D:\repo\docker-compose.porttest.yml'
ensure_compose_cmd
compose_joined="$(printf '<%s>' "${COMPOSE_CMD[@]}")"
assert_contains "ensure_compose_cmd preserves Windows drive path" "${compose_joined}" '<-f><D:\repo\docker-compose.porttest.yml>'

echo "SUMMARY: ${PASS} pass, ${FAIL} fail"
if [[ "${FAIL}" -gt 0 ]]; then
  exit 1
fi

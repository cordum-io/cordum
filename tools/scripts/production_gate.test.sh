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

HELPER="${SANDBOX}/production_gate_functions.sh"
{
  echo 'set -euo pipefail'
  echo 'die() { echo "die: $*" >&2; return 1; }'
  echo 'now_ms() { date +%s%3N; }'
  echo 'sanitize_message() { local msg="${1:-}"; msg="${msg//$'\''\n'\''/ }"; printf "%s" "${msg}"; }'
  extract_function ensure_compose_cmd
  extract_function run_gate
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

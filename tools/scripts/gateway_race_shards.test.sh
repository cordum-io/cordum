#!/usr/bin/env bash
# Synthetic contract tests for tools/scripts/gateway_race_shards.sh.
#
# The real shard runner is intentionally exercised with a fake `go` binary so
# these tests never invoke the race detector or the network. The fake exposes a
# stable test list and records every `go test` invocation, allowing the harness
# to assert deterministic sharding, focused binary-integrity coverage, dry-run
# behavior, and failure messages that identify the failing shard.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
SHARD_SCRIPT="${REPO_ROOT}/tools/scripts/gateway_race_shards.sh"

if [[ ! -f "${SHARD_SCRIPT}" ]]; then
  echo "FAIL: shard script not found at ${SHARD_SCRIPT}" >&2
  exit 1
fi

SANDBOX="$(mktemp -d -t gateway-race-shards-test.XXXXXX)"
trap 'rm -rf "${SANDBOX}"' EXIT

FAKE_BIN="${SANDBOX}/bin"
FAKE_GO_LOG="${SANDBOX}/fake-go.log"
mkdir -p "${FAKE_BIN}"
cat >"${FAKE_BIN}/go" <<'FAKEGO'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"${FAKE_GO_LOG}"

if [[ "$*" == *" -list ^Test"* || "$*" == *" -list '^Test'"* ]]; then
  cat <<'TESTS'
TestZulu
TestGamma
TestAlpha
TestBinaryIntegrity_MaxBytesErrorTypedCheck
BenchmarkIgnored
TESTS
  exit 0
fi

regex=""
while [[ "$#" -gt 0 ]]; do
  case "$1" in
    -run)
      shift
      regex="${1:-}"
      ;;
  esac
  shift || true
done

echo "{\"Action\":\"run\",\"Test\":\"${regex}\"}"
if [[ "${regex}" == *"TestGamma"* ]]; then
  echo "signal: killed" >&2
  exit 99
fi
echo "PASS"
exit 0
FAKEGO
chmod +x "${FAKE_BIN}/go"

PASS=0
FAIL=0

run_case() {
  local name="$1"
  local expected_exit="$2"
  local expected_grep="$3"
  shift 3

  echo "--- ${name} ---"
  local out_file
  out_file="$(mktemp -t gateway-race-shards-out.XXXXXX)"
  : >"${FAKE_GO_LOG}"

  local actual_exit=0
  PATH="${FAKE_BIN}:${PATH}" \
    FAKE_GO_LOG="${FAKE_GO_LOG}" \
    "$@" >"${out_file}" 2>&1 || actual_exit=$?

  local case_pass=1
  if [[ "${actual_exit}" -ne "${expected_exit}" ]]; then
    echo "  FAIL: exit ${actual_exit} != expected ${expected_exit}"
    cat "${out_file}"
    case_pass=0
  fi
  if [[ -n "${expected_grep}" ]] && ! grep -qE "${expected_grep}" "${out_file}"; then
    echo "  FAIL: stdout/stderr did not match /${expected_grep}/"
    cat "${out_file}"
    case_pass=0
  fi

  if [[ "${case_pass}" -eq 1 ]]; then
    echo "  PASS"
    PASS=$((PASS + 1))
  else
    FAIL=$((FAIL + 1))
  fi
  rm -f "${out_file}"
}

assert_log_matches() {
  local name="$1"
  local expected="$2"

  echo "--- ${name} ---"
  if grep -qE "${expected}" "${FAKE_GO_LOG}"; then
    echo "  PASS"
    PASS=$((PASS + 1))
  else
    echo "  FAIL: fake go log missing /${expected}/"
    cat "${FAKE_GO_LOG}"
    FAIL=$((FAIL + 1))
  fi
}

LOG_DIR="${SANDBOX}/logs with spaces"
mkdir -p "${LOG_DIR}"

run_case "T1 list-tests sorts only Test names" 0 \
  "TestAlpha.*TestBinaryIntegrity_MaxBytesErrorTypedCheck.*TestGamma.*TestZulu" \
  env LOG_DIR="${LOG_DIR}" bash "${SHARD_SCRIPT}" --list-tests

run_case "T2 dry-run shard 1/2 uses stable index split" 0 \
  "DRY-RUN shard 1/2.*TestAlpha.*TestGamma" \
  env TOTAL_SHARDS=2 LOG_DIR="${LOG_DIR}" bash "${SHARD_SCRIPT}" --dry-run --shard 1/2

run_case "T3 dry-run all emits every shard" 0 \
  "DRY-RUN shard 1/2.*DRY-RUN shard 2/2" \
  env TOTAL_SHARDS=2 COUNT=7 TIMEOUT=12m LOG_DIR="${LOG_DIR}" bash "${SHARD_SCRIPT}" --dry-run --all

run_case "T4 focused binary-integrity dry-run is exact" 0 \
  "DRY-RUN focused-binary-integrity.*TestBinaryIntegrity_MaxBytesErrorTypedCheck" \
  env LOG_DIR="${LOG_DIR}" bash "${SHARD_SCRIPT}" --dry-run --focused-binary-integrity

run_case "T5 shard 2/2 succeeds and names log path" 0 \
  "PASS shard 2/2.*log=.*/shard-2-of-2.jsonl" \
  env TOTAL_SHARDS=2 LOG_DIR="${LOG_DIR}" bash "${SHARD_SCRIPT}" --shard 2/2
assert_log_matches "T5 fake go received anchored shard regex" \
  "test -race ./core/controlplane/gateway -run \\^\\(TestBinaryIntegrity_MaxBytesErrorTypedCheck\\|TestZulu\\)\\$ -count=3 -timeout=15m -json"

run_case "T6 failing shard exits nonzero and names shard" 99 \
  "FAIL shard 1/2.*exit=99.*log=.*/shard-1-of-2.jsonl" \
  env TOTAL_SHARDS=2 LOG_DIR="${LOG_DIR}" bash "${SHARD_SCRIPT}" --shard 1/2

run_case "T7 failing all exits nonzero and names first failed shard" 99 \
  "FAIL first_failed_shard=1/2 exit=99 log_dir=.*/logs with spaces" \
  env TOTAL_SHARDS=2 LOG_DIR="${LOG_DIR}" bash "${SHARD_SCRIPT}" --all

run_case "T8 invalid shard is rejected before go test" 2 \
  "invalid --shard" \
  env LOG_DIR="${LOG_DIR}" bash "${SHARD_SCRIPT}" --shard 0/2

echo ""
echo "==== SUMMARY: ${PASS} pass, ${FAIL} fail ===="
if [[ "${FAIL}" -gt 0 ]]; then
  exit 1
fi
exit 0

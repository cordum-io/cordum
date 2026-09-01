#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
INTEGRATION="${ROOT}/.github/workflows/integration-nightly.yml"
NIGHTLY="${ROOT}/.github/workflows/nightly.yml"
SOAK="${ROOT}/tools/scripts/soak_test.sh"
CI="${ROOT}/.github/workflows/ci.yml"
HELPER="${ROOT}/tools/scripts/github_actions_mask_env.sh"
GUARD="${ROOT}/tools/scripts/workflow_credential_guard.py"
TEST_TMP="$(mktemp -d)"
PASS=0; FAIL=0
trap 'rm -rf "${TEST_TMP}"' EXIT
record() {
  local name="$1" status="$2"
  if [[ "${status}" -eq 0 ]]; then
    echo "PASS: ${name}"
    PASS=$((PASS + 1))
  else
    echo "FAIL: ${name}" >&2
    FAIL=$((FAIL + 1))
  fi
}
assert_count() {
  local name="$1" file="$2" pattern="$3" want="$4" got status=0
  got="$(grep -cF -- "${pattern}" "${file}" || true)"
  [[ "${got}" == "${want}" ]] || status=1
  record "${name}" "${status}"
}
new_value() {
  local tag="$1" suffix="${2:-}"
  VALUE="gha_test_${tag}_${$}_${RANDOM}_${RANDOM}${suffix}"
  printf '%s\n' "${VALUE}" >> "${CASE_SENTINELS}"
}
mask_count() {
  local value="$1" escaped; escaped="${value//%/%25}"
  grep -Fxc -- "::add-mask::${escaped}" "${HELPER_LOG}" 2>/dev/null || true
}
call_ok() { "$@" >> "${HELPER_LOG}" 2>&1; }
call_fails() {
  local status
  set +e
  "$@" >> "${HELPER_LOG}" 2>&1
  status=$?
  set -e
  [[ "${status}" -ne 0 && "${status}" -ne 127 ]]
}
setup_env_file() { export GITHUB_ENV="${CASE_DIR}/github-env"; : > "${GITHUB_ENV}"; }
valid_helper_case() {
  local -a names=(CORDUM_API_KEY CORDUM_APPROVER_API_KEY REDIS_PASSWORD CORDUM_ADMIN_PASSWORD CORDUM_API_KEYS) values=()
  local name value mask_only mode artifact_dir="${CASE_DIR}/artifacts" artifact="${CASE_DIR}/artifacts/nested/log.txt"
  mkdir -p "${artifact_dir}/nested"
  setup_env_file
  if [[ "${GITHUB_ACTIONS:-}" == "true" && -x /usr/bin/python3 ]]; then
    [[ "${GHA__PYTHON}" == /usr/bin/python* && ! -L "${GHA__PYTHON}" ]] || return 1
  fi
  for name in "${names[@]}"; do new_value "${name}"; values+=("${VALUE}"); done
  new_value mask_only '%pct'; mask_only="${VALUE}"
  new_value license_token '=='; export TEST_LICENSE_TOKEN="${VALUE}"; names+=(CORDUM_LICENSE_TOKEN); values+=("${VALUE}")
  new_value license_public; export TEST_LICENSE_PUBLIC="${VALUE}"; names+=(CORDUM_LICENSE_PUBLIC_KEY); values+=("${VALUE}")
  call_ok gha_mask_value "${mask_only}" || return 1
  for ((i=0; i<5; i++)); do call_ok gha_mask_env "${names[i]}" "${values[i]}" || return 1; done
  call_ok gha_mask_env_from_command CORDUM_LICENSE_TOKEN CORDUM_LICENSE_PUBLIC_KEY -- \
    bash -c 'printf "CORDUM_LICENSE_TOKEN=%s\nCORDUM_LICENSE_PUBLIC_KEY=%s\n" "$TEST_LICENSE_TOKEN" "$TEST_LICENSE_PUBLIC"' || return 1
  for value in "${values[@]}"; do printf '%s\n' "${value}" >> "${artifact}"; done
  mode="$(stat -c '%a' "${artifact}")"
  call_ok gha_redact_paths "${names[@]}" -- "${artifact_dir}" || return 1
  call_ok gha_redact_paths "${names[@]}" -- "${artifact_dir}" || return 1
  [[ "$(stat -c '%a' "${artifact}")" == "${mode}" ]] || return 1
  [[ "$(wc -l < "${GITHUB_ENV}")" -eq 7 ]] || return 1
  for ((i=0; i<7; i++)); do
    name="${names[i]}"; value="${values[i]}"
    grep -Fxq -- "${name}=${value}" "${GITHUB_ENV}" 2>/dev/null || return 1
    [[ "${!name-}" == "${value}" ]] || return 1
    [[ "$(mask_count "${value}")" -eq 1 ]] || return 1
    ! grep -Fq -- "${value}" "${artifact}" 2>/dev/null || return 1
  done
  [[ "$(mask_count "${mask_only}")" -eq 1 ]] || return 1
  new_value fallback_redaction; unset CORDUM_API_KEY; export CORDUM_API_KEY="${VALUE}"; printf '%s\n' "${VALUE}" > "${artifact}"
  call_ok gha_redact_paths CORDUM_API_KEY -- "${artifact}" && ! grep -Fq -- "${VALUE}" "${artifact}" 2>/dev/null
}
batch_failure_case() {
  local kind="$1" command
  setup_env_file; new_value "batch_${kind}"; export TEST_BATCH_VALUE="${VALUE}"
  case "${kind}" in
    command) command='printf "CORDUM_LICENSE_TOKEN=%s\n" "$TEST_BATCH_VALUE"; printf "%s" "$TEST_BATCH_VALUE" >&2; exit 9' ;;
    malformed) command='printf "%s\n" "$TEST_BATCH_VALUE"' ;;
    missing) command='printf "CORDUM_LICENSE_TOKEN=%s\n" "$TEST_BATCH_VALUE"' ;;
    extra) command='printf "CORDUM_LICENSE_TOKEN=%s\nCORDUM_LICENSE_PUBLIC_KEY=%s\nEXTRA_NAME=%s\n" "$TEST_BATCH_VALUE" "$TEST_BATCH_VALUE" "$TEST_BATCH_VALUE"' ;;
    duplicate) command='printf "CORDUM_LICENSE_TOKEN=%s\nCORDUM_LICENSE_TOKEN=%s\nCORDUM_LICENSE_PUBLIC_KEY=%s\n" "$TEST_BATCH_VALUE" "$TEST_BATCH_VALUE" "$TEST_BATCH_VALUE"' ;;
  esac
  call_fails gha_mask_env_from_command CORDUM_LICENSE_TOKEN CORDUM_LICENSE_PUBLIC_KEY -- bash -c "${command}" || return 1
  [[ ! -s "${GITHUB_ENV}" && "$(mask_count "${VALUE}")" -eq 0 ]]
}
helper_case() {
  local kind="$1" bad path
  source "${HELPER}" >> "${HELPER_LOG}" 2>&1 || return 1
  for fn in gha_mask_value gha_mask_env gha_mask_env_from_command gha_redact_paths; do
    declare -F "${fn}" >/dev/null || return 1
  done
  case "${kind}" in
    valid) valid_helper_case ;;
    empty_name) setup_env_file; new_value empty_name; call_fails gha_mask_env '' "${VALUE}" ;;
    invalid_name) setup_env_file; new_value invalid_name; call_fails gha_mask_env 'BAD-NAME' "${VALUE}" ;;
    cr_name) setup_env_file; new_value cr_name; call_fails gha_mask_env $'BAD\rNAME' "${VALUE}" ;;
    lf_name) setup_env_file; new_value lf_name; call_fails gha_mask_env $'BAD\nNAME' "${VALUE}" ;;
    empty_value) setup_env_file; call_fails gha_mask_env CORDUM_API_KEY '' ;;
    extra_value) new_value extra_value; call_fails gha_mask_value "${VALUE}" unexpected ;;
    extra_env) setup_env_file; new_value extra_env; call_fails gha_mask_env CORDUM_API_KEY "${VALUE}" unexpected ;;
    cr_value) setup_env_file; new_value cr_value; bad="${VALUE}"$'\rtail'; call_fails gha_mask_env CORDUM_API_KEY "${bad}" ;;
    lf_value) setup_env_file; new_value lf_value; bad="${VALUE}"$'\ntail'; call_fails gha_mask_env CORDUM_API_KEY "${bad}" ;;
    unset_env) unset GITHUB_ENV; new_value unset_env; call_fails gha_mask_env CORDUM_API_KEY "${VALUE}" ;;
    bad_env) export GITHUB_ENV="${CASE_DIR}/missing/env"; new_value bad_env; call_fails gha_mask_env CORDUM_API_KEY "${VALUE}" ;;
    write_failure)
      export GITHUB_ENV=/dev/full; [[ -e "${GITHUB_ENV}" ]] || export GITHUB_ENV="${CASE_DIR}"
      new_value write_failure; unset CORDUM_API_KEY
      call_fails gha_mask_env CORDUM_API_KEY "${VALUE}" || return 1
      [[ "$(mask_count "${VALUE}")" -eq 1 && -z "${CORDUM_API_KEY+x}" ]]
      ;;
    command|malformed|missing|extra|duplicate) batch_failure_case "${kind}" ;;
    import_side_effect)
      setup_env_file
      call_fails gha_mask_env_from_command CORDUM_LICENSE_TOKEN CORDUM_LICENSE_PUBLIC_KEY -- \
        bash -c 'printf "UNSAFE_SIDE_EFFECT=1\n" >> "${GITHUB_ENV}"; exit 23' || return 1
      [[ ! -s "${GITHUB_ENV}" ]]
      ;;
    import_swap)
      setup_env_file; local external="${CASE_DIR}/external"
      : > "${external}"; new_value import_swap_one; export TEST_IMPORT_ONE="${VALUE}"
      new_value import_swap_two; export TEST_IMPORT_TWO="${VALUE}"
      export TEST_IMPORT_ENV="${GITHUB_ENV}" TEST_IMPORT_EXTERNAL="${external}"
      call_fails gha_mask_env_from_command CORDUM_LICENSE_TOKEN CORDUM_LICENSE_PUBLIC_KEY -- bash -c \
        'rm -f "${TEST_IMPORT_ENV}"; ln -s "${TEST_IMPORT_EXTERNAL}" "${TEST_IMPORT_ENV}"; printf "CORDUM_LICENSE_TOKEN=%s\nCORDUM_LICENSE_PUBLIC_KEY=%s\n" "${TEST_IMPORT_ONE}" "${TEST_IMPORT_TWO}"' || return 1
      [[ ! -s "${external}" ]]
      ;;
    redact_missing)
      new_value redact_missing; export CORDUM_API_KEY="${VALUE}"; path="${CASE_DIR}/absent"
      call_fails gha_redact_paths CORDUM_API_KEY -- "${path}"
      ;;
    redact_bad_name)
      new_value redact_bad_name; export CORDUM_API_KEY="${VALUE}"; path="${CASE_DIR}/artifact"; : > "${path}"
      call_fails gha_redact_paths 'BAD-NAME' -- "${path}"
      ;;
    redact_binary) new_value redact_binary; export CORDUM_API_KEY="${VALUE}"; path="${CASE_DIR}/binary"; printf '\0%s' "${VALUE}" > "${path}"; call_fails gha_redact_paths CORDUM_API_KEY -- "${path}" ;;
    redact_symlink)
      new_value redact_symlink; export CORDUM_API_KEY="${VALUE}"; mkdir -p "${CASE_DIR}/artifacts"; printf '%s\n' "${VALUE}" > "${CASE_DIR}/external"
      if ln -s "${CASE_DIR}/external" "${CASE_DIR}/artifacts/link" 2>/dev/null && [[ -L "${CASE_DIR}/artifacts/link" ]]; then call_fails gha_redact_paths CORDUM_API_KEY -- "${CASE_DIR}/artifacts"; else true; fi
      ;;
    redact_path_hijack)
      setup_env_file; new_value redact_path_hijack; export CORDUM_API_KEY="${VALUE}"; path="${CASE_DIR}/artifact"; printf '%s\n' "${VALUE}" > "${path}"
      mkdir -p "${CASE_DIR}/fake-bin"; printf '#!/usr/bin/env bash\nexit 0\n' > "${CASE_DIR}/fake-bin/python"; cp "${CASE_DIR}/fake-bin/python" "${CASE_DIR}/fake-bin/python3"; chmod +x "${CASE_DIR}/fake-bin/python" "${CASE_DIR}/fake-bin/python3"
      local old_path="${PATH}"; PATH="${CASE_DIR}/fake-bin:${PATH}"; call_ok gha_redact_paths CORDUM_API_KEY -- "${path}"; PATH="${old_path}"
      ! grep -Fq -- "${VALUE}" "${path}" 2>/dev/null
      ;;
    redact_pythonpath_hijack)
      setup_env_file; new_value redact_pythonpath_hijack; export CORDUM_API_KEY="${VALUE}"; path="${CASE_DIR}/artifact"; printf '%s\n' "${VALUE}" > "${path}"
      mkdir -p "${CASE_DIR}/python-startup"; printf 'import os\nos._exit(0)\n' > "${CASE_DIR}/python-startup/sitecustomize.py"
      PYTHONPATH="${CASE_DIR}/python-startup" call_ok gha_redact_paths CORDUM_API_KEY -- "${path}" || return 1
      ! grep -Fq -- "${VALUE}" "${path}" 2>/dev/null
      ;;
    redact_marker_collision)
      setup_env_file; VALUE='[REDACTED]'; printf '%s\n' "${VALUE}" >> "${CASE_SENTINELS}"; export CORDUM_API_KEY="${VALUE}"; path="${CASE_DIR}/artifact"; printf '%s\n' "${VALUE}" > "${path}"
      call_ok gha_redact_paths CORDUM_API_KEY -- "${path}" && ! grep -Fq -- "${VALUE}" "${path}" 2>/dev/null
      ;;
    redact_hardlink)
      setup_env_file; new_value redact_hardlink; export CORDUM_API_KEY="${VALUE}"; path="${CASE_DIR}/artifact"; local alias="${CASE_DIR}/alias"; printf '%s\n' "${VALUE}" > "${path}"
      if ln "${path}" "${alias}" 2>/dev/null && [[ "$(stat -c '%h' "${path}" 2>/dev/null || echo 1)" -gt 1 ]]; then call_fails gha_redact_paths CORDUM_API_KEY -- "${path}"; else true; fi
      ;;
  esac
}
logs_are_safe() {
  local value count
  [[ ! -s "${CASE_STDIO}" ]] || return 1
  while IFS= read -r value; do
    count="$(grep -Fc -- "${value}" "${HELPER_LOG}" 2>/dev/null || true)"
    [[ "${count}" -le 1 ]] || return 1
    [[ "${count}" -eq 0 ]] || grep -Fxq -- "::add-mask::${value}" "${HELPER_LOG}" 2>/dev/null || return 1
  done < "${CASE_SENTINELS}"
}
run_helper_case() {
  local name="$1" kind="$2" status=0
  CASE_DIR="${TEST_TMP}/${kind}"; mkdir -p "${CASE_DIR}"
  CASE_SENTINELS="${CASE_DIR}/sentinels"; HELPER_LOG="${CASE_DIR}/helper.log"; CASE_STDIO="${CASE_DIR}/stdio.log"
  : > "${CASE_SENTINELS}"; : > "${HELPER_LOG}"; : > "${CASE_STDIO}"
  (helper_case "${kind}") > "${CASE_STDIO}" 2>&1 || status=1
  logs_are_safe || status=1
  record "${name}" "${status}"
}
run_quiet() {
  local name="$1"; shift
  local output="${TEST_TMP}/quiet-${PASS}-${FAIL}.log" status=0
  "$@" > "${output}" 2>&1 || status=1
  record "${name}" "${status}"
}
assert_count "integration enables managed-key storage" "${INTEGRATION}" 'CORDUM_USER_AUTH_ENABLED=true' 1
assert_count "all nightly service jobs enable managed-key storage" "${NIGHTLY}" 'CORDUM_USER_AUTH_ENABLED=true' 3
assert_count "nightly labels the complete 21-gate suite accurately" "${NIGHTLY}" 'Full Production Gate (21 gates)' 1
assert_count "nightly labels the complete gate step accurately" "${NIGHTLY}" 'Run all production gates (1-21)' 1
assert_count "nightly avoids duplicate setup-go and actions/cache restores" "${NIGHTLY}" 'cache: false' 3
assert_count "integration tests isolate fixture license environment" "${INTEGRATION}" 'env -u CORDUM_LICENSE_TOKEN -u CORDUM_LICENSE_PUBLIC_KEY go test -v -tags=integration -timeout 10m ./...' 1
assert_count "nightly soak uses the TLS gateway URL" "${NIGHTLY}" 'CORDUM_API_BASE: https://localhost:8081/api/v1' 1
assert_count "nightly soak trusts the generated CA" "${NIGHTLY}" 'CORDUM_TLS_CA: certs/ca/ca.crt' 1
assert_count "soak requests apply resolved TLS options" "${SOAK}" '"${CURL_TLS_OPTS[@]}"' 2
assert_count "soak supports an explicit TLS CA" "${SOAK}" '--cacert "${TLS_CA}"' 1
assert_count "CI runs soak analysis regression tests" "${CI}" 'bash tools/scripts/soak_test_lib.test.sh' 1
assert_count "strict shared-runner gate excludes hardware-dependent gate 6" "${NIGHTLY}" '--skip-rebuild --strict --exclude-gate 6' 1
assert_count "nightly keeps gate 6 as a visible advisory probe" "${NIGHTLY}" 'RESULTS_FILE=performance_gate_results.json bash tools/scripts/production_gate.sh --gate 6 --skip-rebuild --strict' 1
assert_count "shared-runner performance probe is explicitly nonblocking" "${NIGHTLY}" 'continue-on-error: true' 1
assert_count "nightly does not launder the production p95 threshold" "${NIGHTLY}" 'PERF_P95_MS:' 0
assert_count "release artifacts redact and retain the advisory performance result" "${NIGHTLY}" 'performance_gate_results.json' 3
if [[ ! -f "${HELPER}" ]]; then
  record "masking helper API exists" 1
else
  run_helper_case "helper masks, exports, imports, and redacts every credential class" valid
  for kind in empty_name invalid_name cr_name lf_name empty_value extra_value extra_env cr_value lf_value unset_env bad_env write_failure command malformed missing extra duplicate import_side_effect import_swap redact_missing redact_bad_name redact_binary redact_symlink redact_path_hijack redact_pythonpath_hijack redact_marker_collision redact_hardlink; do
    run_helper_case "helper rejects ${kind//_/ } without leaking" "${kind}"
  done
fi
run_quiet "workflow guard detects all unsafe mutations" python "${GUARD}" --self-test
run_quiet "all active workflows satisfy masking and redaction contract" python "${GUARD}" "${ROOT}"
echo "SUMMARY: ${PASS} pass, ${FAIL} fail"; [[ "${FAIL}" -eq 0 ]]

#!/usr/bin/env bash
# Runtime credential masking contract for production_gate.sh and e2e_test.sh:
# the exact extracted gate/run_gate/results functions, the full script for
# advisory gate 16, and the e2e admin login run against a stubbed HTTP layer.
# Credentials are unique synthetic sentinels; absence is asserted by grep -F.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
PG="${ROOT}/tools/scripts/production_gate.sh"
E2E="${ROOT}/tools/scripts/e2e_test.sh"
SANDBOX="$(mktemp -d -t runtime-credential-masking.XXXXXX)"
trap 'rm -rf "${SANDBOX}"' EXIT
export TMPDIR="${SANDBOX}" GITHUB_ACTIONS=true
export STATE="${SANDBOX}/state" REQ_LOG="${SANDBOX}/requests" UNREG="${SANDBOX}/unregistered"
export MASK_FILE="" INJECT_AT="" INJECT_CODE="" MULTILINE=""
PASS=0 FAIL=0

# Synthetic sentinels. Gate 9 builds its passwords from `date +%s`; the stub's
# Nth call returns ${TAG}dN (call 1 is the non-secret user name).
export TAG="rcm${RANDOM}${RANDOM}x$$"
export S1="sxa-${TAG}" S2="sxb-${TAG}" S3="sxc-${TAG}" K9="kxa-${TAG}" K16="kxb-${TAG}"
P1="PgT3stSecure#${TAG}d2" P2="ViewerPass#${TAG}d3" P3="NewPgSecure#${TAG}d4" E2E_S="exs-${TAG}"
export SECRETS="${S1} ${S2} ${S3} ${K9} ${K16} ${P1} ${P2} ${P3}" LEAK="${S1} ${S2} ${S3} ${K9} ${K16}"
NEEDLES=("${TAG}d2" "${TAG}d3" "${TAG}d4")
for v in "${S1}" "${S2}" "${S3}" "${K9}" "${K16}" "${E2E_S}"; do NEEDLES+=("${v}" "${v:0:8}"); done

expect() {
  local name="$1"; shift
  if "$@"; then echo "PASS: ${name}"; PASS=$((PASS + 1)); else echo "FAIL: ${name}" >&2; FAIL=$((FAIL + 1)); fi
}
rc_of() { local rc=0; "$@" >/dev/null 2>&1 || rc=$?; echo "${rc}"; }
rc_is() { local want="$1"; shift; [[ "$(rc_of "$@")" == "${want}" ]]; }
# sealed FILE...: every file exists and holds no sentinel and no ::add-mask::.
sealed() {
  local f needle
  for f in "$@"; do [[ -e "${f}" ]] || return 1; done
  for needle in "${NEEDLES[@]}" "::add-mask::"; do if grep -qF -- "${needle}" "$@"; then return 1; fi; done
}
registered_once() { [[ "$(grep -cF -- "$2" "$1")" == 1 && "$(grep -cxF -- "::add-mask::$2" "$1")" == 1 ]]; }

# --- Stubbed HTTP layer and tools --------------------------------------------
# curl logs each request, records any sentinel it carries before MASK_FILE
# registers it, and answers the INJECT_AT call ("METHOD /path@N") with
# INJECT_CODE and a body that echoes the request and every sentinel.
curl() {
  local method=GET url="" data="" headers="" with_code=0 code=200 body='{}' route n count
  while (($#)); do
    case "$1" in
      -X) method="$2"; shift ;; -H) headers+="$2 "; shift ;; -d) data="$2"; shift ;; -w) with_code=1; shift ;;
      -o|--connect-timeout|--max-time|--cacert) shift ;; http*) url="$1" ;;
    esac
    shift
  done
  [[ -n "${url}" ]] || return 0
  route="${url#*/api/v1}"
  route="${method} ${route%%\?*}"
  count="${STATE}/n.${route//[^A-Za-z0-9]/_}"
  n=$(($(cat "${count}" 2>/dev/null || echo 0) + 1))
  echo "${n}" >"${count}"
  echo "${route} | ${headers}| ${data}" >>"${REQ_LOG}"
  stub_check_registered "${route}" "${headers}${data}"
  if [[ "${route}@${n}" == "${INJECT_AT}" ]]; then
    code="${INJECT_CODE}" body="{\"error\":\"echo ${data} ${LEAK} ${headers}\"}"
  else
    stub_route "${route}" "${headers}" "${data}" "${n}"
  fi
  printf '%s' "${body}"
  if ((with_code)); then printf '\n%s' "${code}"; fi
}
stub_route() {
  local route="$1" headers="$2" data="$3" n="$4"
  case "${route}" in
    "POST /users") if [[ "${data}" == *viewer* ]]; then body='{"id":"v1"}'; else body='{"id":"u1"}'; fi ;;
    "POST /auth/login")
      if [[ "${data}" == *pg9-viewer-* ]]; then body="{\"token\":\"${S2}\"}"
      elif [[ -e "${STATE}/deleted" ]]; then code=401
      elif [[ -e "${STATE}/login1" ]]; then body="{\"token\":\"${S3}\"}"
      else : >"${STATE}/login1"; body="{\"token\":\"$(json_value "${S1}")\"}"; fi ;;
    "GET /auth/session") if [[ -e "${STATE}/logout" ]]; then code=401; fi ;;
    "POST /auth/logout") code=204; : >"${STATE}/logout" ;;
    "POST /auth/keys")
      if [[ "${data}" == *pg16-* ]]; then body="{\"key\":{\"id\":\"k16\"},\"secret\":\"$(json_value "${K16}")\"}"
      else body="{\"key\":{\"id\":\"k9\"},\"secret\":\"${K9}\"}"; fi ;;
    "GET /status") if [[ -e "${STATE}/revoked" ]]; then code=401; fi ;;
    "DELETE /auth/keys/k9") code=204; : >"${STATE}/revoked" ;;
    "DELETE /users/u1") code=204; : >"${STATE}/deleted" ;;
    "POST /jobs") if [[ "${headers}" == *Bearer* ]]; then code=403; else body="{\"job_id\":\"job-${n}\"}"; fi ;;
    "GET /jobs/job-1") if [[ -e "${STATE}/rejected" ]]; then body='{"state":"DENIED"}'; else body='{"state":"APPROVAL_REQUIRED"}'; fi ;;
    "GET /jobs/"*) body='{"state":"SUCCEEDED"}' ;;
    "POST /approvals/job-1/reject") code=204; : >"${STATE}/rejected" ;;
    "GET /memory") code=404 ;;
    "GET /workers") body='{"items":[{"pool":"demo-mock-bank"}]}' ;;
    "POST /auth/password"|"DELETE "*) code=204 ;;
  esac
}
json_value() { if [[ -n "${MULTILINE}" ]]; then printf '%s\\n%s' "$1" "$1"; else printf '%s' "$1"; fi; }
stub_check_registered() {
  local v
  [[ -n "${MASK_FILE}" ]] || return 0
  for v in ${SECRETS}; do [[ "$2" != *"${v}"* ]] || grep -qxF -- "::add-mask::${v}" "${MASK_FILE}" || echo "$1" >>"${UNREG}"; done
}
date() {
  if [[ "$*" != "+%s" ]]; then command date "$@"; return; fi
  local n=$(($(cat "${STATE}/date" 2>/dev/null || echo 0) + 1))
  echo "${n}" >"${STATE}/date"
  echo "${TAG}d${n}"
}
docker() { :; }; go() { :; }; openssl() { :; }; cordumctl() { :; }; grpcurl() { return 1; }
ensure_mock_bank_pack() { :; }; ensure_mock_bank_worker() { :; }; cleanup() { :; }
export -f curl stub_route json_value stub_check_registered date docker go openssl cordumctl grpcurl \
  ensure_mock_bank_pack ensure_mock_bank_worker cleanup

# --- Exact production_gate.sh functions --------------------------------------
extract_function() {
  awk -v fn="$2" '$0 == fn "() {" {emit=1} emit {print} emit && $0 == "}" {exit}' "$1"
}
HELPER="${SANDBOX}/production_gate_functions.sh"
for fn in now_ms log sanitize_message json_escape api_url api_response api_response_code api_response_body \
  format_api_failure api_code api_body api_call http_code poll_job_terminal run_gate write_results_json \
  is_blocking_gate print_summary gate_9_identity gate_16_degradation pg_init_secret_registration \
  pg_secret_registration_failed pg_require_untraced pg_register_secret pg_assert_secret_registration; do
  extract_function "${PG}" "${fn}"
done >"${HELPER}"
# shellcheck source=/dev/null
source "${HELPER}"
# shellcheck source=tools/scripts/github_actions_mask_env.sh
source "${ROOT}/tools/scripts/github_actions_mask_env.sh"
export SCRIPT_DIR="${ROOT}/tools/scripts" ROOT_DIR="${ROOT}" API_BASE="http://pg.invalid" TENANT_ID=default
API_KEY="synthetic-gate-key" CURL_TIMEOUT_OPTS=() CURL_TLS_OPTS=() JSON_HEADERS=(-H "Content-Type: application/json")
AUTH_HEADERS=(-H "X-API-Key: synthetic-gate-key" -H "X-Tenant-ID: default")
SELECT_GATE="" EXCLUDED_GATES=() SELECTED_GATES=() BLOCKING_GATES=(1 2 4 5 7 9 18) STRICT_MODE=0
declare -A GATE_STATUS GATE_DURATION_MS GATE_MESSAGE GATE_NAME

reset_state() { rm -rf "${STATE}"; mkdir -p "${STATE}"; }
new_case() { : >"${REQ_LOG}"; : >"${UNREG}"; reset_state; }
init_masks() { new_case; MASK_FILE="$1"; pg_init_secret_registration >"$1" || true; }
# run_case GATE OUT: one gate through the real run_gate; OUT.msg/OUT.json get GATE_MESSAGE/results.
run_case() {
  local fn=gate_9_identity
  if [[ "$1" == 16 ]]; then fn=gate_16_degradation; fi
  reset_state
  run_gate "$1" "${fn}" "Gate $1" </dev/null >"$2" 2>&1
  SELECTED_GATES=("$1")
  RESULTS_FILE="$2.json" write_results_json
  printf '%s' "${GATE_MESSAGE[$1]}" >"$2.msg"
}
# full_run NAME [MULTILINE]: production_gate.sh --gate 16 (advisory, non-strict) in Actions.
full_run() {
  mkdir -p "${SANDBOX}/$1.tmp"
  new_case
  env -u STRICT_MODE -u CORDUM_TLS_CA MULTILINE="${2:-}" MASK_FILE="${SANDBOX}/$1.out" TMPDIR="${SANDBOX}/$1.tmp" \
    CORDUM_API_KEY=synthetic-gate-key CORDUM_API_BASE="${API_BASE}" RESULTS_FILE="${SANDBOX}/$1.json" \
    MOCK_BANK_PID_FILE="${SANDBOX}/$1.pid" CORDUM_PRODUCTION_GATE_REUSE_MOCK_BANK_WORKER=1 \
    bash "${PG}" --gate 16 </dev/null >"${SANDBOX}/$1.out" 2>"${SANDBOX}/$1.err"
}
used_after_registration() { local v; for v in "$@"; do grep -qF -- "${v}" "${REQ_LOG}" || return 1; done; [[ ! -s "${UNREG}" ]]; }
failure_sealed() { [[ "${GATE_STATUS[$1]}" == "$2" && ! -s "${UNREG}" ]] && sealed "${SANDBOX}"/b.out*; }
refused_before_use() {
  [[ "${GATE_STATUS[$1]}" == FAIL ]] && ! grep -qF -- "$2" "${REQ_LOG}" &&
    sealed "$3" "$3.msg" "$3.json" && rc_is 1 pg_assert_secret_registration
}
traced_refusal() { grep -qxF -- "$2" "$1" && [[ ! -s "${REQ_LOG}" && ! -e "${STATE}/date" ]] && sealed "$1"; }

# (a) Actions success path: each value is registered exactly once, on the mask FD only.
init_masks "${SANDBOX}/a.mask"
run_case 9 "${SANDBOX}/a9.out"
run_case 16 "${SANDBOX}/a16.out"
expect "(a) gates 9 and 16 pass" [ "${GATE_STATUS[9]}${GATE_STATUS[16]}" = PASSPASS ]
for entry in "user password|${P1}" "user session|${S1}" "API key secret|${K9}" "viewer password|${P2}" \
  "viewer session|${S2}" "re-login session|${S3}" "replacement password|${P3}" "gate 16 key secret|${K16}"; do
  expect "(a) ${entry%%|*} registered exactly once on the mask FD" registered_once "${SANDBOX}/a.mask" "${entry#*|}"
done
expect "(a) the mask FD carries only those 8 registrations" [ "$(wc -l <"${SANDBOX}/a.mask")" -eq 8 ]
expect "(a) tee'd output, GATE_MESSAGE and results JSON hold zero sentinel bytes" sealed "${SANDBOX}"/a9.out* "${SANDBOX}"/a16.out*
# shellcheck disable=SC2086
expect "(a) every value reached the stubbed API only after its registration" used_after_registration ${SECRETS}
expect "(a) the run-wide registration check passes" rc_is 0 pg_assert_secret_registration
expect "(a) full script exits 0 for gate 16" rc_is 0 full_run full-ok
expect "(a) full script registers the key once on the runner stdout" registered_once "${SANDBOX}/full-ok.out" "${K16}"
expect "(a) full script stderr and results JSON hold zero sentinel bytes" sealed "${SANDBOX}"/full-ok.{err,json}
expect "(a) full script uses the key only after registration" used_after_registration "${K16}"
expect "(a) full script removes its registration state on exit" [ -z "$(ls -A "${SANDBOX}/full-ok.tmp")" ]

# (b) Every failure path; injected error bodies echo the request and all sentinels.
B_CASES=(
  "9|POST /users@1|500|FAIL" "9|GET /users@1|500|FAIL" "9|POST /auth/login@1|500|FAIL" "9|GET /auth/session@1|500|FAIL"
  "9|POST /auth/logout@1|500|FAIL" "9|GET /auth/session@2|200|FAIL" "9|POST /auth/keys@1|500|FAIL" "9|GET /auth/keys@1|500|FAIL"
  "9|GET /status@1|500|FAIL" "9|DELETE /auth/keys/k9@1|500|FAIL" "9|GET /status@2|200|FAIL" "9|POST /users@2|500|PASS"
  "9|POST /auth/login@2|500|PASS" "9|POST /jobs@1|200|FAIL" "9|POST /auth/login@3|500|PASS" "9|POST /auth/password@1|500|FAIL"
  "9|DELETE /users/u1@1|500|FAIL" "9|POST /auth/login@4|200|FAIL" "16|POST /auth/keys@1|500|FAIL" "16|POST /auth/keys@1|201|FAIL"
  "16|POST /jobs@1|500|FAIL" "16|DELETE /auth/keys/k16@1|500|PASS" "16|POST /approvals/job-1/reject@1|500|FAIL"
  "16|GET /traces/job-2@1|500|FAIL" "16|GET /memory@1|500|FAIL"
)
init_masks "${SANDBOX}/b.mask"
for entry in "${B_CASES[@]}"; do
  IFS='|' read -r gate INJECT_AT INJECT_CODE want <<<"${entry}"
  new_case
  run_case "${gate}" "${SANDBOX}/b.out"
  expect "(b) gate ${gate}: ${INJECT_AT} -> ${INJECT_CODE} gives ${want}, registered before use, zero sentinel bytes" \
    failure_sealed "${gate}" "${want}"
done
INJECT_AT="" INJECT_CODE=""
expect "(b) ordinary gate failures are not registration failures" rc_is 0 pg_assert_secret_registration

# (c) Registration failures fail the gate before first use and fail the whole run.
init_masks "${SANDBOX}/c1.mask"
# bash reuses freed descriptors >= 10 for its own saved fds, so point at a high closed one.
if [[ -n "${PG_MASK_FD:-}" ]]; then exec {PG_MASK_FD}>&-; PG_MASK_FD=200; fi
run_case 9 "${SANDBOX}/c1.out"
expect "(c) closed mask FD: gate 9 fails before its first password is used" refused_before_use 9 "${TAG}d2" "${SANDBOX}/c1.out"
init_masks "${SANDBOX}/c2.mask"
exec {PG_MASK_FD}<"${SANDBOX}/c2.mask"
run_case 16 "${SANDBOX}/c2.out"
expect "(c) read-only mask FD: gate 16 fails before its key is used" refused_before_use 16 "${K16}" "${SANDBOX}/c2.out"
expect "(c) the unregistered gate 16 key is still revoked" grep -q '^DELETE /auth/keys/k16 ' "${REQ_LOG}"
expect "(c) gate 16 stays advisory in non-strict mode" rc_is 0 print_summary
expect "(c) yet the run-wide registration check fails" rc_is 1 pg_assert_secret_registration
init_masks "${SANDBOX}/c3.mask"
MULTILINE=1 run_case 9 "${SANDBOX}/c3.out"
expect "(c) multiline session token: gate 9 fails before the session is used" refused_before_use 9 "${S1}" "${SANDBOX}/c3.out"
init_masks "${SANDBOX}/c4.mask"
no_helper_register() { unset -f gha_mask_value; pg_register_secret "$1"; }
for entry in "empty|" "multiline|a-${TAG}"$'\n'"b" "carriage-return|a-${TAG}"$'\r'; do
  expect "(c) ${entry%%|*} value refused" rc_is 1 pg_register_secret "${entry#*|}"
done
expect "(c) missing value refused" rc_is 1 pg_register_secret
expect "(c) missing gha_mask_value helper refused" rc_is 1 no_helper_register "h-${TAG}"
expect "(c) refused values never reach the mask FD" [ ! -s "${SANDBOX}/c4.mask" ]
expect "(c) full script: an unregistrable key fails the run though gate 16 is advisory" rc_is 1 full_run full-bad 1
expect "(c) full script: results show only an advisory gate 16 failure" rc_is 0 jq -e \
  '.strict_mode == false and .blocking_passed == true and .gates[0].status == "FAIL" and .gates[0].class == "ADVISORY"' \
  "${SANDBOX}/full-bad.json"
expect "(c) full script: the refused key never reached the API" rc_is 1 grep -qF -- "${K16}" "${REQ_LOG}"
expect "(c) full script: zero sentinel bytes in stdout, stderr and results" sealed "${SANDBOX}"/full-bad.{out,err,json}

# (d) xtrace: both gates refuse before any credential is generated or requested.
init_masks "${SANDBOX}/d.mask"
(set -x; run_gate 9 gate_9_identity "Gate 9"; { set +x; } 2>/dev/null; echo "status=${GATE_STATUS[9]}") \
  </dev/null >"${SANDBOX}/d9.out" 2>&1 || true
expect "(d) set -x: gate 9 refuses before any password, login or key" traced_refusal "${SANDBOX}/d9.out" "status=FAIL"
expect "(d) the xtrace refusal fails the run" rc_is 1 pg_assert_secret_registration
new_case
env SHELLOPTS=xtrace bash -c 'source "$1"; gate_16_degradation; echo "rc=$?"' _ "${HELPER}" \
  </dev/null >"${SANDBOX}/d16.out" 2>&1 || true
expect "(d) SHELLOPTS=xtrace: gate 16 refuses before creating a key" traced_refusal "${SANDBOX}/d16.out" "rc=1"

# (e) Outside Actions: nothing is registered or printed; behaviour is unchanged.
unset GITHUB_ACTIONS
init_masks "${SANDBOX}/e.mask"
MASK_FILE=""
run_case 9 "${SANDBOX}/e9.out"
run_case 16 "${SANDBOX}/e16.out"
export GITHUB_ACTIONS=true
expect "(e) gates 9 and 16 pass" [ "${GATE_STATUS[9]}${GATE_STATUS[16]}" = PASSPASS ]
expect "(e) no ::add-mask:: and zero sentinel bytes anywhere" sealed "${SANDBOX}/e.mask" "${SANDBOX}"/e9.out* "${SANDBOX}"/e16.out*
expect "(e) no registration failure outside Actions" rc_is 0 pg_assert_secret_registration

# (f) e2e_test.sh admin login: registered in Actions, never printed, not even a prefix.
E2E_HELPER="${SANDBOX}/e2e_functions.sh"
{ grep -E '^(green|red|yellow)\(\)' "${E2E}"; extract_function "${E2E}" check; extract_function "${E2E}" e2e_admin_login; } \
  >"${E2E_HELPER}"
e2e_run() {
  reset_state
  (
    # shellcheck source=/dev/null
    source "${E2E_HELPER}"
    http_body() { : >"${STATE}/e2e-login"; printf '{"token":"%s"}' "${E2E_TOKEN}"; }
    http_code() { printf '200'; }
    PASS=0 FAIL=0 ERRORS="" SESSION="" BASE="http://e2e.invalid" ADMIN_USERNAME=admin TENANT_ID=default
    ADMIN_PASSWORD="synthetic-admin-${TAG}" E2E_TOKEN="${E2E_S}"
    case "$1" in local) unset GITHUB_ACTIONS ;; multiline) E2E_TOKEN="${E2E_S}\\n${E2E_S}" ;; xtrace) set -x ;; esac
    e2e_admin_login
    { set +x; } 2>/dev/null
    kept=no
    [[ "${SESSION}" != "${E2E_S}" ]] || kept=yes
    echo "pass=${PASS} fail=${FAIL} kept=${kept}"
  ) </dev/null >"${SANDBOX}/e2e-$1.out" 2>&1 || true
}
# e2e_ok MODE ACCOUNTING MASKS: accounting, mask count, no session prefix off the registration line.
e2e_ok() {
  local out="${SANDBOX}/e2e-$1.out"
  grep -qxF -- "$2" "${out}" && [[ "$(grep -c '::add-mask::' "${out}" || true)" == "$3" ]] &&
    [[ "$(grep -vxF -- "::add-mask::${E2E_S}" "${out}" | grep -cF -- "${E2E_S:0:8}" || true)" == 0 ]]
}
e2e_run actions
expect "(f) Actions: session registered once and kept for later steps" e2e_ok actions "pass=2 fail=0 kept=yes" 1
e2e_run local
expect "(f) outside Actions: no mask command, session kept" e2e_ok local "pass=2 fail=0 kept=yes" 0
e2e_run multiline
expect "(f) multiline session refused, API-key fallback kept" e2e_ok multiline "pass=1 fail=1 kept=no" 0
e2e_run xtrace
expect "(f) xtrace: login refused and recorded as a failure" e2e_ok xtrace "pass=0 fail=1 kept=no" 0
expect "(f) xtrace: no login request was sent" [ ! -e "${STATE}/e2e-login" ]
expect "(f) e2e_test.sh prints no session substring" rc_is 1 grep -qE '\$\{SESSION:[0-9-]' "${E2E}"

echo "SUMMARY: ${PASS} pass, ${FAIL} fail"
[[ "${FAIL}" -eq 0 && "${PASS}" -gt 0 ]] || exit 1

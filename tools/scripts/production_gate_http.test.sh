#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
SCRIPT="${ROOT}/tools/scripts/production_gate.sh"
SANDBOX="$(mktemp -d -t production-gate-http-test.XXXXXX)"
trap 'rm -rf "${SANDBOX}"' EXIT

extract_function() {
  local fn="$1"
  awk -v fn="${fn}" '
    $0 == fn "() {" {emit=1; seen=1}
    emit && seen && $0 != fn "() {" && $0 ~ /^[A-Za-z_][A-Za-z0-9_]*\(\) \{$/ {exit}
    emit {print}
  ' "${SCRIPT}"
}

HELPER="${SANDBOX}/http_functions.sh"
{
  echo 'set -euo pipefail'
  echo 'sanitize_message() { printf "%s" "$1" | tr "\r\n\t" "   "; }'
  extract_function api_url
  extract_function api_response
  extract_function api_response_code
  extract_function api_response_body
  extract_function api_code
  extract_function api_body
  extract_function format_api_failure
} >"${HELPER}"
# shellcheck source=/dev/null
source "${HELPER}"

PASS=0
FAIL=0
FAKE_CURL_LOG="${SANDBOX}/curl.log"
export FAKE_CURL_LOG
API_BASE="https://api.example.test"
CURL_TIMEOUT_OPTS=()
CURL_TLS_OPTS=()
AUTH_HEADERS=()

assert_eq() {
  local name="$1" got="$2" want="$3"
  if [[ "${got}" == "${want}" ]]; then
    echo "PASS: ${name}"
    PASS=$((PASS + 1))
  else
    echo "FAIL: ${name}: got '${got}', want '${want}'" >&2
    FAIL=$((FAIL + 1))
  fi
}

call_count() {
  wc -l <"${FAKE_CURL_LOG}" | tr -d '[:space:]'
}

assert_post_receive_failure_is_not_retried() {
  local helper="$1" rc=0
  : >"${FAKE_CURL_LOG}"
  curl() { echo request >>"${FAKE_CURL_LOG}"; return 56; }
  "${helper}" POST /agents >/dev/null || rc=$?
  unset -f curl
  assert_eq "${helper} preserves ambiguous POST receive failure" "${rc}" "56"
  assert_eq "${helper} does not retry ambiguous POST" "$(call_count)" "1"
}

assert_get_receive_failure_is_retried() {
  local helper="$1" rc=0
  : >"${FAKE_CURL_LOG}"
  curl() {
    echo request >>"${FAKE_CURL_LOG}"
    if [[ "$(call_count)" == "1" ]]; then
      return 56
    fi
    printf '%s\n%s' '{"ok":true}' '200'
  }
  "${helper}" GET /status >/dev/null || rc=$?
  unset -f curl
  assert_eq "${helper} retries safe GET receive failure" "${rc}" "0"
  assert_eq "${helper} bounds safe GET receive retry" "$(call_count)" "2"
}

formatter_fn="$(extract_function format_api_failure)"
if [[ "${formatter_fn}" == *API_DIAGNOSTIC_MAX_CHARS* ]]; then
  PASS=$((PASS + 1))
  echo "PASS: API failure formatter has a configurable response bound"
else
  FAIL=$((FAIL + 1))
  echo "FAIL: API failure formatter lacks a configurable response bound" >&2
fi
formatted="$(API_DIAGNOSTIC_MAX_CHARS=32 format_api_failure 403 \
  '{"error":"denied","detail":"0123456789abcdefghijklmnopqrstuvwxyz"}')"
assert_eq "API failure formatter truncates body" "${formatted}" \
  'status=403 body={"error":"denied","detail":"0123'
formatted="$(format_api_failure 400 $'line one\nline two')"
assert_eq "API failure formatter sanitizes newlines" "${formatted}" \
  'status=400 body=line one line two'

curl() {
  echo request >>"${FAKE_CURL_LOG}"
  printf '%s\n%s' '{"error":"denied"}' '403'
}
: >"${FAKE_CURL_LOG}"
raw_response="$(api_response POST /agents)"
unset -f curl
assert_eq "api_response preserves HTTP status" \
  "$(api_response_code "${raw_response}")" "403"
assert_eq "api_response preserves response body" \
  "$(api_response_body "${raw_response}")" '{"error":"denied"}'
assert_eq "api_response does not retry HTTP errors" "$(call_count)" "1"

for helper in api_response api_code api_body; do
  assert_post_receive_failure_is_not_retried "${helper}"
  assert_get_receive_failure_is_retried "${helper}"
done

echo "SUMMARY: ${PASS} pass, ${FAIL} fail"
if [[ "${FAIL}" -gt 0 ]]; then
  exit 1
fi

#!/usr/bin/env bash
# Contract tests for tools/scripts/fetch_stargazers.sh.
#
# The fetcher must page through GitHub's stargazer API, retry transient API
# failures without accepting error JSON, validate every response, and replace
# its output atomically only after the complete snapshot is valid.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
FETCHER="${ROOT}/tools/scripts/fetch_stargazers.sh"
SANDBOX="$(mktemp -d -t fetch-stargazers-test.XXXXXX)"
trap 'rm -rf "${SANDBOX}"' EXIT

FAKE_BIN="${SANDBOX}/bin"
FAKE_GH_LOG="${SANDBOX}/gh.log"
FAKE_GH_STATE="${SANDBOX}/gh-state"
mkdir -p "${FAKE_BIN}" "${FAKE_GH_STATE}"

cat >"${FAKE_BIN}/gh" <<'FAKEGH'
#!/usr/bin/env bash
set -euo pipefail

printf '%s\n' "$*" >>"${FAKE_GH_LOG}"
endpoint=""
for arg in "$@"; do
  if [[ "${arg}" == repos/*/stargazers\?* ]]; then
    endpoint="${arg}"
  fi
done
page="$(printf '%s' "${endpoint}" | sed -n 's/.*[?&]page=\([0-9][0-9]*\).*/\1/p')"
if [[ -z "${page}" ]]; then
  echo "fake gh: missing page query" >&2
  exit 90
fi

attempt_file="${FAKE_GH_STATE}/page-${page}"
attempt=0
if [[ -f "${attempt_file}" ]]; then
  attempt="$(cat "${attempt_file}")"
fi
attempt=$((attempt + 1))
printf '%s' "${attempt}" >"${attempt_file}"

case "${FAKE_GH_SCENARIO}" in
  valid)
    if [[ "${page}" -eq 1 ]]; then
      printf '%s\n' '[{"user":{"login":"bob"}},{"user":{"login":"alice"}}]'
    else
      printf '%s\n' '[{"user":{"login":"carol"}}]'
    fi
    ;;
  transient)
    if [[ "${attempt}" -eq 1 ]]; then
      printf '%s\n' '{"message":"temporary upstream failure"}'
      echo "gh: HTTP 502" >&2
      exit 1
    fi
    printf '%s\n' '[{"user":{"login":"alice"}}]'
    ;;
  exhaust)
    printf '%s\n' '{"message":"API rate limit exceeded"}'
    echo "gh: HTTP 403" >&2
    exit 1
    ;;
  invalid-json)
    printf '%s\n' '{not-json'
    ;;
  non-array)
    printf '%s\n' '{"message":"API rate limit exceeded"}'
    ;;
  partial)
    if [[ "${page}" -eq 1 ]]; then
      printf '%s\n' '[{"user":{"login":"alice"}},{"user":{"login":"bob"}}]'
    else
      printf '%s\n' '{"message":"temporary upstream failure"}'
      echo "gh: HTTP 502" >&2
      exit 1
    fi
    ;;
  *)
    echo "fake gh: unknown scenario ${FAKE_GH_SCENARIO}" >&2
    exit 91
    ;;
esac
FAKEGH
chmod +x "${FAKE_BIN}/gh"

PASS=0
FAIL=0

record_pass() {
  echo "  PASS: $1"
  PASS=$((PASS + 1))
}

record_fail() {
  echo "  FAIL: $1" >&2
  FAIL=$((FAIL + 1))
}

assert_eq() {
  local name="$1"
  local got="$2"
  local want="$3"
  if [[ "${got}" == "${want}" ]]; then
    record_pass "${name}"
  else
    record_fail "${name}: got '${got}', want '${want}'"
  fi
}

assert_contains() {
  local name="$1"
  local file="$2"
  local expected="$3"
  if grep -qF "${expected}" "${file}"; then
    record_pass "${name}"
  else
    record_fail "${name}: missing '${expected}'"
  fi
}

assert_not_contains() {
  local name="$1"
  local file="$2"
  local unexpected="$3"
  if grep -qF "${unexpected}" "${file}"; then
    record_fail "${name}: unexpectedly found '${unexpected}'"
  else
    record_pass "${name}"
  fi
}

reset_fake() {
  rm -f "${FAKE_GH_LOG}"
  rm -rf "${FAKE_GH_STATE}"
  mkdir -p "${FAKE_GH_STATE}"
}

line_count() {
  local file="$1"
  if [[ ! -f "${file}" ]]; then
    printf '0'
    return
  fi
  wc -l <"${file}" | tr -d ' '
}

invoke_fetcher() {
  local scenario="$1"
  local output="$2"
  local log="$3"
  local actual_exit=0

  PATH="${FAKE_BIN}:${PATH}" \
    FAKE_GH_LOG="${FAKE_GH_LOG}" \
    FAKE_GH_STATE="${FAKE_GH_STATE}" \
    FAKE_GH_SCENARIO="${scenario}" \
    STARGAZER_PAGE_SIZE=2 \
    STARGAZER_MAX_PAGES=10 \
    STARGAZER_MAX_ATTEMPTS=3 \
    STARGAZER_RETRY_SECONDS=0 \
    bash "${FETCHER}" --repo cordum-io/cordum --output "${output}" \
      >"${log}" 2>&1 || actual_exit=$?
  printf '%s' "${actual_exit}"
}

echo "--- T1 valid paginated arrays produce a sorted snapshot ---"
reset_fake
output="${SANDBOX}/valid.txt"
log="${SANDBOX}/valid.log"
status="$(invoke_fetcher valid "${output}" "${log}")"
assert_eq "valid fetch exits zero" "${status}" "0"
actual="$(cat "${output}" 2>/dev/null || true)"
assert_eq "valid fetch writes all unique usernames" "${actual}" $'alice\nbob\ncarol'
calls="$(line_count "${FAKE_GH_LOG}")"
assert_eq "valid fetch requests two pages" "${calls}" "2"

echo "--- T2 transient gh error JSON is discarded before retry ---"
reset_fake
output="${SANDBOX}/transient.txt"
log="${SANDBOX}/transient.log"
status="$(invoke_fetcher transient "${output}" "${log}")"
assert_eq "transient fetch exits zero after retry" "${status}" "0"
assert_eq "transient error object is not accepted" "$(cat "${output}" 2>/dev/null || true)" "alice"
calls="$(line_count "${FAKE_GH_LOG}")"
assert_eq "transient fetch retries exactly once" "${calls}" "2"

echo "--- T3 retry exhaustion is bounded and nonzero ---"
reset_fake
output="${SANDBOX}/exhaust.txt"
log="${SANDBOX}/exhaust.log"
printf '%s\n' "keep-me" >"${output}"
status="$(invoke_fetcher exhaust "${output}" "${log}")"
if [[ "${status}" -ne 0 ]]; then
  record_pass "exhausted fetch exits nonzero"
else
  record_fail "exhausted fetch unexpectedly exits zero"
fi
calls="$(line_count "${FAKE_GH_LOG}")"
assert_eq "exhausted fetch stops at max attempts" "${calls}" "3"
assert_eq "exhausted fetch preserves prior snapshot" "$(cat "${output}")" "keep-me"

for scenario in invalid-json non-array; do
  echo "--- T4 ${scenario} response is rejected ---"
  reset_fake
  output="${SANDBOX}/${scenario}.txt"
  log="${SANDBOX}/${scenario}.log"
  printf '%s\n' "keep-me" >"${output}"
  status="$(invoke_fetcher "${scenario}" "${output}" "${log}")"
  if [[ "${status}" -ne 0 ]]; then
    record_pass "${scenario} exits nonzero"
  else
    record_fail "${scenario} unexpectedly exits zero"
  fi
  assert_contains "${scenario} reports invalid response" "${log}" "invalid stargazer response"
  assert_eq "${scenario} preserves prior snapshot" "$(cat "${output}")" "keep-me"
done

echo "--- T5 partial pagination failure cannot overwrite a snapshot ---"
reset_fake
output="${SANDBOX}/partial.txt"
log="${SANDBOX}/partial.log"
printf '%s\n' "keep-me" >"${output}"
status="$(invoke_fetcher partial "${output}" "${log}")"
if [[ "${status}" -ne 0 ]]; then
  record_pass "partial fetch exits nonzero"
else
  record_fail "partial fetch unexpectedly exits zero"
fi
assert_eq "partial fetch preserves prior snapshot" "$(cat "${output}")" "keep-me"
calls="$(line_count "${FAKE_GH_LOG}")"
assert_eq "partial fetch bounds second-page retries" "${calls}" "4"

echo "--- T6 workflow serializes and fails closed on snapshot read errors ---"
workflow="${ROOT}/.github/workflows/star-tracker.yml"
assert_contains "unstars job has a dedicated concurrency group" "${workflow}" \
  "group: star-tracker-detect-unstars"
assert_contains "overlapping unstar runs wait instead of cancelling" "${workflow}" \
  "cancel-in-progress: false"
assert_not_contains "snapshot download errors are not ignored" "${workflow}" \
  "continue-on-error: true"
assert_not_contains "artifact metadata failures are not treated as first run" "${workflow}" \
  "2>/dev/null || true)"
assert_not_contains "artifact download failures are not ignored" "${workflow}" \
  "> /tmp/previous/snapshot.zip 2>/dev/null || true"

echo ""
echo "SUMMARY: ${PASS} pass, ${FAIL} fail"
if [[ "${FAIL}" -gt 0 ]]; then
  exit 1
fi

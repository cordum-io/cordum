#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LIB="${SCRIPT_DIR}/soak_test_lib.sh"
SOAK="${SCRIPT_DIR}/soak_test.sh"

if [[ ! -f "${LIB}" ]]; then
  echo "FAIL: soak analysis helper library is missing: ${LIB}" >&2
  exit 1
fi

# shellcheck source=tools/scripts/soak_test_lib.sh
source "${LIB}"

PASS=0
FAIL=0

assert_eq() {
  local name="$1" want="$2" got="$3"
  if [[ "${got}" == "${want}" ]]; then
    echo "PASS: ${name}"
    PASS=$((PASS + 1))
  else
    echo "FAIL: ${name}: got '${got}', want '${want}'" >&2
    FAIL=$((FAIL + 1))
  fi
}

tmpdir="$(mktemp -d -t soak-test-lib.XXXXXX)"
trap 'rm -rf "${tmpdir}"' EXIT

cat >"${tmpdir}/http.log" <<'EOF'
1 POST:/jobs 200
2 POST:/jobs:invalid-empty-prompt 400 expected=400
3 GET:/jobs 503
4 POST:/jobs:invalid-empty-prompt 401 expected=400
5 GET:/health 000000
EOF

assert_eq "expected client error is excluded from error rate" "3" \
  "$(count_unexpected_http_responses "${tmpdir}/http.log")"
assert_eq "only unexpected 4xx responses feed storm detection" \
  "POST:/jobs:invalid-empty-prompt" \
  "$(unexpected_client_error_endpoints "${tmpdir}/http.log")"

{
  for i in $(seq 1 7); do
    for _ in $(seq 1 "$((8 - i))"); do
      printf 'service | repeated-%02d\n' "${i}"
    done
  done
} >"${tmpdir}/docker.log"
top_lines="$(top_repeated_log_lines <"${tmpdir}/docker.log")"
assert_eq "log storm analysis returns at most five lines" "5" \
  "$(printf '%s\n' "${top_lines}" | wc -l | tr -d ' ')"
assert_eq "log storm analysis ranks the most repeated line first" "repeated-01" \
  "$(printf '%s\n' "${top_lines}" | sed -n '1p' | awk '{$1=""; sub(/^ /, ""); print}')"

assert_eq "docker sizes convert to MiB" "18.55 1536.00 0.49" \
  "$(docker_size_to_mib '18.55MiB') $(docker_size_to_mib '1.5GiB') $(docker_size_to_mib '512kB')"
assert_eq "unparseable docker sizes print nothing" "" "$(docker_size_to_mib 'n/a')"
assert_eq "memory growth keeps decimals (1.95% -> 2.07% is 6.2%, not 100%)" "6.2" \
  "$(memory_growth_pct 1.95 2.07)"

load_memory_baseline '# drift-baseline
{"container":"warmup","mem_usage":"15MiB / 7.6GiB","mem_pct":"0.20%"}
{"container":"leak","mem_usage":"100MiB / 7.6GiB","mem_pct":"1.30%"}
{"container":"steady","mem_usage":"50MiB / 7.6GiB","mem_pct":"0.65%"}
{"container":"opaque","mem_usage":"-- / --","mem_pct":"1.00%"}'
findings="$(memory_drift_findings '{"container":"warmup","mem_usage":"30MiB / 7.6GiB","mem_pct":"0.40%"}
{"container":"leak","mem_usage":"200MiB / 7.6GiB","mem_pct":"2.60%"}
{"container":"steady","mem_usage":"52MiB / 7.6GiB","mem_pct":"0.68%"}
{"container":"opaque","mem_usage":"-- / --","mem_pct":"2.00%"}' 50 32 post-warm-up)"
assert_eq "drift past both limits fails, under the MiB floor only warns" "FAIL:leak FAIL:opaque WARN:warmup" \
  "$(printf '%s\n' "${findings}" |
    awk -F'\t' '{ n = $2; sub(/^Memory (drift|growth) on /, "", n); sub(/[: ].*$/, "", n); print $1 ":" n }' |
    sort | tr '\n' ' ' | sed 's/ $//')"
assert_eq "an unmeasurable size fails closed as a measurement problem, not as drift" "1" \
  "$(printf '%s\n' "${findings}" | grep -c '^FAIL.*opaque could not be measured in MiB' || true)"

assert_eq "soak script loads analysis helpers" "1" \
  "$(grep -cF 'source "${SCRIPT_DIR}/soak_test_lib.sh"' "${SOAK}" || true)"
assert_eq "soak script records the intentional 400 expectation" "1" \
  "$(grep -cF 'record_status "POST:/jobs:invalid-empty-prompt" "${status}" 400' "${SOAK}" || true)"
assert_eq "soak error rate uses semantic response classification" "1" \
  "$(grep -cF 'count_unexpected_http_responses "${HTTP_LOG}"' "${SOAK}" || true)"
assert_eq "soak log ranking avoids an early-closing head pipeline" "1" \
  "$(grep -cF 'top_repeated_log_lines' "${SOAK}" || true)"
assert_eq "soak drift check uses the decimal-safe helper" "1" \
  "$(grep -cF 'memory_drift_findings "${final_metrics}"' "${SOAK}" || true)"
assert_eq "soak warm-up is normalised to base 10 (0300 is not octal 192)" "1" \
  "$(grep -cF 'WARMUP_SECONDS=$((10#${WARMUP_SECONDS}))' "${SOAK}" || true)"

echo "SUMMARY: ${PASS} pass, ${FAIL} fail"
[[ "${FAIL}" -eq 0 ]]

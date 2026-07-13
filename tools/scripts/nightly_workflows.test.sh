#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
INTEGRATION="${ROOT}/.github/workflows/integration-nightly.yml"
NIGHTLY="${ROOT}/.github/workflows/nightly.yml"
DEMO_E2E="${ROOT}/.github/workflows/demo-mock-bank-e2e.yml"
SOAK="${ROOT}/tools/scripts/soak_test.sh"
CI="${ROOT}/.github/workflows/ci.yml"
PASS=0
FAIL=0

assert_count() {
  local name="$1" file="$2" pattern="$3" want="$4" got
  got="$(grep -cF -- "${pattern}" "${file}" || true)"
  if [[ "${got}" == "${want}" ]]; then
    echo "PASS: ${name}"
    PASS=$((PASS + 1))
  else
    echo "FAIL: ${name}: got ${got}, want ${want}" >&2
    FAIL=$((FAIL + 1))
  fi
}

assert_count "integration provisions an ephemeral CI license" "${INTEGRATION}" \
  'go run ./tools/cilicense >> "$GITHUB_ENV"' 1
assert_count "integration enables managed-key storage" "${INTEGRATION}" \
  'CORDUM_USER_AUTH_ENABLED=true' 1
assert_count "integration provisions an admin password" "${INTEGRATION}" \
  'CORDUM_ADMIN_PASSWORD=' 1
assert_count "all nightly service jobs provision CI licenses" "${NIGHTLY}" \
  'go run ./tools/cilicense >> "$GITHUB_ENV"' 3
assert_count "all nightly service jobs enable managed-key storage" "${NIGHTLY}" \
  'CORDUM_USER_AUTH_ENABLED=true' 3
assert_count "all nightly service jobs provision admin passwords" "${NIGHTLY}" \
  'CORDUM_ADMIN_PASSWORD=' 3
assert_count "nightly labels the complete 21-gate suite accurately" "${NIGHTLY}" \
  'Full Production Gate (21 gates)' 1
assert_count "nightly labels the complete gate step accurately" "${NIGHTLY}" \
  'Run all production gates (1-21)' 1
assert_count "nightly avoids duplicate setup-go and actions/cache restores" "${NIGHTLY}" \
  'cache: false' 3
assert_count "integration tests isolate fixture license environment" "${INTEGRATION}" \
  'env -u CORDUM_LICENSE_TOKEN -u CORDUM_LICENSE_PUBLIC_KEY go test -v -tags=integration -timeout 10m ./...' 1
assert_count "mock-bank e2e provisions an ephemeral CI license" "${DEMO_E2E}" \
  'go run ./tools/cilicense >> "$GITHUB_ENV"' 1
assert_count "nightly soak uses the TLS gateway URL" "${NIGHTLY}" \
  'CORDUM_API_BASE: https://localhost:8081/api/v1' 1
assert_count "nightly soak trusts the generated CA" "${NIGHTLY}" \
  'CORDUM_TLS_CA: certs/ca/ca.crt' 1
assert_count "soak requests apply resolved TLS options" "${SOAK}" \
  '"${CURL_TLS_OPTS[@]}"' 2
assert_count "soak supports an explicit TLS CA" "${SOAK}" \
  '--cacert "${TLS_CA}"' 1
assert_count "CI runs soak analysis regression tests" "${CI}" \
  'bash tools/scripts/soak_test_lib.test.sh' 1
assert_count "strict shared-runner gate excludes hardware-dependent gate 6" "${NIGHTLY}" \
  '--skip-rebuild --strict --exclude-gate 6' 1
assert_count "nightly keeps gate 6 as a visible advisory probe" "${NIGHTLY}" \
  'RESULTS_FILE=performance_gate_results.json bash tools/scripts/production_gate.sh --gate 6 --skip-rebuild --strict' 1
assert_count "shared-runner performance probe is explicitly nonblocking" "${NIGHTLY}" \
  'continue-on-error: true' 1
assert_count "nightly does not launder the production p95 threshold" "${NIGHTLY}" \
  'PERF_P95_MS:' 0
assert_count "release artifacts retain the advisory performance result" "${NIGHTLY}" \
  'performance_gate_results.json' 2

echo "SUMMARY: ${PASS} pass, ${FAIL} fail"
[[ "${FAIL}" -eq 0 ]]

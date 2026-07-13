#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
INTEGRATION="${ROOT}/.github/workflows/integration-nightly.yml"
NIGHTLY="${ROOT}/.github/workflows/nightly.yml"
PASS=0
FAIL=0

assert_count() {
  local name="$1" file="$2" pattern="$3" want="$4" got
  got="$(grep -cF "${pattern}" "${file}" || true)"
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
assert_count "integration tests isolate fixture license environment" "${INTEGRATION}" \
  'env -u CORDUM_LICENSE_TOKEN -u CORDUM_LICENSE_PUBLIC_KEY go test -v -tags=integration -timeout 10m ./...' 1

echo "SUMMARY: ${PASS} pass, ${FAIL} fail"
[[ "${FAIL}" -eq 0 ]]

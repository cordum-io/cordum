#!/usr/bin/env bash
# EDGE-074 — synthetic test for tools/scripts/check_dashboard_deps.sh.
#
# Asserts the gate's main modes:
#   T1 — clean tree: gate exits 0 with "OK:" line.
#   T2 — package.json edited while pnpm-lock.yaml is stale: gate exits 3.
#   T3 — malformed package.json: gate exits 2 with validation failure output.
#
# Each test runs in an isolated /tmp copy of dashboard/ so the working tree
# is never mutated. Restoration of the original tree is unconditional via
# bash trap.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
GATE="${REPO_ROOT}/tools/scripts/check_dashboard_deps.sh"

if [[ ! -x "${GATE}" ]] && [[ ! -f "${GATE}" ]]; then
  echo "FAIL: gate script not found at ${GATE}" >&2
  exit 1
fi

# Use a temporary REPO_ROOT mirror so the gate's `cd dashboard/` walks the
# expected layout. Only the dashboard/ subdirectory is mirrored.
SANDBOX="$(mktemp -d -t edge074-test.XXXXXX)"
trap 'rm -rf "${SANDBOX}"' EXIT

mkdir -p "${SANDBOX}/dashboard"
cp "${REPO_ROOT}/dashboard/package.json"      "${SANDBOX}/dashboard/"
cp "${REPO_ROOT}/dashboard/pnpm-lock.yaml"    "${SANDBOX}/dashboard/"
# The gate locates the repo via $(cd "$(dirname "$0")/../.." && pwd), so
# stage a copy of the script under the sandbox to make REPO_ROOT resolve to
# ${SANDBOX} instead of the real repo.
mkdir -p "${SANDBOX}/tools/scripts"
cp "${GATE}" "${SANDBOX}/tools/scripts/check_dashboard_deps.sh"
chmod +x "${SANDBOX}/tools/scripts/check_dashboard_deps.sh"

PASS=0
FAIL=0

run_case() {
  local name="$1"
  local expected_exit="$2"
  local expected_grep="$3"

  echo "--- ${name} ---"
  local out_file
  out_file="$(mktemp -t edge074-test-out.XXXXXX)"
  local actual_exit=0
  bash "${SANDBOX}/tools/scripts/check_dashboard_deps.sh" >"${out_file}" 2>&1 || actual_exit=$?

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
  rm -f "${out_file}"

  if [[ "${case_pass}" -eq 1 ]]; then
    echo "  PASS"
    PASS=$((PASS + 1))
  else
    FAIL=$((FAIL + 1))
  fi
}

restore_clean_tree() {
  cp "${REPO_ROOT}/dashboard/package.json"      "${SANDBOX}/dashboard/"
  cp "${REPO_ROOT}/dashboard/pnpm-lock.yaml"    "${SANDBOX}/dashboard/"
}

# T1 — clean tree
restore_clean_tree
run_case "T1 clean tree" 0 "OK: dashboard dependencies clean"

# T2 — inject lockfile drift: bump every lodash range in package.json while
# leaving pnpm-lock.yaml unchanged. `pnpm install --frozen-lockfile` should
# fail with ERR_PNPM_OUTDATED_LOCKFILE, and the gate maps that to exit 3.
restore_clean_tree
sed_tmp="$(mktemp -t edge074-edit.XXXXXX)"
sed 's|"lodash": "\^4\.18\.0"|"lodash": "^4.18.1"|g' "${SANDBOX}/dashboard/package.json" > "${sed_tmp}"
mv "${sed_tmp}" "${SANDBOX}/dashboard/package.json"
run_case "T2 pnpm lockfile drift detected" 3 "out of sync|ERR_PNPM_(OUTDATED_LOCKFILE|LOCKFILE_CONFIG_MISMATCH)"

# T3 — inject a malformed package.json. This is not lockfile drift; the gate
# should return the dependency/configuration failure class (exit 2).
restore_clean_tree
sed_tmp="$(mktemp -t edge074-edit.XXXXXX)"
sed 's|"name": "cordum-dashboard-v2"|"name": |' "${SANDBOX}/dashboard/package.json" > "${sed_tmp}"
mv "${sed_tmp}" "${SANDBOX}/dashboard/package.json"
run_case "T3 package config error detected" 2 "dependency validation failed|Unexpected token"

echo ""
echo "==== SUMMARY: ${PASS} pass, ${FAIL} fail ===="
if [[ "${FAIL}" -gt 0 ]]; then
  exit 1
fi
exit 0

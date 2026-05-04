#!/usr/bin/env bash
# EDGE-074 — synthetic test for tools/scripts/check_dashboard_deps.sh.
#
# Asserts the gate's three failure modes:
#   T1 — clean tree: gate exits 0 with "OK:" line.
#   T2 — EOVERRIDE injected (lodash dep reverted to ^4.17.21 while overrides
#        keeps ^4.18.0): gate exits 2 with EOVERRIDE in output.
#   T3 — lockfile drift injected (package.json edited but lockfile not
#        regenerated): gate exits 3 with "out of sync" in output.
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
cp "${REPO_ROOT}/dashboard/package-lock.json" "${SANDBOX}/dashboard/"
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
  cp "${REPO_ROOT}/dashboard/package-lock.json" "${SANDBOX}/dashboard/"
}

# T1 — clean tree
restore_clean_tree
run_case "T1 clean tree" 0 "OK: dashboard dependencies clean"

# Surgical sed-range edit: only modify entries WITHIN the dependencies block,
# leaving overrides + pnpm.overrides untouched. The range delimiters are
# context lines that are unique to the dependencies block in the current
# dashboard/package.json shape (jspdf...lucide-react bracket the lodash
# entry; any future structural change requires updating these markers).
# Portable across BSD sed (macOS) and GNU sed (Linux/MSYS).
edit_dependencies_block() {
  local key="$1"
  local from="$2"
  local to="$3"
  local pkg="${SANDBOX}/dashboard/package.json"
  local tmp
  tmp="$(mktemp -t edge074-edit.XXXXXX)"
  sed "/\"jspdf\"/,/\"lucide-react\"/ s|\"${key}\": \"${from}\"|\"${key}\": \"${to}\"|" "${pkg}" > "${tmp}"
  mv "${tmp}" "${pkg}"
}

# T2 — inject EOVERRIDE: revert dependencies.lodash to ^4.17.21 while
# overrides.lodash and pnpm.overrides.lodash stay at ^4.18.0. Reproduces
# the architect's 2026-05-04 discovery: ^4.17.21 (>=4.17.21 <4.18.0) does
# not intersect ^4.18.0 (>=4.18.0 <4.19.0); npm flags EOVERRIDE.
restore_clean_tree
edit_dependencies_block "lodash" "\^4\.18\.0" "^4.17.21"
run_case "T2 EOVERRIDE detected" 2 "EOVERRIDE|dependency resolution error"

# T3 — inject lockfile drift: lodash already has 4.18.1 published. Bump
# BOTH dependencies.lodash and overrides.lodash + pnpm.overrides.lodash to
# ^4.18.1 (so no EOVERRIDE), then assert the gate's Phase B detects that
# the existing lockfile (resolved against ^4.18.0 -> 4.18.0) drifts from
# the package.json's new ^4.18.1 range.
restore_clean_tree
sed_tmp="$(mktemp -t edge074-edit.XXXXXX)"
sed 's|"lodash": "\^4\.18\.0"|"lodash": "^4.18.1"|g' "${SANDBOX}/dashboard/package.json" > "${sed_tmp}"
mv "${sed_tmp}" "${SANDBOX}/dashboard/package.json"
run_case "T3 lockfile drift detected" 3 "out of sync|drift detected"

echo ""
echo "==== SUMMARY: ${PASS} pass, ${FAIL} fail ===="
if [[ "${FAIL}" -gt 0 ]]; then
  exit 1
fi
exit 0

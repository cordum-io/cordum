#!/usr/bin/env bash
# EDGE-068b — synthetic test for tools/scripts/lint_no_secret_log.sh Phase 4
# (argv-only exec guard).
#
# The harness exercises the guard against three fixture corpora under
# tools/scripts/testdata/lint_no_secret_log/:
#
#   phase4_pass/      — clean Go files the guard MUST allow (exit 0).
#   phase4_fail/      — Go files the guard MUST flag (exit 1) and the
#                       output MUST mention the fixture filename.
#   phase4_exception/ — Go files that look like shell exec patterns but
#                       carry the `// no-shell-exec-lint` marker. MUST
#                       pass (exit 0).
#
# Each case runs the lint with LINT_SCAN_ROOTS_OVERRIDE pointing at a single
# fixture directory so the FAIL signal localises to that fixture.
#
# Final invariant: with the env override unset, a full lint run against the
# real cordum/ tree (cmd/+core/) MUST still exit 0. This protects against
# accidental testdata bleed into the production scan.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
LINT="${REPO_ROOT}/tools/scripts/lint_no_secret_log.sh"
FIXTURE_ROOT="${REPO_ROOT}/tools/scripts/testdata/lint_no_secret_log"

if [[ ! -f "${LINT}" ]]; then
  echo "FAIL: lint script not found at ${LINT}" >&2
  exit 1
fi
if [[ ! -d "${FIXTURE_ROOT}" ]]; then
  echo "FAIL: fixture root not found at ${FIXTURE_ROOT}" >&2
  exit 1
fi

PASS=0
FAIL=0

# run_case <name> <fixture-subdir> <expected-exit> <expected-grep-regex>
#
# Sets LINT_SCAN_ROOTS_OVERRIDE to the supplied fixture subdir, runs the
# lint, and asserts both the exit code and a stdout pattern. expected-grep
# is matched with `grep -E`; pass an empty string to skip the pattern check.
run_case() {
  local name="$1"
  local fixture_subdir="$2"
  local expected_exit="$3"
  local expected_grep="$4"

  echo "--- ${name} ---"
  local out_file
  out_file="$(mktemp -t edge068b-test-out.XXXXXX)"
  local actual_exit=0
  LINT_SCAN_ROOTS_OVERRIDE="${FIXTURE_ROOT}/${fixture_subdir}" \
    bash "${LINT}" >"${out_file}" 2>&1 || actual_exit=$?

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

# T1 — pass corpus: pure-argv + go-test-compile -c flag must NOT trip the guard.
run_case "T1 phase4_pass — argv-only + non-shell -c flag" \
  "phase4_pass" 0 "OK: No raw secret logging"

# T2..T9 — fail corpus. Per-fixture isolated cases: each sub-case stages a
# single .go file in a tmpdir so the FAIL line names the specific fixture and
# a regression in one pattern does not mask another.
isolate_one() {
  local src="$1"
  local stage
  stage="$(mktemp -d -t edge068b-stage.XXXXXX)"
  cp "${src}" "${stage}/"
  echo "${stage}"
}

run_isolated_fail() {
  local name="$1"
  local fixture_file="$2"
  local expected_filename
  expected_filename="$(basename "${fixture_file}")"

  echo "--- ${name} ---"
  local stage
  stage="$(isolate_one "${FIXTURE_ROOT}/phase4_fail/${fixture_file}")"
  local out_file
  out_file="$(mktemp -t edge068b-test-out.XXXXXX)"
  local actual_exit=0
  LINT_SCAN_ROOTS_OVERRIDE="${stage}" \
    bash "${LINT}" >"${out_file}" 2>&1 || actual_exit=$?

  local case_pass=1
  if [[ "${actual_exit}" -ne 1 ]]; then
    echo "  FAIL: exit ${actual_exit} != expected 1"
    cat "${out_file}"
    case_pass=0
  fi
  if ! grep -qE "FAIL: .*${expected_filename}.*may spawn a shell interpreter" "${out_file}"; then
    echo "  FAIL: stdout did not flag ${expected_filename}"
    cat "${out_file}"
    case_pass=0
  fi
  rm -f "${out_file}"
  rm -rf "${stage}"

  if [[ "${case_pass}" -eq 1 ]]; then
    echo "  PASS"
    PASS=$((PASS + 1))
  else
    FAIL=$((FAIL + 1))
  fi
}

run_isolated_fail "T2 sh -c"                       "sh_dash_c.go"
run_isolated_fail "T3 /bin/sh -c (absolute path)"  "bin_sh_dash_c.go"
run_isolated_fail "T4 bash -c"                     "bash_dash_c.go"
run_isolated_fail "T5 cmd /C (Windows)"            "cmd_slash_c.go"
run_isolated_fail "T6 cmd.exe /c (lowercase)"       "cmd_exe_slash_c.go"
run_isolated_fail "T7 powershell -Command"          "powershell_dash_command.go"
run_isolated_fail "T8 multi-line exec.CommandContext" "multiline.go"
run_isolated_fail "T9 /usr/bin/sh -c (absolute)"   "absolute_path_sh.go"

# T10 — exception corpus: doctor.go-style runtime.GOOS branch with marker
# annotations + a minimum-shape inline marker. MUST pass.
run_case "T10 phase4_exception — // no-shell-exec-lint marker bypass" \
  "phase4_exception" 0 "OK: No raw secret logging"

# T11 — default-tree invariant: with LINT_SCAN_ROOTS_OVERRIDE unset, a full
# scan of the real cmd/+core/ tree MUST still exit 0. This guards against
# the testdata accidentally bleeding into the production scan path.
echo "--- T11 default-tree invariant (no override) ---"
default_out="$(mktemp -t edge068b-test-out.XXXXXX)"
default_exit=0
bash "${LINT}" >"${default_out}" 2>&1 || default_exit=$?
default_case_pass=1
if [[ "${default_exit}" -ne 0 ]]; then
  echo "  FAIL: default-tree exit ${default_exit} != expected 0"
  cat "${default_out}"
  default_case_pass=0
fi
if ! grep -qE "OK: No raw secret logging" "${default_out}"; then
  echo "  FAIL: default-tree output missing OK line"
  cat "${default_out}"
  default_case_pass=0
fi
rm -f "${default_out}"
if [[ "${default_case_pass}" -eq 1 ]]; then
  echo "  PASS"
  PASS=$((PASS + 1))
else
  FAIL=$((FAIL + 1))
fi

echo ""
echo "==== SUMMARY: ${PASS} pass, ${FAIL} fail ===="
if [[ "${FAIL}" -gt 0 ]]; then
  exit 1
fi
exit 0

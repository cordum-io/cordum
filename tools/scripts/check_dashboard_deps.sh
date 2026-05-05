#!/usr/bin/env bash
# EDGE-074 — dashboard dependency hygiene gate.
#
# Catches pnpm dependency drift at PR time, BEFORE `pnpm install`:
#   1. package.json / pnpm-lock.yaml drift (pnpm exits with
#      ERR_PNPM_OUTDATED_LOCKFILE under --frozen-lockfile).
#   2. Dependency configuration or resolver errors that make pnpm unable to
#      validate the lockfile.
#
# Strategy:
#   Run `pnpm install --frozen-lockfile --lockfile-only --ignore-scripts`.
#   This validates package.json against pnpm-lock.yaml without writing
#   node_modules or regenerating the lockfile. Exit 3 on stale/mismatched
#   lockfile, 2 on other pnpm dependency/config failures.
#
# Exit codes:
#   0 = clean
#   2 = pnpm dependency/configuration error
#   3 = pnpm-lock.yaml drift
#   1 = unexpected internal error (missing pnpm, missing dashboard/, etc.)
#
# To suppress in extraordinary cases: comment out the gate's CI step (visible
# in PR diff for review) — this script intentionally has no skip flag.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
DASHBOARD_DIR="${REPO_ROOT}/dashboard"

if [[ ! -d "${DASHBOARD_DIR}" ]]; then
  echo "FAIL: dashboard/ not found at ${DASHBOARD_DIR}" >&2
  exit 1
fi
if [[ ! -f "${DASHBOARD_DIR}/package.json" ]]; then
  echo "FAIL: ${DASHBOARD_DIR}/package.json missing" >&2
  exit 1
fi
if [[ ! -f "${DASHBOARD_DIR}/pnpm-lock.yaml" ]]; then
  echo "FAIL: ${DASHBOARD_DIR}/pnpm-lock.yaml missing — run 'pnpm install --lockfile-only' in dashboard/ and commit the result" >&2
  exit 1
fi
if ! command -v pnpm >/dev/null 2>&1; then
  echo "FAIL: pnpm not found on PATH" >&2
  exit 1
fi

cd "${DASHBOARD_DIR}"

PNPM_OUTPUT_FILE="$(mktemp -t edge074-pnpm.XXXXXX)"
trap 'rm -f "${PNPM_OUTPUT_FILE}"' EXIT

pnpm_exit=0
pnpm install --frozen-lockfile --lockfile-only --ignore-scripts \
  >"${PNPM_OUTPUT_FILE}" 2>&1 || pnpm_exit=$?

if [[ "${pnpm_exit}" -ne 0 ]]; then
  if grep -qE 'ERR_PNPM_OUTDATED_LOCKFILE|ERR_PNPM_LOCKFILE_CONFIG_MISMATCH|lockfile is not up to date|specifiers in the lockfile' "${PNPM_OUTPUT_FILE}"; then
    echo "FAIL: dashboard/pnpm-lock.yaml is out of sync with dashboard/package.json (EDGE-074)" >&2
    echo "" >&2
    cat "${PNPM_OUTPUT_FILE}" >&2
    echo "" >&2
    echo "A previous edit to package.json did not update the pnpm lockfile." >&2
    echo "Locally, run:" >&2
    echo "  cd dashboard && pnpm install --lockfile-only" >&2
    echo "and commit pnpm-lock.yaml alongside package.json." >&2
    exit 3
  fi
  echo "FAIL: pnpm dependency validation failed with exit ${pnpm_exit} (EDGE-074):" >&2
  echo "" >&2
  cat "${PNPM_OUTPUT_FILE}" >&2
  exit 2
fi

echo "OK: dashboard dependencies clean (pnpm lockfile matches package.json)"
exit 0

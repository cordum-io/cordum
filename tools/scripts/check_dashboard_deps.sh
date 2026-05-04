#!/usr/bin/env bash
# EDGE-074 — dashboard dependency hygiene gate.
#
# Catches two classes of npm config drift at PR time, BEFORE `npm ci`:
#   1. Direct dep / overrides semver mismatch (npm exits with EOVERRIDE).
#   2. package-lock.json out of sync with package.json (silent on `npm ci`
#      from a cached docker layer; explodes on the next dep bump).
#
# Strategy:
#   Phase A — `npm install --package-lock-only --legacy-peer-deps --dry-run`
#             surfaces EOVERRIDE / ERESOLVE / EUSAGE errors without touching
#             the working tree. Exit 2 on resolution failure.
#   Phase B — backup the existing lockfile, regenerate it, diff vs backup.
#             Drift means a previous PR edited package.json without
#             re-running npm install --package-lock-only. Exit 3 on drift.
#             Original lockfile is restored before exit so subsequent CI
#             steps see a clean tree.
#
# Exit codes:
#   0 = clean
#   2 = npm resolution error (EOVERRIDE / ERESOLVE / EUSAGE)
#   3 = lockfile drift
#   1 = unexpected internal error (missing npm, missing dashboard/, etc.)
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
if [[ ! -f "${DASHBOARD_DIR}/package-lock.json" ]]; then
  echo "FAIL: ${DASHBOARD_DIR}/package-lock.json missing — run 'npm install --package-lock-only --legacy-peer-deps' in dashboard/ and commit the result" >&2
  exit 1
fi
if ! command -v npm >/dev/null 2>&1; then
  echo "FAIL: npm not found on PATH" >&2
  exit 1
fi

cd "${DASHBOARD_DIR}"

# Phase A — dry-run resolves overrides + deps without writing.
DRY_RUN_STDERR_FILE="$(mktemp -t edge074-dry-run.XXXXXX)"
trap 'rm -f "${DRY_RUN_STDERR_FILE}"' EXIT

dry_run_exit=0
npm install --package-lock-only --legacy-peer-deps --dry-run \
  >/dev/null 2>"${DRY_RUN_STDERR_FILE}" || dry_run_exit=$?

if [[ "${dry_run_exit}" -ne 0 ]]; then
  if grep -qE 'EOVERRIDE|ERESOLVE|EUSAGE' "${DRY_RUN_STDERR_FILE}"; then
    echo "FAIL: dashboard dependency resolution error (EDGE-074):" >&2
    grep -E 'EOVERRIDE|ERESOLVE|EUSAGE|npm error' "${DRY_RUN_STDERR_FILE}" >&2 || true
    echo "" >&2
    echo "Likely cause: a direct dep version range does not intersect its overrides entry," >&2
    echo "or peer-dep resolution failed. When bumping a dep that has an 'overrides' entry," >&2
    echo "bump BOTH the direct dep AND the override to the same range." >&2
    echo "After editing package.json, run:" >&2
    echo "  cd dashboard && npm install --package-lock-only --legacy-peer-deps" >&2
    echo "and commit the resulting package-lock.json." >&2
    exit 2
  fi
  echo "FAIL: npm install --dry-run exited with ${dry_run_exit} (no recognized error code in stderr):" >&2
  cat "${DRY_RUN_STDERR_FILE}" >&2
  exit 2
fi

# Phase B — regen lockfile and diff vs current. Drift means a previous PR
# edited package.json without re-running npm install --package-lock-only.
LOCKFILE_BACKUP="$(mktemp -t edge074-lock.XXXXXX)"
cp package-lock.json "${LOCKFILE_BACKUP}"

# Augment EXIT trap: also restore the lockfile no matter how we leave.
trap 'cp "${LOCKFILE_BACKUP}" package-lock.json 2>/dev/null || true; rm -f "${DRY_RUN_STDERR_FILE}" "${LOCKFILE_BACKUP}"' EXIT

regen_exit=0
npm install --package-lock-only --legacy-peer-deps >/dev/null 2>&1 || regen_exit=$?
if [[ "${regen_exit}" -ne 0 ]]; then
  # If dry-run passed but regen failed, that's a Phase-B bug (likely network
  # or peer-dep cache mismatch). Surface it but don't conflate with drift.
  echo "FAIL: lockfile regen exited with ${regen_exit} (dry-run had passed)" >&2
  exit 1
fi

if ! diff -q "${LOCKFILE_BACKUP}" package-lock.json >/dev/null 2>&1; then
  echo "FAIL: dashboard/package-lock.json is out of sync with dashboard/package.json (EDGE-074)" >&2
  echo "" >&2
  echo "A previous edit to package.json bumped a dep without regenerating the lockfile." >&2
  echo "Locally, run:" >&2
  echo "  cd dashboard && npm install --package-lock-only --legacy-peer-deps" >&2
  echo "and commit the resulting package-lock.json alongside the package.json change." >&2
  exit 3
fi

echo "OK: dashboard dependencies clean (no EOVERRIDE / ERESOLVE / EUSAGE / lockfile drift)"
exit 0

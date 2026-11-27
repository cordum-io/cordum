#!/usr/bin/env bash
# Negative test for check-generated.sh. In a scratch git repo holding HEAD's .gitignore,
# docs/api/openapi and sdk/python, the check must pass on the committed tree, fail when a
# schema-only spec change (a new response code, no new operationId) is committed without
# regenerating the client, pass once the regenerated client is committed, and fail when a generated
# module is missing from the commit. Needs git and the generator extra in sdk/python/.venv
# (sdk/python/README.md, "Regenerating from the spec").
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
SDK_DIR="$(cd -- "${SCRIPT_DIR}/.." && pwd)"
REPO_DIR="$(git -C "${SDK_DIR}" rev-parse --show-toplevel)"
# The scratch copy has no .venv of its own, so generate.sh falls back to the generator on PATH.
export PATH="${SDK_DIR}/.venv/bin:${SDK_DIR}/.venv/Scripts:${PATH}"
if ! command -v openapi-python-client >/dev/null 2>&1; then
  echo "FAIL: openapi-python-client not found; install the generator extra into sdk/python/.venv" >&2
  exit 1
fi

SCRATCH="$(mktemp -d)"
trap 'rm -rf "${SCRATCH}"' EXIT
git -C "${REPO_DIR}" -c core.autocrlf=false archive --format=tar HEAD -- .gitignore docs/api/openapi sdk/python |
  tar -x -C "${SCRATCH}"
G=(git -C "${SCRATCH}" -c user.name=check-generated-test -c user.email=check-generated-test@invalid
  -c commit.gpgsign=false -c core.autocrlf=false)
"${G[@]}" init -q
"${G[@]}" add -A
"${G[@]}" commit -q -m "HEAD copy"

GEN=sdk/python/src/cordum_sdk/_generated
MODULE="${GEN}/api/health/health_check.py"
SPEC="${SCRATCH}/docs/api/openapi/cordum-api.yaml"
OK_LINE="Python SDK generated client is current"
ERR_LINE="error: sdk/python/src/cordum_sdk/_generated is out of date"
FAILURES=0
OUT=""
RC=0

run_check() {
  if OUT="$(bash "${SCRATCH}/sdk/python/scripts/check-generated.sh" 2>&1)"; then RC=0; else RC=$?; fi
}
report() {
  if [[ "$1" == ok ]]; then
    echo "PASS: $2"
  else
    echo "FAIL: $2 (exit ${RC})" >&2
    printf '%s\n' "${OUT}" | tail -n 10 >&2
    FAILURES=$((FAILURES + 1))
  fi
}
expect_current() {
  run_check
  if [[ ${RC} -eq 0 && "${OUT}" == *"${OK_LINE}"* ]]; then report ok "$1"; else report bad "$1"; fi
}
expect_stale() {
  run_check
  if [[ ${RC} -eq 1 && "${OUT}" == *"${ERR_LINE}"* && "${OUT}" == *"${MODULE}"* ]]; then
    report ok "$1"
  else
    report bad "$1"
  fi
}
count_operation_ids() { grep -c -E '^[[:space:]]*operationId:[[:space:]]*[^[:space:]]' "${SPEC}"; }

expect_current "the committed tree is current"

ops_before="$(count_operation_ids)"
if ! awk -v code="        '418':" -v desc="          description: Synthetic response added by check-generated.test.sh" '
  { print }
  $0 == "      operationId: healthCheck" { found = 1; next }
  found && $0 == "      responses:" { print code; print desc; found = 0; done = 1 }
  END { exit !done }' "${SPEC}" >"${SCRATCH}/spec.new"; then
  echo "FAIL: could not add a 418 response to healthCheck in the spec" >&2
  exit 1
fi
mv "${SCRATCH}/spec.new" "${SPEC}"
if [[ "$(count_operation_ids)" != "${ops_before}" ]]; then
  echo "FAIL: the spec edit changed the operationId count" >&2
  exit 1
fi
"${G[@]}" commit -q -am "healthCheck gains a 418 response; the client is not regenerated"
expect_stale "a schema-only spec change committed without regeneration fails"

"${G[@]}" add -A -- "${GEN}"
# --allow-empty: a check that did not regenerate leaves nothing to commit, and the cases go on.
"${G[@]}" commit -q --allow-empty -m "regenerate the client"
expect_current "the committed regenerated client passes"

"${G[@]}" rm -q -- "${MODULE}"
"${G[@]}" commit -q -m "drop a generated module"
expect_stale "a generated module missing from the commit fails"

if [[ ${FAILURES} -ne 0 ]]; then
  echo "check-generated.test.sh: ${FAILURES} of 4 cases failed" >&2
  exit 1
fi
echo "check-generated.test.sh: 4 cases passed"

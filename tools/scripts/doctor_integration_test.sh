#!/usr/bin/env bash
# cordumctl doctor — integration test
#
# Drives the real compose stack end-to-end:
#   1. boot the stack via ./tools/scripts/quickstart.sh (or assume running)
#   2. run `cordumctl doctor --json` — assert exitCode 0 and no fail states
#   3. `docker compose stop nats` — re-run doctor, assert exit 1 + nats fail
#   4. restart nats, verify recovery (exit 0 again within retry window)
#   5. (opt-in) test --fix against the pack-missing path
#
# CORDUM_INTEGRATION=1 gate so `make test` doesn't run it; the Makefile's
# `doctor-test` target sets this automatically.
#
# Requires: bash 4+, docker compose, jq, a built cordumctl on PATH (or
# set CORDUMCTL to an absolute path).
set -euo pipefail

if [[ "${CORDUM_INTEGRATION:-}" != "1" ]]; then
  echo "skip: set CORDUM_INTEGRATION=1 to run doctor integration tests" >&2
  exit 0
fi

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${REPO_ROOT}"

: "${CORDUMCTL:=cordumctl}"
: "${CORDUM_GATEWAY:=https://127.0.0.1:8081}"
: "${CORDUM_API_KEY:?export CORDUM_API_KEY before running}"
: "${CORDUM_TENANT_ID:=default}"
: "${CORDUM_DOCTOR_TIMEOUT:=30}"

fail() { echo "DOCTOR INTEGRATION FAIL: $*" >&2; exit 1; }
ok()   { echo "  OK  $*"; }
note() { echo "  -- $*"; }

need() { command -v "$1" >/dev/null 2>&1 || fail "required tool not found: $1"; }
need jq
need docker
need "${CORDUMCTL}"

run_doctor_json() {
  # --cacert + --insecure reuse whatever env is set. Caller wraps in `|| true`
  # when a non-zero exit is expected.
  local args=(doctor --json --timeout "${CORDUM_DOCTOR_TIMEOUT}")
  # Auto-pass the locally-generated CA when the target is HTTPS and the caller
  # hasn't pre-seeded CORDUM_TLS_CA. Without this, the default compose stack's
  # self-signed certs fail TLS verification on the baseline phase.
  if [[ -z "${CORDUM_TLS_CA:-}" && "${CORDUM_GATEWAY}" == https://* && -f "${REPO_ROOT}/certs/ca/ca.crt" ]]; then
    args+=(--cacert "${REPO_ROOT}/certs/ca/ca.crt")
  fi
  "${CORDUMCTL}" "${args[@]}"
}

# Assert exitCode, stored in the JSON envelope, matches the expected value.
expect_exit_code() {
  local payload="$1" expected="$2" label="$3"
  local got
  got="$(echo "${payload}" | jq -r '.exitCode // empty')"
  if [[ "${got}" != "${expected}" ]]; then
    echo "${payload}" | jq '.' >&2 || echo "${payload}" >&2
    fail "${label}: exitCode=${got}, expected ${expected}"
  fi
  ok "${label}: exitCode=${got}"
}

# Assert a specific check's state in the JSON envelope.
expect_state() {
  local payload="$1" id="$2" expected="$3" label="$4"
  local got
  got="$(echo "${payload}" | jq -r --arg id "${id}" '.checks[] | select(.id == $id) | .state // empty')"
  if [[ "${got}" != "${expected}" ]]; then
    echo "${payload}" | jq --arg id "${id}" '.checks[] | select(.id == $id)' >&2 || true
    fail "${label}: check ${id}.state=${got}, expected ${expected}"
  fi
  ok "${label}: ${id}.state=${got}"
}

echo "== Phase 1: baseline (stack up) =="
payload="$(run_doctor_json)"
expect_exit_code "${payload}" 0 "baseline all-green"
expect_state "${payload}" "gateway_reachable" "ok" "baseline"
expect_state "${payload}" "nats_connected" "ok" "baseline"
expect_state "${payload}" "redis_ok" "ok" "baseline"

echo
echo "== Phase 2: simulate NATS outage =="
MSYS_NO_PATHCONV=1 docker compose stop nats >/dev/null
trap 'MSYS_NO_PATHCONV=1 docker compose start nats >/dev/null 2>&1 || true' EXIT

# Give the gateway's status probe one refresh cycle to notice.
sleep 3

set +e
payload="$(run_doctor_json)"
rc=$?
set -e
if [[ "${rc}" -eq 0 ]]; then
  fail "doctor returned 0 while NATS was stopped"
fi
expect_exit_code "${payload}" 1 "nats-down exit non-zero"
expect_state "${payload}" "nats_connected" "fail" "nats-down"

fix_hint="$(echo "${payload}" | jq -r '.checks[] | select(.id == "nats_connected") | .fix')"
if [[ -z "${fix_hint}" ]]; then
  fail "nats_connected fail has empty fix hint"
fi
note "fix hint surfaced: ${fix_hint}"

echo
echo "== Phase 3: restart NATS, verify recovery =="
MSYS_NO_PATHCONV=1 docker compose start nats >/dev/null
trap - EXIT

# NATS reconnect is eventual — give the gateway up to 20s to recover.
recovered=false
for i in 1 2 3 4 5 6 7 8 9 10; do
  sleep 2
  payload="$(run_doctor_json || true)"
  state="$(echo "${payload}" | jq -r '.checks[] | select(.id == "nats_connected") | .state // empty')"
  if [[ "${state}" == "ok" ]]; then
    recovered=true
    break
  fi
done
if ! ${recovered}; then
  fail "nats_connected did not recover to ok within 20s"
fi
ok "nats recovered after restart"
expect_exit_code "${payload}" 0 "post-recovery"

echo
echo "All doctor integration assertions passed."

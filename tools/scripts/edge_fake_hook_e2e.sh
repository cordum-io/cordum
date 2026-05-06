#!/usr/bin/env bash
# edge_fake_hook_e2e.sh — CI-safe Edge P0 end-to-end exerciser.
#
# Drives the full Edge P0 backend path with synthetic Claude hook payloads:
#   cordum-hook (stdin JSON) -> cordum-agentd (HTTP loopback)
#     -> Gateway /api/v1/edge/* -> Safety Kernel evaluate -> approvals
#     -> session events -> evidence export bundle.
#
# Default (hook) mode: pipes synthetic Claude hook JSON through
# ./bin/cordum-hook against a locally-spawned ./bin/cordum-agentd that the
# script launches with a process-local CORDUM_AGENTD_NONCE. Exercises the
# full EDGE-027 acceptance path that QA validates.
#
# Bypass mode (CORDUM_EDGE_E2E_BYPASS_HOOK=1): drives each gate via
# Gateway-direct /api/v1/edge/* requests, bypassing cordum-hook + agentd.
# Useful in CI hosts without a Go toolchain or hook/agentd binaries.
#
# Requires no real Claude Code binary. Uses synthetic file-path strings only;
# never reads or stats a real .env. All payload bodies are fake markers.
#
# Reachability mode: SKIP cleanly when API_BASE is unreachable or docker is
# missing in default-mode runs. Strict mode (CORDUM_INTEGRATION=1) treats
# every missing prerequisite as a FAIL.
#
# Contract owners: Cordum Edge (epic-545b186e), task EDGE-027.
# Style template: tools/scripts/platform_smoke.sh.
# EDGE-017.4.1: hook-mode authentication is header-only. The script keeps
# CORDUM_AGENTD_URL bare and passes the runtime nonce to cordum-hook through
# CORDUM_AGENTD_HOOK_NONCE so cordum-hook sends X-Cordum-Agentd-Nonce.

set -euo pipefail

# ---------------------------------------------------------------------------
# Exit codes
# ---------------------------------------------------------------------------
# 0   — all gates PASS, or SKIP path taken in non-integration mode
# 1   — gate assertion FAIL (script reached a gate and the assertion failed)
# 2   — usage error or missing prerequisite in CORDUM_INTEGRATION=1 mode
# 124 — bounded wait timed out (matches Unix `timeout(1)` convention)

readonly EXIT_OK=0
readonly EXIT_FAIL=1
readonly EXIT_PREREQ=2
readonly EXIT_TIMEOUT=124

# ---------------------------------------------------------------------------
# PASS/FAIL/SKIP line shapes (one line per gate, single-line stable)
# ---------------------------------------------------------------------------
# Gate name                  Emitted by
# -----------------------    ------------------------------------------------
# edge_session_setup         step 7  (agentd ready + session/execution created)
# edge_pretooluse_deny       step 8  (PreToolUse Read .env -> deny + DENY event)
# edge_approval_flow         step 9  (Edit -> approval -> retry -> consume)
# edge_posttooluse_artifact  step 10 (PostToolUse + artifact pointer recorded)
# edge_evidence_export       step 11 (export bundle has expected entries)
#
# PASS line shape: `PASS <gate>`
# FAIL line shape: `FAIL <gate>: <reason>`
# SKIP line shape: `SKIP edge_fake_hook_e2e: <reason>` (whole-script skip)

readonly SCRIPT_NAME="edge_fake_hook_e2e"

# ---------------------------------------------------------------------------
# Defaults (env-overridable)
# ---------------------------------------------------------------------------
: "${CORDUM_API_KEY:=}"
: "${CORDUM_TENANT_ID:=default}"
: "${CORDUM_GATEWAY:=}"
: "${CORDUM_INTEGRATION:=}"
: "${CORDUM_EDGE_E2E_START_STACK:=}"
: "${CORDUM_EDGE_E2E_TIMEOUT:=10}"
: "${CORDUM_EDGE_E2E_KEEP_TMP:=}"
: "${CORDUM_TLS_CA:=}"
# Bypass mode: drive each gate via Gateway-direct /api/v1/edge/* requests
# instead of piping synthetic Claude hook JSON through cordum-hook ->
# cordum-agentd. Useful in CI environments without a Go toolchain or
# cordum-hook/cordum-agentd binaries. Default mode (unset) exercises the
# real EDGE-027 acceptance path. See docs/LOCAL_E2E.md.
: "${CORDUM_EDGE_E2E_BYPASS_HOOK:=}"
# Pin the cordum-agentd loopback port. Defaults to 0 = pick a free port.
# Override only when a specific port is required (e.g. firewalled CI host).
: "${CORDUM_EDGE_E2E_AGENTD_PORT:=0}"

# ---------------------------------------------------------------------------
# Output helpers
# ---------------------------------------------------------------------------
log() {
  printf '[%s] %s\n' "${SCRIPT_NAME}" "$*" >&2
}

pass() {
  printf 'PASS %s\n' "$1"
}

fail() {
  local gate=$1
  shift
  printf 'FAIL %s: %s\n' "${gate}" "$*" >&2
  exit "${EXIT_FAIL}"
}

skip() {
  printf 'SKIP %s: %s\n' "${SCRIPT_NAME}" "$*"
  exit "${EXIT_OK}"
}

# ---------------------------------------------------------------------------
# Help
# ---------------------------------------------------------------------------
print_help() {
  cat <<'HELP'
edge_fake_hook_e2e.sh — CI-safe Edge P0 end-to-end exerciser

USAGE
  bash tools/scripts/edge_fake_hook_e2e.sh [--help]

MODES
  Default (CORDUM_INTEGRATION unset):
    Probes API_BASE. If unreachable or docker is missing, prints
    `SKIP edge_fake_hook_e2e: <reason>` and exits 0. Useful for
    non-Edge developers and CI runs that haven't started the stack.

  Strict (CORDUM_INTEGRATION=1):
    Treats every missing prerequisite as a FAIL (non-zero exit).
    Required when the gate must run.

  Stack-bring-up (CORDUM_EDGE_E2E_START_STACK=1):
    Runs `make dev-up` before probing. Does not download anything
    beyond what `make dev-up` already pulls. Combine with
    CORDUM_INTEGRATION=1 to enforce a green run.

  Hook (default; CORDUM_EDGE_E2E_BYPASS_HOOK unset):
    Spawns a process-local cordum-agentd and pipes synthetic Claude
    hook JSON through cordum-hook. Exercises the full
    cordum-hook -> cordum-agentd -> Gateway path. Requires
    ./bin/cordum-hook + ./bin/cordum-agentd (built from cmd/ if a Go
    toolchain is available) and `openssl` on PATH for nonce generation.

  Bypass (CORDUM_EDGE_E2E_BYPASS_HOOK=1):
    Drives each gate via direct /api/v1/edge/evaluate +
    /api/v1/edge/events requests. Skips the agentd subprocess and the
    cordum-hook binary requirement. Use only on CI hosts where the Go
    toolchain or hook/agentd binaries are unavailable.

ENVIRONMENT
  CORDUM_API_KEY                 Required in strict mode. API key for the
                                 Gateway and for cordum-agentd.
  CORDUM_TENANT_ID               Tenant for /api/v1/edge/* requests
                                 (default `default`).
  CORDUM_GATEWAY                 Gateway base URL. If unset, derived from
                                 ./certs/ca/ca.crt presence: present ->
                                 https://localhost:8081, otherwise
                                 http://localhost:8081.
  CORDUM_TLS_CA                  PEM CA cert for Gateway TLS. Auto-detected
                                 from ./certs/ca/ca.crt when present.
  CORDUM_INTEGRATION             Set to 1 to make missing prerequisites
                                 a FAIL instead of a SKIP.
  CORDUM_EDGE_E2E_START_STACK    Set to 1 to run `make dev-up` first.
  CORDUM_EDGE_E2E_TIMEOUT        Bounded wait seconds for HTTP requests
                                 and approval transitions (default 10).
  CORDUM_EDGE_E2E_KEEP_TMP       Set to 1 to skip temp-dir cleanup
                                 (debug only).
  CORDUM_EDGE_E2E_BYPASS_HOOK    Set to 1 to skip cordum-hook + agentd and
                                 drive gates via Gateway-direct requests.
                                 Default (unset) runs the full hook path.
  CORDUM_EDGE_E2E_AGENTD_PORT    Pin agentd loopback port. Default 0 picks
                                 a free port; override only when the host
                                 forbids ephemeral binds.

PASS LINE SHAPES (one per gate)
  PASS edge_session_setup
  PASS edge_pretooluse_deny
  PASS edge_approval_flow
  PASS edge_posttooluse_artifact
  PASS edge_evidence_export

FAIL / SKIP LINE SHAPES
  FAIL <gate>: <reason>
  SKIP edge_fake_hook_e2e: <reason>

EXIT CODES
  0    all gates PASS, or SKIP taken in non-integration mode
  1    gate assertion FAIL
  2    usage error or missing prerequisite in CORDUM_INTEGRATION=1 mode
  124  bounded wait timed out

SECURITY
  Uses synthetic file-path strings only (e.g. `<tmpdir>/fixture/.env`)
  and never reads or stats a real .env. No secret values in payloads,
  logs, or evidence assertions.

REAL CLAUDE
  This script is the CI-safe variant. The manual real-Claude demo
  flow lives separately in docs/edge/ and is not exercised here.
HELP
}

# ---------------------------------------------------------------------------
# Argument parsing
# ---------------------------------------------------------------------------
parse_args() {
  while [[ $# -gt 0 ]]; do
    case "$1" in
      -h|--help)
        print_help
        exit "${EXIT_OK}"
        ;;
      *)
        printf 'unknown argument: %s\n\n' "$1" >&2
        print_help >&2
        exit "${EXIT_PREREQ}"
        ;;
    esac
  done
}

# ---------------------------------------------------------------------------
# Mode predicates
# ---------------------------------------------------------------------------
is_integration_mode() {
  case "${CORDUM_INTEGRATION,,}" in
    1|true|yes|on) return 0 ;;
    *)             return 1 ;;
  esac
}

want_start_stack() {
  case "${CORDUM_EDGE_E2E_START_STACK,,}" in
    1|true|yes|on) return 0 ;;
    *)             return 1 ;;
  esac
}

want_keep_tmp() {
  case "${CORDUM_EDGE_E2E_KEEP_TMP,,}" in
    1|true|yes|on) return 0 ;;
    *)             return 1 ;;
  esac
}

want_bypass_hook() {
  case "${CORDUM_EDGE_E2E_BYPASS_HOOK,,}" in
    1|true|yes|on) return 0 ;;
    *)             return 1 ;;
  esac
}

# ---------------------------------------------------------------------------
# Tool detection (jq, curl, optional Windows fallbacks)
# ---------------------------------------------------------------------------
have_cmd() {
  command -v "$1" >/dev/null 2>&1
}

to_curl_path() {
  local path=${1:-}
  if [[ -n "${path}" && "${path}" == /* ]] && have_cmd cygpath && curl --version 2>/dev/null | grep -qi schannel; then
    cygpath -w "${path}"
    return 0
  fi
  printf '%s' "${path}"
}

JQ_BIN=""
JQ_NEEDS_NATIVE_PATH=0

mark_jq_path_mode() {
  JQ_NEEDS_NATIVE_PATH=0
  local resolved="${JQ_BIN}"
  if have_cmd "${JQ_BIN}"; then
    resolved=$(command -v "${JQ_BIN}" 2>/dev/null || printf '%s' "${JQ_BIN}")
  fi
  case "${resolved}" in
    *.exe|/[A-Za-z]/*|/c/*|/d/*)
      JQ_NEEDS_NATIVE_PATH=1
      ;;
  esac
}

to_jq_path() {
  local path=${1:-}
  if [[ "${JQ_NEEDS_NATIVE_PATH}" == "1" && -n "${path}" && "${path}" == /* ]] && have_cmd cygpath; then
    cygpath -w "${path}"
    return 0
  fi
  printf '%s' "${path}"
}

detect_jq() {
  if [[ -n "${CORDUM_JQ:-}" && -x "${CORDUM_JQ}" ]]; then
    JQ_BIN="${CORDUM_JQ}"
    mark_jq_path_mode
    return 0
  fi
  if have_cmd jq; then
    JQ_BIN="jq"
    mark_jq_path_mode
    return 0
  fi
  # Windows/MSYS fallback: tools/scripts/jq.exe is a documented hook in the
  # plan but is not committed to the repo. If a developer drops one in,
  # we'll find it; otherwise rely on system jq.
  if [[ -x "tools/scripts/jq.exe" ]]; then
    JQ_BIN="tools/scripts/jq.exe"
    mark_jq_path_mode
    return 0
  fi
  return 1
}

require_tools() {
  for cmd in curl; do
    if ! have_cmd "${cmd}"; then
      if is_integration_mode; then
        printf 'FAIL %s: required command not found: %s\n' "${SCRIPT_NAME}" "${cmd}" >&2
        exit "${EXIT_PREREQ}"
      fi
      skip "required command not found: ${cmd}"
    fi
  done
  if ! detect_jq; then
    if is_integration_mode; then
      printf 'FAIL %s: jq not found (set CORDUM_JQ or install jq)\n' "${SCRIPT_NAME}" >&2
      exit "${EXIT_PREREQ}"
    fi
    skip 'jq not found (set CORDUM_JQ or install jq)'
  fi
}

# ---------------------------------------------------------------------------
# Curl wrapper — auth + TLS + bounded timeout, body to file, code on stdout.
# CURL_OPTS / AUTH_HEADERS are populated by `init_curl_opts` once API_BASE
# and CORDUM_API_KEY are known.
# ---------------------------------------------------------------------------
CURL_OPTS=()
AUTH_HEADERS=()
init_curl_opts() {
  CURL_OPTS=(--silent --show-error --max-time "${CORDUM_EDGE_E2E_TIMEOUT}")
  local ca=""
  if [[ -n "${CORDUM_TLS_CA}" ]]; then
    ca="${CORDUM_TLS_CA}"
  elif [[ -f "./certs/ca/ca.crt" ]]; then
    ca="./certs/ca/ca.crt"
  fi
  if [[ -n "${ca}" ]]; then
    CURL_OPTS+=(--cacert "$(to_curl_path "${ca}")")
  fi
  if curl --version 2>/dev/null | grep -qi schannel; then
    CURL_OPTS+=(--ssl-no-revoke)
  fi
  # X-Principal-Id is the dev-mode principal binding that
  # core/controlplane/gateway/auth/basic.go reads when API-key auth alone
  # would leave the principal blank. EDGE-008.7 hardening on
  # resolveEdgeAuthPrincipal refuses to read principal_id from JSON body,
  # so this header is required for every Edge call (sessions, executions,
  # events, evaluate, approvals).
  AUTH_HEADERS=(
    -H "X-API-Key: ${CORDUM_API_KEY}"
    -H "X-Tenant-ID: ${CORDUM_TENANT_ID}"
    -H "X-Principal-Id: ${EDGE_PRINCIPAL_ID:-${CORDUM_EDGE_PRINCIPAL_ID:-demo-principal}}"
  )
}

# Issue an HTTP request and capture body + http_code.
#
# Usage: curl_request <method> <url> [<json_body>]
# - On return: HTTP status code is printed on stdout (or "000" on connect
#   failure), and CURL_LAST_BODY_FILE is set to the path of the response
#   body file. Caller must read/clean it; cleanup is also caught by trap.
CURL_LAST_BODY_FILE=""
# Sentinel file for cross-subshell body-file path passing.
# `code=$(curl_request ...)` runs the function in a subshell, so any
# CURL_LAST_BODY_FILE assignment inside is lost when the subshell exits.
# We persist the path to disk so extract_json/assert_json in the parent
# shell can recover it.
CURL_LAST_BODY_SENTINEL="${CURL_LAST_BODY_SENTINEL:-${TMP_ROOT:-/tmp}/.edge_e2e_last_body}"
curl_request() {
  local method=$1 url=$2 body=${3:-}
  local body_file
  body_file=$(mktemp -p "${TMP_ROOT:-/tmp}" curl_body.XXXXXX)
  CURL_LAST_BODY_FILE="${body_file}"
  printf '%s' "${body_file}" > "${CURL_LAST_BODY_SENTINEL}"
  local curl_body_file
  curl_body_file="$(to_curl_path "${body_file}")"
  local code
  # EDGE-051: Windows-schannel curl 8.16 exits non-zero on a non-fatal cert
  # diagnostic even when --write-out wrote a valid HTTP code to stdout. The
  # `|| true` swallows the exit; the regex check below validates that
  # stdout actually carries a 3-digit HTTP status before we trust it. On
  # Linux where curl exits 0 on success, behavior is unchanged. On real
  # network failure (curl writes nothing to stdout), regex fails and code
  # falls back to "000" — same fail-mode as the previous `|| code="000"`.
  if [[ -n "${body}" ]]; then
    code=$(curl "${CURL_OPTS[@]}" "${AUTH_HEADERS[@]}" \
      -H 'Content-Type: application/json' \
      -X "${method}" -d "${body}" \
      -o "${curl_body_file}" -w '%{http_code}' \
      "${url}" 2>/dev/null) || true
  else
    code=$(curl "${CURL_OPTS[@]}" "${AUTH_HEADERS[@]}" \
      -X "${method}" \
      -o "${curl_body_file}" -w '%{http_code}' \
      "${url}" 2>/dev/null) || true
  fi
  [[ "${code}" =~ ^[0-9]{3}$ ]] || code="000"
  printf '%s' "${code}"
}

# Assert HTTP status. Emits FAIL with single-line context on miss.
# Usage: assert_http_status <gate> <actual_code> <expected_csv> <description>
assert_http_status() {
  local gate=$1 actual=$2 expected_csv=$3 desc=$4
  local IFS=','
  local exp
  for exp in ${expected_csv}; do
    if [[ "${actual}" == "${exp}" ]]; then
      return 0
    fi
  done
  fail "${gate}" "${desc} returned HTTP ${actual}; want ${expected_csv}"
}

# JSON field assertion via `jq -e`. Reads body file from
# `CURL_LAST_BODY_FILE` unless the caller passes a different path.
# Usage: assert_json <gate> <jq_expression> <description> [<body_file>]
assert_json() {
  local gate=$1 expr=$2 desc=$3
  local body_file=${4:-${CURL_LAST_BODY_FILE}}
  if [[ -z "${body_file}" && -n "${CURL_LAST_BODY_SENTINEL:-}" && -f "${CURL_LAST_BODY_SENTINEL}" ]]; then
    body_file=$(cat "${CURL_LAST_BODY_SENTINEL}" 2>/dev/null)
  fi
  if [[ -z "${body_file}" || ! -f "${body_file}" ]]; then
    fail "${gate}" "no JSON body to assert (${desc})"
  fi
  local jq_body_file
  jq_body_file="$(to_jq_path "${body_file}")"
  if ! "${JQ_BIN}" -e "${expr}" "${jq_body_file}" >/dev/null 2>&1; then
    fail "${gate}" "${desc} (jq expr: ${expr})"
  fi
}

# Assert that the response body's `.reason` field is a non-empty string and
# contains the given substring (case-insensitive). EDGE-056 introduced this
# helper so reject/expired/consumed terminal states get tightened reason-
# substring assertions without each gate hand-rolling the jq filter. Body file
# defaults to the same fallback chain as `assert_json` (caller-supplied →
# CURL_LAST_BODY_FILE → CURL_LAST_BODY_SENTINEL).
# Usage: assert_reason_contains <gate> <substring> [<body_file>]
assert_reason_contains() {
  local gate=$1 substr=$2
  local body_file=${3:-${CURL_LAST_BODY_FILE}}
  if [[ -z "${body_file}" && -n "${CURL_LAST_BODY_SENTINEL:-}" && -f "${CURL_LAST_BODY_SENTINEL}" ]]; then
    body_file=$(cat "${CURL_LAST_BODY_SENTINEL}" 2>/dev/null)
  fi
  if [[ -z "${body_file}" || ! -f "${body_file}" ]]; then
    fail "${gate}" "no JSON body to assert (.reason contains '${substr}')"
  fi
  local jq_body_file
  jq_body_file="$(to_jq_path "${body_file}")"
  if ! "${JQ_BIN}" -e --arg s "${substr}" \
    '(.reason | type == "string") and (.reason | test($s; "i"))' \
    "${jq_body_file}" >/dev/null 2>&1; then
    fail "${gate}" ".reason field missing or does not contain '${substr}' (case-insensitive)"
  fi
}

# JSON field extraction via `jq -r`. Returns empty string on miss; caller
# decides whether to FAIL.
# Usage: extract_json <jq_expression> [<body_file>]
extract_json() {
  local expr=$1
  local body_file=${2:-${CURL_LAST_BODY_FILE}}
  # Fallback to sentinel: when curl_request was called via `code=$(...)`,
  # the parent shell's CURL_LAST_BODY_FILE is stale. The sentinel file
  # holds the most recent body path written by curl_request itself.
  if [[ -z "${body_file}" && -n "${CURL_LAST_BODY_SENTINEL:-}" && -f "${CURL_LAST_BODY_SENTINEL}" ]]; then
    body_file=$(cat "${CURL_LAST_BODY_SENTINEL}" 2>/dev/null)
  fi
  if [[ -z "${body_file}" || ! -f "${body_file}" ]]; then
    printf ''
    return 0
  fi
  local jq_body_file
  jq_body_file="$(to_jq_path "${body_file}")"
  "${JQ_BIN}" -r "${expr}" "${jq_body_file}" 2>/dev/null || printf ''
}

# Negative assertion — body MUST NOT contain the given byte sequence.
# Used for redaction sanity checks (e.g., no raw secrets in event payloads).
# Usage: assert_body_does_not_contain <gate> <needle> <description> [<body_file>]
assert_body_does_not_contain() {
  local gate=$1 needle=$2 desc=$3
  local body_file=${4:-${CURL_LAST_BODY_FILE}}
  if [[ -z "${body_file}" || ! -f "${body_file}" ]]; then
    return 0
  fi
  if grep -F -q -- "${needle}" "${body_file}" 2>/dev/null; then
    fail "${gate}" "${desc}: response unexpectedly contained literal: ${needle}"
  fi
}

# Bounded retry loop. Exits with EXIT_TIMEOUT on miss.
#
# Usage: retry_until <max_iterations> <sleep_seconds> <gate_for_timeout> \
#                    <description> <command...>
retry_until() {
  local max=$1 sleep_sec=$2 gate=$3 desc=$4
  shift 4
  local i
  for ((i=0; i<max; i++)); do
    if "$@"; then
      return 0
    fi
    sleep "${sleep_sec}"
  done
  printf 'FAIL %s: timed out after %d attempts: %s\n' "${gate}" "${max}" "${desc}" >&2
  exit "${EXIT_TIMEOUT}"
}

# ---------------------------------------------------------------------------
# Redaction-safe logging helpers.
#
# NEVER pass tool_input bodies, file contents, .env path values that resolve
# to a real file, or anything from a curl response body to these helpers.
# Callers must restrict log content to:
#   - synthetic markers (script-generated identifiers)
#   - decision labels (allow/deny/ask)
#   - HTTP codes
#   - gate names from this script
# These helpers exist so future readers see exactly which data points are
# safe to log; everything else stays in the body file.
# ---------------------------------------------------------------------------
log_decision() {
  local gate=$1 decision=$2
  printf '[%s] %s decision=%s\n' "${SCRIPT_NAME}" "${gate}" "${decision}" >&2
}

log_http() {
  local gate=$1 method=$2 path=$3 code=$4
  printf '[%s] %s %s %s -> HTTP %s\n' "${SCRIPT_NAME}" "${gate}" "${method}" "${path}" "${code}" >&2
}

# ---------------------------------------------------------------------------
# Temp state + cleanup. init_tempdir creates a per-run scratch dir under
# the OS tempdir and registers an EXIT trap. cleanup_on_exit is idempotent
# so the trap is safe on every exit path (success, FAIL, SIGINT, parent
# kill). Hook-mode also uses the same trap to terminate the agentd
# subprocess; trap_kill_agentd is best-effort and never raises errors.
# ---------------------------------------------------------------------------
TMP_ROOT=""
AGENTD_PID=""
AGENTD_PORT=""
AGENTD_NONCE=""
AGENTD_URL=""
AGENTD_STATE_DIR=""
HOOK_BIN=""
AGENTD_BIN=""

trap_kill_agentd() {
  if [[ -z "${AGENTD_PID}" ]]; then
    return 0
  fi
  if kill -0 "${AGENTD_PID}" 2>/dev/null; then
    kill -TERM "${AGENTD_PID}" 2>/dev/null || true
    local i
    for ((i=0; i<20; i++)); do
      kill -0 "${AGENTD_PID}" 2>/dev/null || break
      sleep 0.1
    done
    kill -KILL "${AGENTD_PID}" 2>/dev/null || true
  fi
  AGENTD_PID=""
}

cleanup_on_exit() {
  local rc=$?
  trap - EXIT
  set +e
  trap_kill_agentd
  if want_keep_tmp; then
    [[ -n "${TMP_ROOT}" && -d "${TMP_ROOT}" ]] && log "preserving temp dir: ${TMP_ROOT}"
  else
    if [[ -n "${TMP_ROOT}" && -d "${TMP_ROOT}" ]]; then
      rm -rf "${TMP_ROOT}"
    fi
  fi
  return "${rc}"
}

init_tempdir() {
  TMP_ROOT=$(mktemp -d -t edge_fake_hook_e2e.XXXXXX)
  trap cleanup_on_exit EXIT
}

# ---------------------------------------------------------------------------
# Hook-mode lifecycle helpers (default mode; bypassed when
# CORDUM_EDGE_E2E_BYPASS_HOOK=1).
#
# locate_or_build_binaries: prefer existing ./bin/cordum-hook +
# ./bin/cordum-agentd; otherwise build via `go build` if a toolchain is on
# PATH. Never downloads from the network. Sets HOOK_BIN + AGENTD_BIN.
#
# pick_agentd_port: chooses a free TCP port on 127.0.0.1 unless
# CORDUM_EDGE_E2E_AGENTD_PORT is non-zero, in which case it uses that.
# Uses Python or `bash + /dev/tcp` probing; falls back to the documented
# default 8765 when no probe tool is available. Does NOT collide-check
# the override; operators set it deliberately.
#
# generate_agentd_nonce: 32 bytes of crypto/rand encoded base64. Matches
# the validator at core/edge/agentd/app.go ValidateExternalNonce. Never
# echoes the value; only logs `nonce_bytes=<count>` for visibility.
#
# start_agentd: spawns ./bin/cordum-agentd as a child process bound to
# 127.0.0.1:<AGENTD_PORT> with CORDUM_AGENTD_NONCE set. Writes stderr
# to a temp file under TMP_ROOT for post-mortem if the wait loop fails;
# never echoed to the script's stdout in normal flow. Sets AGENTD_PID.
#
# wait_agentd_ready: bounded loop polling the agentd hook URL with a
# short POST. Server-up signal is HTTP 401 (the nonce in the probe is
# either absent or wrong, but the server answers — proves bind + handler
# are wired). Other 4xx are also accepted as "server up". Connection
# refused or timeouts loop until budget expires.
#
# compose_agentd_url: assembles the bare
# `http://127.0.0.1:<port>/v1/edge/hooks/claude` endpoint. The matching nonce
# is passed only through CORDUM_AGENTD_HOOK_NONCE in run_hook.
#
# run_hook: pipes a synthetic Claude hook JSON to ./bin/cordum-hook with
# the verified env (CORDUM_AGENTD_URL, CORDUM_EDGE_SESSION_ID,
# CORDUM_EDGE_EXECUTION_ID, optional CORDUM_AGENTD_FAIL_CLOSED). Captures
# stdout into a callee-supplied file, stderr into a separate file. The
# caller then `assert_json` against the stdout file the same way it
# already does against curl response bodies.
# ---------------------------------------------------------------------------
locate_or_build_binaries() {
  local gate=${1:-edge_fake_hook_e2e}
  HOOK_BIN="./bin/cordum-hook"
  AGENTD_BIN="./bin/cordum-agentd"
  local needs_build=0

  if [[ ! -x "${HOOK_BIN}" || ! -x "${AGENTD_BIN}" ]]; then
    needs_build=1
  elif have_cmd find && {
    find ./cmd/cordum-hook ./cmd/cordum-agentd ./core/edge ./core/controlplane/gateway \
      -type f \( -name '*.go' -o -name '*.json' -o -name '*.yaml' -o -name '*.yml' \) \
      \( -newer "${HOOK_BIN}" -o -newer "${AGENTD_BIN}" \) -print -quit 2>/dev/null | grep -q .
  }; then
    needs_build=1
    log 'existing ./bin/cordum-hook or ./bin/cordum-agentd is older than Edge source; rebuilding'
  fi

  if [[ "${needs_build}" == "1" ]]; then
    if ! have_cmd go; then
      if is_integration_mode; then
        printf 'FAIL %s: cordum-hook/cordum-agentd binaries missing and `go` not on PATH (build them or set CORDUM_EDGE_E2E_BYPASS_HOOK=1)\n' "${gate}" >&2
        exit "${EXIT_PREREQ}"
      fi
      skip 'cordum-hook/cordum-agentd binaries missing and `go` not on PATH'
    fi
    log 'building ./bin/cordum-hook + ./bin/cordum-agentd via `go build`'
    if ! go build -o "${HOOK_BIN}" ./cmd/cordum-hook >&2; then
      fail "${gate}" 'go build ./cmd/cordum-hook failed'
    fi
    if ! go build -o "${AGENTD_BIN}" ./cmd/cordum-agentd >&2; then
      fail "${gate}" 'go build ./cmd/cordum-agentd failed'
    fi
  fi
  log "hook binary: ${HOOK_BIN}"
  log "agentd binary: ${AGENTD_BIN}"
}

pick_agentd_port() {
  if [[ -n "${CORDUM_EDGE_E2E_AGENTD_PORT}" && "${CORDUM_EDGE_E2E_AGENTD_PORT}" != "0" ]]; then
    AGENTD_PORT="${CORDUM_EDGE_E2E_AGENTD_PORT}"
    return 0
  fi
  if have_cmd python; then
    AGENTD_PORT=$(python -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()' 2>/dev/null || true)
  fi
  if [[ -z "${AGENTD_PORT}" ]] && have_cmd python3; then
    AGENTD_PORT=$(python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()' 2>/dev/null || true)
  fi
  if [[ -z "${AGENTD_PORT}" ]]; then
    # Fallback: try the documented default. Operator can override via env.
    AGENTD_PORT="8765"
    log "warning: no python on PATH; falling back to default agentd port ${AGENTD_PORT}"
  fi
}

generate_agentd_nonce() {
  local gate=${1:-edge_fake_hook_e2e}
  if ! have_cmd openssl; then
    if is_integration_mode; then
      printf 'FAIL %s: openssl not on PATH (required for CORDUM_AGENTD_NONCE generation; install openssl or set CORDUM_EDGE_E2E_BYPASS_HOOK=1)\n' "${gate}" >&2
      exit "${EXIT_PREREQ}"
    fi
    skip 'openssl not on PATH; required for nonce generation in hook mode'
  fi
  AGENTD_NONCE=$(openssl rand -base64 32 2>/dev/null | tr -d '\r\n')
  if [[ -z "${AGENTD_NONCE}" ]]; then
    fail "${gate}" 'openssl rand returned empty nonce'
  fi
  # Length tells us bytes-after-decode without echoing the value itself.
  log "agentd nonce generated (${#AGENTD_NONCE} chars base64; >=32 bytes raw)"
}

compose_agentd_url() {
  AGENTD_URL="http://127.0.0.1:${AGENTD_PORT}/v1/edge/hooks/claude"
}

start_agentd() {
  local gate=${1:-edge_fake_hook_e2e}
  AGENTD_STATE_DIR="${TMP_ROOT}/agentd_state"
  mkdir -p "${AGENTD_STATE_DIR}"
  local agentd_socket="http://127.0.0.1:${AGENTD_PORT}/v1/edge/hooks/claude"
  local agentd_stderr="${TMP_ROOT}/agentd.stderr"
  log "starting cordum-agentd on 127.0.0.1:${AGENTD_PORT} (state-dir=${AGENTD_STATE_DIR})"
  # Go's HTTP client uses the system trust store. When the Gateway uses a
  # locally-issued CA (./certs/ca/ca.crt or $CORDUM_TLS_CA), inject
  # SSL_CERT_FILE so the agentd subprocess can validate the TLS chain.
  local agentd_ssl_cert_file=""
  if [[ -n "${CORDUM_TLS_CA}" ]]; then
    agentd_ssl_cert_file="${CORDUM_TLS_CA}"
  elif [[ -f "./certs/ca/ca.crt" ]]; then
    agentd_ssl_cert_file="$(pwd)/certs/ca/ca.crt"
  fi
  if [[ -n "${agentd_ssl_cert_file}" ]]; then
    agentd_ssl_cert_file="$(to_curl_path "${agentd_ssl_cert_file}")"
  fi
  # GODEBUG x509usefallbackroots=1 forces Go's TLS layer to honor
  # SSL_CERT_FILE / SSL_CERT_DIR on Windows where it would otherwise use
  # the platform certificate store. Required when the local Gateway uses
  # a self-issued CA not present in the Windows root store.
  CORDUM_AGENTD_NONCE="${AGENTD_NONCE}" \
    CORDUM_GATEWAY="${API_BASE}" \
    CORDUM_API_KEY="${CORDUM_API_KEY}" \
    CORDUM_TENANT_ID="${CORDUM_TENANT_ID}" \
    CORDUM_PRINCIPAL_ID="${EDGE_PRINCIPAL_ID}" \
    CORDUM_AGENTD_SOCKET="${agentd_socket}" \
    CORDUM_AGENTD_STATE_DIR="${AGENTD_STATE_DIR}" \
    CORDUM_EDGE_SESSION_ID="${EDGE_SESSION_ID}" \
    CORDUM_EDGE_EXECUTION_ID="${EDGE_EXECUTION_ID}" \
    CORDUM_TLS_CA="${agentd_ssl_cert_file}" \
    SSL_CERT_FILE="${agentd_ssl_cert_file}" \
    SSL_CERT_DIR="$(dirname "${agentd_ssl_cert_file}")" \
    "${AGENTD_BIN}" >/dev/null 2>"${agentd_stderr}" &
  AGENTD_PID=$!
  log "agentd PID=${AGENTD_PID}"
}

wait_agentd_ready() {
  local gate=${1:-edge_fake_hook_e2e}
  local probe_url="http://127.0.0.1:${AGENTD_PORT}/v1/edge/hooks/claude"
  local deadline=$((CORDUM_EDGE_E2E_TIMEOUT * 10))
  local i code
  for ((i=0; i<deadline; i++)); do
    if [[ -n "${AGENTD_PID}" ]] && ! kill -0 "${AGENTD_PID}" 2>/dev/null; then
      log "agentd subprocess exited prematurely; stderr (truncated):"
      head -c 4096 "${TMP_ROOT}/agentd.stderr" >&2 2>/dev/null || true
      fail "${gate}" "agentd subprocess (PID ${AGENTD_PID}) exited before becoming ready"
    fi
    code=$(curl --silent --show-error --max-time 1 \
      -X POST -H 'Content-Type: application/json' -d '{}' \
      -o "$(to_curl_path /dev/null)" -w '%{http_code}' \
      "${probe_url}" 2>/dev/null) || true
    [[ "${code}" =~ ^[0-9]{3}$ ]] || code="000"
    case "${code}" in
      401|400|405|413) return 0 ;;  # server is up; auth/body error proves handler reachable
    esac
    sleep 0.1
  done
  log "agentd readiness probe timed out; stderr (truncated):"
  head -c 4096 "${TMP_ROOT}/agentd.stderr" >&2 2>/dev/null || true
  fail "${gate}" "agentd did not become ready within ${CORDUM_EDGE_E2E_TIMEOUT}s"
}

# Pipe synthetic Claude hook JSON to cordum-hook. Stdout (Claude-compatible
# JSON) goes to the caller-supplied file; stderr goes to a sibling file.
# Returns the hook's exit code.
#
# Usage: run_hook <event_subcommand> <stdin_json> <stdout_file>
#   event_subcommand: pre-tool-use | post-tool-use | post-tool-use-failure |
#                     user-prompt-submit | config-change | file-changed
HOOK_LAST_STDERR_FILE=""
run_hook() {
  local event=$1 stdin_json=$2 stdout_file=$3
  local stderr_file
  stderr_file=$(mktemp -p "${TMP_ROOT}" hook_stderr.XXXXXX)
  HOOK_LAST_STDERR_FILE="${stderr_file}"
  local rc=0
  CORDUM_AGENTD_URL="${AGENTD_URL}" \
    CORDUM_AGENTD_HOOK_NONCE="${AGENTD_NONCE}" \
    CORDUM_EDGE_SESSION_ID="${EDGE_SESSION_ID}" \
    CORDUM_EDGE_EXECUTION_ID="${EDGE_EXECUTION_ID}" \
    CORDUM_EDGE_PRINCIPAL_ID="${EDGE_PRINCIPAL_ID}" \
    CORDUM_TENANT_ID="${CORDUM_TENANT_ID}" \
    "${HOOK_BIN}" claude "${event}" \
    >"${stdout_file}" 2>"${stderr_file}" \
    <<<"${stdin_json}" || rc=$?
  printf '%s' "${rc}"
}

# JSON assertion against a file (analog of assert_json which assumes the
# CURL_LAST_BODY_FILE global). Used after run_hook.
# Usage: assert_json_file <gate> <file> <jq_expr> <description>
assert_json_file() {
  local gate=$1 file=$2 expr=$3 desc=$4
  if [[ -z "${file}" || ! -f "${file}" ]]; then
    fail "${gate}" "no JSON body to assert (${desc})"
  fi
  local jq_file
  jq_file="$(to_jq_path "${file}")"
  if ! "${JQ_BIN}" -e "${expr}" "${jq_file}" >/dev/null 2>&1; then
    local hint=""
    if [[ -n "${HOOK_LAST_STDERR_FILE}" && -f "${HOOK_LAST_STDERR_FILE}" ]]; then
      hint=$(head -c 240 "${HOOK_LAST_STDERR_FILE}" | tr '\n' ' ')
    fi
    fail "${gate}" "${desc} (jq expr: ${expr}; hook stderr head: ${hint})"
  fi
}

extract_json_file() {
  local file=$1 expr=$2
  if [[ -z "${file}" || ! -f "${file}" ]]; then
    printf ''
    return 0
  fi
  local jq_file
  jq_file="$(to_jq_path "${file}")"
  "${JQ_BIN}" -r "${expr}" "${jq_file}" 2>/dev/null || printf ''
}

# ---------------------------------------------------------------------------
# Demo policy fixture seed/verify (EDGE-010 runtime overlay).
#
# Determinism contract (verified against HEAD):
#   - core/edge/classifier.go:492 does `strings.Contains(padded, "/.env")`,
#     so any path containing `/.env` classifies as path.class=secret +
#     risk_tags=secrets. The runtime overlay rule
#     `claude-code.deny-secret-reads` matches that exact label/risk shape
#     and produces decision=deny. Thus FIXTURE_DENY_PATH below DENIES
#     deterministically without uploading a tenant-pinned policy.
#   - The runtime overlay rule `claude-code.require-approval-for-edits`
#     matches capability=file.write + risk_tags=write — i.e. ANY Edit/Write
#     tool action — and produces decision=require_approval. The hook then
#     translates that to permissionDecision=deny + approval_ref in the
#     reason text (hook_output.go:46-53).
#
# We use only synthetic path STRINGS — never read or stat a real .env or
# protected.txt. The fixture directory is created so the strings resolve
# under TMP_ROOT for log clarity, but the files themselves are never
# written. lint_no_secret_log.sh + step-15 grep enforce that.
# ---------------------------------------------------------------------------
DEMO_POLICY_OVERLAY="examples/cordum-edge-pack/overlays/policy.fragment.yaml"
FIXTURE_DIR=""
FIXTURE_DENY_PATH=""
FIXTURE_APPROVE_PATH=""

verify_demo_policy_overlay() {
  local gate=${1:-edge_fake_hook_e2e}
  if [[ ! -f "${DEMO_POLICY_OVERLAY}" ]]; then
    if is_integration_mode; then
      printf 'FAIL %s: demo policy overlay missing: %s\n' "${gate}" "${DEMO_POLICY_OVERLAY}" >&2
      exit "${EXIT_PREREQ}"
    fi
    skip "demo policy overlay missing: ${DEMO_POLICY_OVERLAY}"
  fi
  # The overlay carries `version: edge-policy-demo-v0` — log the version
  # for transparency without echoing rule contents.
  local version
  version=$(grep -E '^version:' "${DEMO_POLICY_OVERLAY}" | head -1 | awk '{print $2}')
  log "demo policy overlay: ${DEMO_POLICY_OVERLAY} (${version:-unknown-version})"
  log "policy assumption: agentd loads cordum-edge-pack overlay for tenant ${CORDUM_TENANT_ID}"
}

setup_fixture_paths() {
  if [[ -z "${TMP_ROOT}" ]]; then
    fail edge_fake_hook_e2e 'setup_fixture_paths called before init_tempdir'
  fi
  FIXTURE_DIR="${TMP_ROOT}/fixture"
  mkdir -p "${FIXTURE_DIR}/src"
  # Path containment match -> classifier path.class=secret + risk_tags=secrets
  # -> deny-secret-reads rule. Path string only; file never created/read.
  FIXTURE_DENY_PATH="${FIXTURE_DIR}/.env"
  # Path containment match (/src/ + .go suffix) -> classifier
  # path.class=source_code + risk_tags=source_code. The cordum-edge-pack
  # ships demo+production policy fragments with overlapping rule IDs;
  # production rules sort later alphabetically and replace demo's during
  # mergePolicies (kernel.go:1608). Production's
  # `claude-code.require-approval-for-edits` requires path.class=source_code,
  # so the fixture is a fake .go path under /src/ to satisfy the narrower
  # production matcher. File is never created/read; the path string alone
  # drives the classifier.
  FIXTURE_APPROVE_PATH="${FIXTURE_DIR}/src/protected.go"
  log "fixture deny path (synthetic, never read): ${FIXTURE_DENY_PATH}"
  log "fixture approve path (synthetic, never read): ${FIXTURE_APPROVE_PATH}"
}

# ---------------------------------------------------------------------------
# Reachability probe — used to drive SKIP vs FAIL in default vs strict mode.
# Runs before init_curl_opts because the probe is unauthenticated and must
# work even when CORDUM_API_KEY is unset (default-mode SKIP path).
# ---------------------------------------------------------------------------
detect_api_base() {
  if [[ -n "${CORDUM_GATEWAY}" ]]; then
    printf '%s' "${CORDUM_GATEWAY}"
    return 0
  fi
  if [[ -z "${CORDUM_TLS_CA}" && -f "./certs/ca/ca.crt" ]]; then
    printf 'https://localhost:8081'
  elif [[ -n "${CORDUM_TLS_CA}" ]]; then
    printf 'https://localhost:8081'
  else
    printf 'http://localhost:8081'
  fi
}

probe_api_base_reachable() {
  local api_base=$1
  command -v curl >/dev/null 2>&1 || return 1
  local curl_opts=(--silent --show-error --max-time 3 --output "$(to_curl_path /dev/null)" --write-out '%{http_code}')
  if [[ -n "${CORDUM_TLS_CA}" ]] || [[ -f "./certs/ca/ca.crt" ]]; then
    local ca=${CORDUM_TLS_CA:-./certs/ca/ca.crt}
    curl_opts+=(--cacert "$(to_curl_path "${ca}")")
  fi
  # Match init_curl_opts behavior: Windows schannel curl can fail TLS
  # revocation checks against locally-issued CAs even though the cert is
  # otherwise valid. Mirror the --ssl-no-revoke flag here so the probe
  # doesn't return SKIP/FAIL on a working stack.
  if curl --version 2>/dev/null | grep -qi schannel; then
    curl_opts+=(--ssl-no-revoke)
  fi
  local code
  # EDGE-051: Windows-schannel curl 8.16 exits non-zero on a non-fatal cert
  # diagnostic even when --write-out wrote a valid HTTP code to stdout.
  # Trust stdout if it looks like a 3-digit status; otherwise treat as
  # no-response. Mirrors the EDGE-051 fix in curl_request above.
  code=$(curl "${curl_opts[@]}" "${api_base}/api/v1/health" 2>/dev/null) || true
  [[ "${code}" =~ ^[0-9]{3}$ ]] || return 1
  # Any 2xx/3xx/4xx means the Gateway answered; only network failure -> SKIP.
  case "${code}" in
    2*|3*|4*) return 0 ;;
    *)        return 1 ;;
  esac
}

# ---------------------------------------------------------------------------
# Module-global state populated by gate_session_setup and consumed by
# subsequent gates (PreToolUse deny, approval flow, PostToolUse, export).
# ---------------------------------------------------------------------------
API_BASE=""
EDGE_SESSION_ID=""
EDGE_EXECUTION_ID=""
# Set EDGE_PRINCIPAL_ID at script init (uses $$ once) so init_curl_opts can
# bake the X-Principal-Id header before any Edge call fires. The basic auth
# provider reads X-Principal-Id when API-key auth alone leaves the principal
# blank (core/controlplane/gateway/auth/basic.go:HeaderValue) — required by
# EDGE-008.7 hardening on resolveEdgeAuthPrincipal which refuses body fields.
EDGE_PRINCIPAL_ID="edge-fake-hook-e2e-$$"

# ---------------------------------------------------------------------------
# Gate routing — default is hook mode (EDGE-027 acceptance path); bypass
# mode (CORDUM_EDGE_E2E_BYPASS_HOOK=1) keeps the original Gateway-direct
# semantics for CI hosts without hook/agentd binaries.
#
# Hook mode flow:
#   gate_session_setup            -> Gateway-direct create + round-trip GETs
#                                    (also exports CORDUM_EDGE_SESSION_ID +
#                                    CORDUM_EDGE_EXECUTION_ID for the hook
#                                    subprocess via run_hook helper).
#   gate_pretooluse_deny          -> stdin Claude hook JSON to cordum-hook
#                                    (event=pre-tool-use); assert
#                                    permissionDecision == "deny" + verify
#                                    Gateway events listing recorded the DENY.
#   gate_approval_flow            -> 3 sequential cordum-hook invocations
#                                    (initial REQUIRE_APPROVAL, retry ALLOW
#                                    after Gateway-direct approve, terminal
#                                    DENY); approve still hits Gateway
#                                    /api/v1/edge/approvals/{ref}/approve.
#   gate_posttooluse_artifact     -> stdin Claude hook JSON to cordum-hook
#                                    (event=post-tool-use). Direct /events
#                                    POST stays available in bypass mode.
#   gate_evidence_export          -> Gateway-direct POST in BOTH modes;
#                                    export is admin-side and never flows
#                                    through agentd.
#
# EDGE-017.4.1: hook-mode uses header-only nonce auth. PASS line shapes stay
# stable so QA evidence does not churn across the migration.
# ---------------------------------------------------------------------------

# ---------------------------------------------------------------------------
# Gate: edge_session_setup (step 7).
#
# Creates an EdgeSession + initial AgentExecution via the Gateway directly,
# captures the IDs, and round-trips GET /sessions/{id} + /executions/{id} to
# confirm tenant isolation.
# ---------------------------------------------------------------------------
gate_session_setup() {
  local gate=edge_session_setup

  local body
  body=$(cat <<JSON
{
  "principal_id": "${EDGE_PRINCIPAL_ID}",
  "principal_type": "service",
  "agent_product": "cordum-edge-fake-hook-e2e",
  "agent_version": "0.1.0",
  "mode": "ci",
  "cwd": "${TMP_ROOT}",
  "host_id": "edge-e2e-host",
  "device_id": "edge-e2e-device",
  "policy_mode": "enforce"
}
JSON
)
  local code
  code=$(curl_request POST "${API_BASE}/api/v1/edge/sessions" "${body}")
  log_http "${gate}" POST "/api/v1/edge/sessions" "${code}"
  assert_http_status "${gate}" "${code}" "201" "create edge session"
  EDGE_SESSION_ID=$(extract_json '.session_id // empty')
  if [[ -z "${EDGE_SESSION_ID}" ]]; then
    fail "${gate}" 'create-session response missing session_id'
  fi
  log "edge session_id=${EDGE_SESSION_ID}"

  body=$(cat <<JSON
{
  "session_id": "${EDGE_SESSION_ID}",
  "adapter": "claude-code-hook",
  "mode": "ci",
  "attempt": 0
}
JSON
)
  code=$(curl_request POST "${API_BASE}/api/v1/edge/executions" "${body}")
  log_http "${gate}" POST "/api/v1/edge/executions" "${code}"
  assert_http_status "${gate}" "${code}" "201" "create edge execution"
  EDGE_EXECUTION_ID=$(extract_json '.execution_id // empty')
  if [[ -z "${EDGE_EXECUTION_ID}" ]]; then
    fail "${gate}" 'create-execution response missing execution_id'
  fi
  log "edge execution_id=${EDGE_EXECUTION_ID}"

  # Round-trip GETs confirm tenant isolation: 200 + matching IDs.
  code=$(curl_request GET "${API_BASE}/api/v1/edge/sessions/${EDGE_SESSION_ID}")
  log_http "${gate}" GET "/api/v1/edge/sessions/${EDGE_SESSION_ID}" "${code}"
  assert_http_status "${gate}" "${code}" "200" "GET edge session"
  assert_json "${gate}" '.session_id == "'"${EDGE_SESSION_ID}"'"' 'session_id round-trip mismatch'
  assert_json "${gate}" '.tenant_id == "'"${CORDUM_TENANT_ID}"'"' "session.tenant_id != ${CORDUM_TENANT_ID}"

  code=$(curl_request GET "${API_BASE}/api/v1/edge/executions/${EDGE_EXECUTION_ID}")
  log_http "${gate}" GET "/api/v1/edge/executions/${EDGE_EXECUTION_ID}" "${code}"
  assert_http_status "${gate}" "${code}" "200" "GET edge execution"
  assert_json "${gate}" '.execution_id == "'"${EDGE_EXECUTION_ID}"'"' 'execution_id round-trip mismatch'
  assert_json "${gate}" '.session_id == "'"${EDGE_SESSION_ID}"'"' 'execution.session_id != session.session_id'

  pass "${gate}"
}

# ---------------------------------------------------------------------------
# Gate: edge_pretooluse_deny (step 8).
#
# Default (hook) mode: pipes synthetic Claude PreToolUse JSON through
# cordum-hook against the locally-spawned cordum-agentd. Asserts the hook
# stdout carries permissionDecision == "deny" + a non-empty reason, then
# verifies the Gateway recorded the DENY decision in the session events
# log. The classifier (core/edge/classifier.go:492 strings.Contains
# "/.env") tags `<tmpdir>/fixture/.env` reads as path.class=secret +
# risk_tags=secrets, which the EDGE-010 demo overlay rule
# `claude-code.deny-secret-reads` denies.
#
# Bypass mode (CORDUM_EDGE_E2E_BYPASS_HOOK=1): POSTs the equivalent
# evaluate request directly to /api/v1/edge/evaluate. PASS line shape
# stays identical so QA evidence does not churn between modes.
# ---------------------------------------------------------------------------
gate_pretooluse_deny() {
  if want_bypass_hook; then
    gate_pretooluse_deny_bypass
  else
    gate_pretooluse_deny_hook
  fi
}

gate_pretooluse_deny_hook() {
  local gate=edge_pretooluse_deny

  local hook_input
  hook_input=$(cat <<JSON
{
  "hook_event_name": "PreToolUse",
  "session_id": "${EDGE_SESSION_ID}",
  "tool_name": "Read",
  "tool_input": {
    "file_path": "${FIXTURE_DENY_PATH}"
  },
  "cwd": "${TMP_ROOT}"
}
JSON
)

  local stdout_file
  stdout_file=$(mktemp -p "${TMP_ROOT}" hook_stdout.XXXXXX)
  local rc
  rc=$(run_hook pre-tool-use "${hook_input}" "${stdout_file}")
  log "cordum-hook claude pre-tool-use exit=${rc}"
  if [[ "${rc}" != "0" ]]; then
    fail "${gate}" "cordum-hook exit ${rc} on PreToolUse Read .env"
  fi

  # PreToolUse deny shape per core/edge/claude/hook_output.go preToolUseOutput:
  # decision "DENY" -> hookSpecificOutput.permissionDecision = "deny" with
  # a non-empty permissionDecisionReason.
  assert_json_file "${gate}" "${stdout_file}" \
    '.hookSpecificOutput.hookEventName == "PreToolUse"' \
    'hook stdout missing PreToolUse hookSpecificOutput'
  assert_json_file "${gate}" "${stdout_file}" \
    '.hookSpecificOutput.permissionDecision == "deny"' \
    'hook stdout permissionDecision != deny'
  assert_json_file "${gate}" "${stdout_file}" \
    '(.hookSpecificOutput.permissionDecisionReason | type == "string") and (.hookSpecificOutput.permissionDecisionReason | length > 0)' \
    'hook stdout permissionDecisionReason empty'

  local decision reason
  decision=$(extract_json_file "${stdout_file}" '.hookSpecificOutput.permissionDecision')
  reason=$(extract_json_file "${stdout_file}" '.hookSpecificOutput.permissionDecisionReason' | head -c 120)
  log_decision "${gate}" "${decision}"
  log "deny reason (truncated): ${reason}"

  # Confirm the DENY decision was persisted as an event under this execution
  # via the agentd -> Gateway path. Same Gateway listing as bypass mode.
  local code
  code=$(curl_request GET \
    "${API_BASE}/api/v1/edge/sessions/${EDGE_SESSION_ID}/events?decision=DENY&limit=200")
  log_http "${gate}" GET "/api/v1/edge/sessions/${EDGE_SESSION_ID}/events?decision=DENY" "${code}"
  assert_http_status "${gate}" "${code}" "200" "list DENY events"
  assert_json "${gate}" \
    '[.items[]? | select(.execution_id == "'"${EDGE_EXECUTION_ID}"'" and .decision == "DENY")] | length >= 1' \
    "no DENY event recorded for execution ${EDGE_EXECUTION_ID}"

  # Negative redaction sanity on hook stdout AND Gateway events response —
  # neither must echo secret-shaped tokens we never wrote.
  assert_body_does_not_contain "${gate}" "OPENAI_API_KEY" 'events listing contains a real-secret marker'
  assert_body_does_not_contain "${gate}" "AWS_SECRET_ACCESS_KEY" 'events listing contains a real-secret marker'
  if grep -F -q -- "OPENAI_API_KEY" "${stdout_file}" 2>/dev/null; then
    fail "${gate}" 'hook stdout unexpectedly contained OPENAI_API_KEY marker'
  fi
  if grep -F -q -- "AWS_SECRET_ACCESS_KEY" "${stdout_file}" 2>/dev/null; then
    fail "${gate}" 'hook stdout unexpectedly contained AWS_SECRET_ACCESS_KEY marker'
  fi

  pass "${gate}"
}

gate_pretooluse_deny_bypass() {
  local gate=edge_pretooluse_deny

  local body
  body=$(cat <<JSON
{
  "session_id": "${EDGE_SESSION_ID}",
  "execution_id": "${EDGE_EXECUTION_ID}",
  "principal_id": "${EDGE_PRINCIPAL_ID}",
  "agent_product": "cordum-edge-fake-hook-e2e",
  "layer": "hook",
  "kind": "hook.pre_tool_use",
  "tool_name": "Read",
  "input_redacted": {
    "file_path": "${FIXTURE_DENY_PATH}"
  },
  "cwd": "${TMP_ROOT}"
}
JSON
)
  local code
  code=$(curl_request POST "${API_BASE}/api/v1/edge/evaluate" "${body}")
  log_http "${gate}" POST "/api/v1/edge/evaluate" "${code}"
  assert_http_status "${gate}" "${code}" "200" "evaluate Read .env"

  # Server-side decision must be DENY with a non-empty reason and the
  # hook-friendly translation `permission_decision == "deny"`.
  assert_json "${gate}" '.decision == "DENY"' "evaluate decision != DENY"
  assert_json "${gate}" '(.reason | type == "string") and (.reason | length > 0)' 'evaluate reason empty'
  assert_json "${gate}" '.permission_decision == "deny"' 'permission_decision != deny'

  local decision reason
  decision=$(extract_json '.decision')
  reason=$(extract_json '.reason' | head -c 120)
  log_decision "${gate}" "${decision}"
  log "deny reason (truncated): ${reason}"

  # Confirm the DENY decision was persisted as an event under this execution.
  # Bound the read to events for the right execution and decision filter.
  code=$(curl_request GET \
    "${API_BASE}/api/v1/edge/sessions/${EDGE_SESSION_ID}/events?decision=DENY&limit=200")
  log_http "${gate}" GET "/api/v1/edge/sessions/${EDGE_SESSION_ID}/events?decision=DENY" "${code}"
  assert_http_status "${gate}" "${code}" "200" "list DENY events"
  assert_json "${gate}" \
    '[.items[]? | select(.execution_id == "'"${EDGE_EXECUTION_ID}"'" and .decision == "DENY")] | length >= 1' \
    "no DENY event recorded for execution ${EDGE_EXECUTION_ID}"

  # Negative redaction sanity: agentd/Gateway must NOT echo file body bytes.
  # Since the script never writes a real .env, common secret-shaped tokens
  # cannot legitimately appear in the response — if they do, evidence
  # redaction has regressed.
  assert_body_does_not_contain "${gate}" "OPENAI_API_KEY" 'evaluate response contains a real-secret marker'
  assert_body_does_not_contain "${gate}" "AWS_SECRET_ACCESS_KEY" 'evaluate response contains a real-secret marker'

  pass "${gate}"
}

# ---------------------------------------------------------------------------
# Gate: edge_approval_flow (step 9).
#
# Default (hook) mode: three sequential cordum-hook invocations against the
# locally-spawned cordum-agentd. Identical synthetic Edit hook JSON each
# time; agentd computes a stable action_hash so the second call consumes
# the approval and the third hits the terminal-after-consume path.
#
#   1. cordum-hook claude pre-tool-use (Edit on protected.txt)
#       -> permissionDecision == "deny" with reason containing
#          "approval_ref=<ref>; approve then retry the tool call"
#          (per core/edge/claude/hook_output.go preToolUseOutput
#          REQUIRE_APPROVAL branch).
#   2. GET  /api/v1/edge/approvals/{ref}            -> status=pending
#   3. POST /api/v1/edge/approvals/{ref}/approve    -> status=approved
#   4. cordum-hook claude pre-tool-use (same input) -> permissionDecision = "allow"
#   5. cordum-hook claude pre-tool-use (same input) -> permissionDecision = "deny"
#                                                       reason contains "already consumed"
#
# Bypass mode (CORDUM_EDGE_E2E_BYPASS_HOOK=1): exercises the same four-step
# Edge approval contract via Gateway-direct /api/v1/edge/evaluate calls.
#
# The terminal-on-third-retry shape is verified at
# `core/controlplane/gateway/handlers_edge_evaluate.go:768-769` —
# `edgeEvaluateRetryDeny(..., "approval already consumed; request a new approval")`.
# ---------------------------------------------------------------------------
gate_approval_flow() {
  if want_bypass_hook; then
    gate_approval_flow_bypass
  else
    gate_approval_flow_hook
  fi
}

gate_approval_flow_hook() {
  local gate=edge_approval_flow

  local hook_input
  hook_input=$(cat <<JSON
{
  "hook_event_name": "PreToolUse",
  "session_id": "${EDGE_SESSION_ID}",
  "tool_name": "Edit",
  "tool_input": {
    "file_path": "${FIXTURE_APPROVE_PATH}"
  },
  "cwd": "${TMP_ROOT}"
}
JSON
)

  # 1. First hook call — REQUIRE_APPROVAL surfaces as permissionDecision=deny
  # with approval_ref embedded in permissionDecisionReason.
  local stdout_initial stdout_consume stdout_terminal rc
  stdout_initial=$(mktemp -p "${TMP_ROOT}" hook_initial.XXXXXX)
  rc=$(run_hook pre-tool-use "${hook_input}" "${stdout_initial}")
  log "cordum-hook claude pre-tool-use (initial) exit=${rc}"
  if [[ "${rc}" != "0" ]]; then
    fail "${gate}" "cordum-hook initial call exit ${rc}"
  fi
  assert_json_file "${gate}" "${stdout_initial}" \
    '.hookSpecificOutput.permissionDecision == "deny"' \
    'initial hook permissionDecision != deny (REQUIRE_APPROVAL surfaces as deny+reason)'
  assert_json_file "${gate}" "${stdout_initial}" \
    '(.hookSpecificOutput.permissionDecisionReason | type == "string") and (.hookSpecificOutput.permissionDecisionReason | test("approval_ref="; "i"))' \
    'initial hook reason missing approval_ref= marker'

  local reason approval_ref
  reason=$(extract_json_file "${stdout_initial}" '.hookSpecificOutput.permissionDecisionReason')
  # Pattern matches the formatter at hook_output.go:53:
  #   "<base reason>; approval_ref=<edge_appr_...>; approve then retry the tool call"
  approval_ref=$(printf '%s' "${reason}" | sed -n 's/.*approval_ref=\(edge_appr_[A-Za-z0-9_-]\{1,\}\).*/\1/p' | head -1)
  if [[ -z "${approval_ref}" ]]; then
    fail "${gate}" 'could not extract approval_ref from hook reason'
  fi
  if [[ ! "${approval_ref}" =~ ^edge_appr_[A-Za-z0-9_-]+$ ]]; then
    fail "${gate}" "approval_ref does not match required pattern: ${approval_ref}"
  fi
  log "approval_ref=${approval_ref}"

  # 2. Approval is pending in the store (Gateway-direct read).
  local code
  code=$(curl_request GET "${API_BASE}/api/v1/edge/approvals/${approval_ref}")
  log_http "${gate}" GET "/api/v1/edge/approvals/${approval_ref}" "${code}"
  assert_http_status "${gate}" "${code}" "200" "GET approval"
  assert_json "${gate}" '.status == "pending"' 'pre-approve status != pending'
  assert_json "${gate}" '.session_id == "'"${EDGE_SESSION_ID}"'"' 'approval bound to wrong session'

  # 3. Resolve as approved (Gateway-direct in both modes — approver is
  # human-side and never flows through cordum-hook). The approver principal
  # MUST differ from the requester principal: edgeApprovalRequesterMatchesResolver
  # (handlers_edge_approvals.go:335) returns 403 self_approval_denied when
  # the resolver identity matches approval.Requester or approval.PrincipalID.
  # Use a separate synthetic approver so the test exercises the
  # require-approval path end-to-end without needing a second API key.
  local approve_body='{"reason":"edge_fake_hook_e2e synthetic approval"}'
  local saved_headers=("${AUTH_HEADERS[@]}")
  AUTH_HEADERS=(
    -H "X-API-Key: ${CORDUM_API_KEY}"
    -H "X-Tenant-ID: ${CORDUM_TENANT_ID}"
    -H "X-Principal-Id: edge-fake-hook-e2e-approver-$$"
  )
  code=$(curl_request POST "${API_BASE}/api/v1/edge/approvals/${approval_ref}/approve" "${approve_body}")
  AUTH_HEADERS=("${saved_headers[@]}")
  log_http "${gate}" POST "/api/v1/edge/approvals/${approval_ref}/approve" "${code}"
  if [[ "${code}" == "403" ]]; then
    fail "${gate}" 'approve returned 403 (likely self_approval_denied — script approver principal collided with requester)'
  fi
  assert_http_status "${gate}" "${code}" "200" "POST approval approve"
  assert_json "${gate}" '.status == "approved"' 'post-approve status != approved'

  # 4. Retry hook with same synthetic input — agentd matches the action_hash
  # against the approved record and consumes the claim. permissionDecision
  # transitions to "allow".
  stdout_consume=$(mktemp -p "${TMP_ROOT}" hook_consume.XXXXXX)
  rc=$(run_hook pre-tool-use "${hook_input}" "${stdout_consume}")
  log "cordum-hook claude pre-tool-use (consume) exit=${rc}"
  if [[ "${rc}" != "0" ]]; then
    fail "${gate}" "cordum-hook consume call exit ${rc}"
  fi
  assert_json_file "${gate}" "${stdout_consume}" \
    '.hookSpecificOutput.permissionDecision == "allow"' \
    'consume retry permissionDecision != allow'

  # 5. Third call — terminal "already consumed" path. permissionDecision
  # returns to "deny" with reason containing "already consumed".
  stdout_terminal=$(mktemp -p "${TMP_ROOT}" hook_terminal.XXXXXX)
  rc=$(run_hook pre-tool-use "${hook_input}" "${stdout_terminal}")
  log "cordum-hook claude pre-tool-use (terminal) exit=${rc}"
  if [[ "${rc}" != "0" ]]; then
    fail "${gate}" "cordum-hook terminal call exit ${rc}"
  fi
  assert_json_file "${gate}" "${stdout_terminal}" \
    '.hookSpecificOutput.permissionDecision == "deny"' \
    'terminal retry permissionDecision != deny'
  # EDGE-056 — retroactive use of assert_reason_contains is gated to the
  # body-file overload because hook-mode payloads carry the "already
  # consumed" marker under .hookSpecificOutput.permissionDecisionReason
  # (NOT .reason — that's the bypass-mode field). assert_reason_contains
  # only handles the .reason path, so the inline jq filter stays here.
  assert_json_file "${gate}" "${stdout_terminal}" \
    '(.hookSpecificOutput.permissionDecisionReason | tostring | test("already consumed"; "i"))' \
    "terminal retry hookSpecificOutput.permissionDecisionReason missing 'already consumed' marker"

  # Negative redaction sanity on each hook stdout file — secret tokens we
  # never wrote must not appear.
  local f
  for f in "${stdout_initial}" "${stdout_consume}" "${stdout_terminal}"; do
    if grep -F -q -- "OPENAI_API_KEY" "${f}" 2>/dev/null; then
      fail "${gate}" "hook stdout (${f}) leaked OPENAI_API_KEY marker"
    fi
    if grep -F -q -- "AWS_SECRET_ACCESS_KEY" "${f}" 2>/dev/null; then
      fail "${gate}" "hook stdout (${f}) leaked AWS_SECRET_ACCESS_KEY marker"
    fi
  done

  pass "${gate}"
}

gate_approval_flow_bypass() {
  local gate=edge_approval_flow

  # 1. First evaluate — REQUIRE_APPROVAL.
  local req
  req=$(cat <<JSON
{
  "session_id": "${EDGE_SESSION_ID}",
  "execution_id": "${EDGE_EXECUTION_ID}",
  "principal_id": "${EDGE_PRINCIPAL_ID}",
  "agent_product": "cordum-edge-fake-hook-e2e",
  "layer": "hook",
  "kind": "hook.pre_tool_use",
  "tool_name": "Edit",
  "input_redacted": {
    "file_path": "${FIXTURE_APPROVE_PATH}"
  },
  "cwd": "${TMP_ROOT}"
}
JSON
)
  local code
  code=$(curl_request POST "${API_BASE}/api/v1/edge/evaluate" "${req}")
  log_http "${gate}" POST "/api/v1/edge/evaluate (initial)" "${code}"
  assert_http_status "${gate}" "${code}" "200" "evaluate Edit (initial)"
  assert_json "${gate}" '.decision == "REQUIRE_APPROVAL"' 'initial evaluate did not require approval'
  local approval_ref
  approval_ref=$(extract_json '.approval_ref // empty')
  if [[ -z "${approval_ref}" ]]; then
    fail "${gate}" 'evaluate response missing approval_ref'
  fi
  if [[ ! "${approval_ref}" =~ ^edge_appr_[A-Za-z0-9_-]+$ ]]; then
    fail "${gate}" "approval_ref does not match required pattern: ${approval_ref}"
  fi
  log "approval_ref=${approval_ref}"

  # 2. Approval is pending in the store.
  code=$(curl_request GET "${API_BASE}/api/v1/edge/approvals/${approval_ref}")
  log_http "${gate}" GET "/api/v1/edge/approvals/${approval_ref}" "${code}"
  assert_http_status "${gate}" "${code}" "200" "GET approval"
  assert_json "${gate}" '.status == "pending"' 'pre-approve status != pending'
  assert_json "${gate}" '.session_id == "'"${EDGE_SESSION_ID}"'"' 'approval bound to wrong session'

  # 3. Resolve as approved. EdgeApprovalDecisionRequest body is optional;
  # supply a synthetic resolver reason so the audit record carries it.
  local approve_body='{"reason":"edge_fake_hook_e2e synthetic approval"}'
  code=$(curl_request POST "${API_BASE}/api/v1/edge/approvals/${approval_ref}/approve" "${approve_body}")
  log_http "${gate}" POST "/api/v1/edge/approvals/${approval_ref}/approve" "${code}"
  # 200 = approved; 403 self_approval_denied is possible when API key principal
  # matches the requester. Surface a directed error if that hits.
  if [[ "${code}" == "403" ]]; then
    fail "${gate}" 'approve returned 403 (likely self_approval_denied — set CORDUM_API_KEY to a different principal than the requester)'
  fi
  assert_http_status "${gate}" "${code}" "200" "POST approval approve"
  assert_json "${gate}" '.status == "approved"' 'post-approve status != approved'

  # 4. Retry evaluate with approval_ref — claim/consume — ALLOW.
  local retry_req
  retry_req=$(cat <<JSON
{
  "session_id": "${EDGE_SESSION_ID}",
  "execution_id": "${EDGE_EXECUTION_ID}",
  "principal_id": "${EDGE_PRINCIPAL_ID}",
  "agent_product": "cordum-edge-fake-hook-e2e",
  "layer": "hook",
  "kind": "hook.pre_tool_use",
  "tool_name": "Edit",
  "input_redacted": {
    "file_path": "${FIXTURE_APPROVE_PATH}"
  },
  "cwd": "${TMP_ROOT}",
  "approval_ref": "${approval_ref}"
}
JSON
)
  code=$(curl_request POST "${API_BASE}/api/v1/edge/evaluate" "${retry_req}")
  log_http "${gate}" POST "/api/v1/edge/evaluate (retry consume)" "${code}"
  assert_http_status "${gate}" "${code}" "200" "evaluate Edit (retry consume)"
  assert_json "${gate}" '.decision == "ALLOW"' 'consume retry did not return ALLOW'
  assert_json "${gate}" '.permission_decision == "allow"' 'consume retry permission_decision != allow'

  # 5. Third retry — same payload + approval_ref — must hit terminal "approval
  # already consumed" path (handlers_edge_evaluate.go:768-769).
  code=$(curl_request POST "${API_BASE}/api/v1/edge/evaluate" "${retry_req}")
  log_http "${gate}" POST "/api/v1/edge/evaluate (terminal)" "${code}"
  assert_http_status "${gate}" "${code}" "200" "evaluate Edit (third retry)"
  assert_json "${gate}" '.decision == "DENY"' 'third retry should be terminal DENY'
  assert_json "${gate}" '.permission_decision == "deny"' 'third retry permission_decision != deny'
  assert_reason_contains "${gate}" "already consumed"
  assert_json "${gate}" '.wait_after == "request_new_approval"' 'third retry wait_after != request_new_approval'

  # Negative redaction sanity (mirrors gate_pretooluse_deny).
  assert_body_does_not_contain "${gate}" "OPENAI_API_KEY" 'evaluate response contains a real-secret marker'
  assert_body_does_not_contain "${gate}" "AWS_SECRET_ACCESS_KEY" 'evaluate response contains a real-secret marker'

  pass "${gate}"
}

# ---------------------------------------------------------------------------
# Gate: edge_approval_rejected (EDGE-056 step 3).
#
# Mirrors gate_approval_flow_bypass but exercises the rejection terminal
# state instead of approve+consume. POSTs an Edit action that requires
# approval, captures the approval_ref, rejects via
# /api/v1/edge/approvals/{ref}/reject (with a synthetic resolver to dodge
# self_approval_denied), retries the same action, and asserts:
#   - retry decision = DENY
#   - retry .reason contains 'rejected' (case-insensitive)
#   - second reject attempt is non-2xx (rejection is terminal — same
#     approval_ref cannot be re-resolved)
# This closes the EDGE-039 / EDGE-042 class of bug at integration time:
# rejection's `permissionDecisionReason` (handlers_edge_evaluate.go:807)
# differs from the policy-deny copy, and full coverage prevents the two
# from accidentally diverging without anyone noticing.
#
# Bypass-mode-only by design: the server-side rejection lifecycle is
# identical regardless of whether the action arrives via cordum-hook or
# direct curl. The hook-mode counterpart would mostly duplicate
# gate_approval_flow_hook scaffolding for diminishing return; if a hook-
# specific bug surfaces, file as a follow-up.
# ---------------------------------------------------------------------------
gate_approval_rejected() {
  local gate=edge_approval_rejected

  # 1. First evaluate — REQUIRE_APPROVAL.
  local req
  req=$(cat <<JSON
{
  "session_id": "${EDGE_SESSION_ID}",
  "execution_id": "${EDGE_EXECUTION_ID}",
  "principal_id": "${EDGE_PRINCIPAL_ID}",
  "agent_product": "cordum-edge-fake-hook-e2e",
  "layer": "hook",
  "kind": "hook.pre_tool_use",
  "tool_name": "Edit",
  "input_redacted": {
    "file_path": "${FIXTURE_APPROVE_PATH}"
  },
  "cwd": "${TMP_ROOT}"
}
JSON
)
  local code
  code=$(curl_request POST "${API_BASE}/api/v1/edge/evaluate" "${req}")
  log_http "${gate}" POST "/api/v1/edge/evaluate (initial)" "${code}"
  assert_http_status "${gate}" "${code}" "200" "evaluate Edit (initial)"
  assert_json "${gate}" '.decision == "REQUIRE_APPROVAL"' 'initial evaluate did not require approval'
  local approval_ref
  approval_ref=$(extract_json '.approval_ref // empty')
  if [[ -z "${approval_ref}" ]]; then
    fail "${gate}" 'evaluate response missing approval_ref'
  fi
  if [[ ! "${approval_ref}" =~ ^edge_appr_[A-Za-z0-9_-]+$ ]]; then
    fail "${gate}" "approval_ref does not match required pattern: ${approval_ref}"
  fi
  log "approval_ref=${approval_ref}"

  # 2. Approval is pending in the store.
  code=$(curl_request GET "${API_BASE}/api/v1/edge/approvals/${approval_ref}")
  log_http "${gate}" GET "/api/v1/edge/approvals/${approval_ref}" "${code}"
  assert_http_status "${gate}" "${code}" "200" "GET approval"
  assert_json "${gate}" '.status == "pending"' 'pre-reject status != pending'
  assert_json "${gate}" '.session_id == "'"${EDGE_SESSION_ID}"'"' 'approval bound to wrong session'

  # 3. Resolve as rejected. Keep the literal word "rejected" in the
  # resolver's free-text reason because the evaluate path may surface that
  # reason verbatim instead of a generated "approval rejected" phrase.
  # Use a separate synthetic resolver principal so
  # the requester != resolver check at handlers_edge_approvals.go:335 does
  # not 403 the call as self_approval_denied (mirrors the approve gate's
  # dance at gate_approval_flow_bypass:L1207-1213, but here we override
  # AUTH_HEADERS in-place since the bypass gate already showed it works
  # without saving/restoring around a single call).
  local reject_body='{"reason":"edge_fake_hook_e2e synthetic approval rejected"}'
  local saved_headers=("${AUTH_HEADERS[@]}")
  AUTH_HEADERS=(
    -H "X-API-Key: ${CORDUM_API_KEY}"
    -H "X-Tenant-ID: ${CORDUM_TENANT_ID}"
    -H "X-Principal-Id: edge-fake-hook-e2e-approver-$$"
  )
  code=$(curl_request POST "${API_BASE}/api/v1/edge/approvals/${approval_ref}/reject" "${reject_body}")
  log_http "${gate}" POST "/api/v1/edge/approvals/${approval_ref}/reject" "${code}"
  if [[ "${code}" == "403" ]]; then
    AUTH_HEADERS=("${saved_headers[@]}")
    fail "${gate}" 'reject returned 403 (likely self_approval_denied — script approver principal collided with requester)'
  fi
  assert_http_status "${gate}" "${code}" "200" "POST approval reject"
  assert_json "${gate}" '.status == "rejected"' 'post-reject status != rejected'
  AUTH_HEADERS=("${saved_headers[@]}")

  # 4. Retry evaluate with the SAME action body — the gateway's auto-consume
  # path matches by action_hash (handlers_edge_evaluate.go:200 area) and finds
  # the rejected approval. Decision = DENY, reason = 'approval rejected' (or
  # the resolver's free-text reason if non-empty per
  # handlers_edge_evaluate.go:807).
  code=$(curl_request POST "${API_BASE}/api/v1/edge/evaluate" "${req}")
  log_http "${gate}" POST "/api/v1/edge/evaluate (retry post-reject)" "${code}"
  assert_http_status "${gate}" "${code}" "200" "evaluate Edit (retry post-reject)"
  assert_json "${gate}" '.decision == "DENY"' 'retry post-reject did not return DENY'
  assert_json "${gate}" '.permission_decision == "deny"' 'retry post-reject permission_decision != deny'
  assert_reason_contains "${gate}" "rejected"

  # 5. Negative — second reject of the same approval_ref must be non-2xx
  # (rejection is terminal; the approval is no longer in pending state and
  # cannot be re-resolved). This guards the "no approval re-issue happens"
  # half of DoD #1.
  saved_headers=("${AUTH_HEADERS[@]}")
  AUTH_HEADERS=(
    -H "X-API-Key: ${CORDUM_API_KEY}"
    -H "X-Tenant-ID: ${CORDUM_TENANT_ID}"
    -H "X-Principal-Id: edge-fake-hook-e2e-approver-$$"
  )
  code=$(curl_request POST "${API_BASE}/api/v1/edge/approvals/${approval_ref}/reject" "${reject_body}")
  AUTH_HEADERS=("${saved_headers[@]}")
  log_http "${gate}" POST "/api/v1/edge/approvals/${approval_ref}/reject (terminal)" "${code}"
  if [[ "${code}" == "200" ]]; then
    fail "${gate}" "second reject of terminal approval returned 200; rejection must be non-replayable"
  fi
  case "${code}" in
    409|422|404) ;;  # any 4xx terminal-state rejection is acceptable
    *) fail "${gate}" "second reject expected 4xx terminal-state error; got HTTP ${code}" ;;
  esac

  # Negative redaction sanity (mirrors gate_pretooluse_deny + flow_bypass).
  assert_body_does_not_contain "${gate}" "OPENAI_API_KEY" 'evaluate response contains a real-secret marker'
  assert_body_does_not_contain "${gate}" "AWS_SECRET_ACCESS_KEY" 'evaluate response contains a real-secret marker'

  pass "${gate}"
}

# ---------------------------------------------------------------------------
# Gate: edge_posttooluse_artifact (step 10).
#
# Default (hook) mode: pipes synthetic Claude PostToolUse JSON through
# cordum-hook. The hook → agentd → Gateway path records a
# `hook.post_tool_use` event for the active execution. Verification is
# against the Gateway session-events listing (artifact_ptr presence
# depends on agentd's evaluator decision and is asserted in bypass mode
# where the script controls the pointer shape directly).
#
# Bypass mode (CORDUM_EDGE_E2E_BYPASS_HOOK=1): POSTs /api/v1/edge/events
# directly with a hand-crafted PostToolUse event carrying a single
# artifact pointer. The payload contains a synthetic bounded summary
# ("wrote 64 bytes") — no raw file body bytes — and the pointer's `uri`
# uses the internal `artifact://...` scheme that the Edge artifact
# validator accepts (per EdgeArtifactPointer schema at OpenAPI L7969-7998).
# ---------------------------------------------------------------------------
gate_posttooluse_artifact() {
  if want_bypass_hook; then
    gate_posttooluse_artifact_bypass
  else
    gate_posttooluse_artifact_hook
  fi
}

gate_posttooluse_artifact_hook() {
  local gate=edge_posttooluse_artifact

  local hook_input
  hook_input=$(cat <<JSON
{
  "hook_event_name": "PostToolUse",
  "session_id": "${EDGE_SESSION_ID}",
  "tool_name": "Edit",
  "tool_input": {
    "file_path": "${FIXTURE_APPROVE_PATH}"
  },
  "tool_response": {
    "summary": "wrote 64 bytes",
    "file_path": "${FIXTURE_APPROVE_PATH}"
  },
  "duration_ms": 12,
  "cwd": "${TMP_ROOT}"
}
JSON
)

  local stdout_file
  stdout_file=$(mktemp -p "${TMP_ROOT}" hook_post.XXXXXX)
  local rc
  rc=$(run_hook post-tool-use "${hook_input}" "${stdout_file}")
  log "cordum-hook claude post-tool-use exit=${rc}"
  if [[ "${rc}" != "0" ]]; then
    fail "${gate}" "cordum-hook PostToolUse exit ${rc}"
  fi
  # PostToolUse output may legitimately be empty for ALLOW per
  # core/edge/claude/hook_output.go postToolUseOutput (only DENY/REQUIRE
  # produce a body). Don't assert on stdout shape; verify event recording
  # via the Gateway listing instead.

  # Confirm the event was persisted via the agentd → Gateway path. Filter
  # by kind=hook.post_tool_use and our execution.
  local code
  code=$(curl_request GET \
    "${API_BASE}/api/v1/edge/sessions/${EDGE_SESSION_ID}/events?kind=hook.post_tool_use&limit=200")
  log_http "${gate}" GET "/api/v1/edge/sessions/${EDGE_SESSION_ID}/events?kind=hook.post_tool_use" "${code}"
  assert_http_status "${gate}" "${code}" "200" "list PostToolUse events"
  assert_json "${gate}" \
    '[.items[]? | select(.execution_id == "'"${EDGE_EXECUTION_ID}"'" and .kind == "hook.post_tool_use")] | length >= 1' \
    "no hook.post_tool_use event recorded for execution ${EDGE_EXECUTION_ID}"
  assert_json "${gate}" \
    '[.items[]? | select(.execution_id == "'"${EDGE_EXECUTION_ID}"'" and .kind == "hook.post_tool_use" and .tool_name == "Edit")] | length >= 1' \
    "no hook.post_tool_use Edit event recorded for execution ${EDGE_EXECUTION_ID}"

  # Negative redaction sanity on hook stdout AND Gateway listing.
  assert_body_does_not_contain "${gate}" "OPENAI_API_KEY" 'events listing contains a real-secret marker'
  assert_body_does_not_contain "${gate}" "AWS_SECRET_ACCESS_KEY" 'events listing contains a real-secret marker'
  if grep -F -q -- "OPENAI_API_KEY" "${stdout_file}" 2>/dev/null; then
    fail "${gate}" 'hook stdout unexpectedly contained OPENAI_API_KEY marker'
  fi
  if grep -F -q -- "AWS_SECRET_ACCESS_KEY" "${stdout_file}" 2>/dev/null; then
    fail "${gate}" 'hook stdout unexpectedly contained AWS_SECRET_ACCESS_KEY marker'
  fi

  pass "${gate}"
}

gate_posttooluse_artifact_bypass() {
  local gate=edge_posttooluse_artifact

  # Synthetic deterministic-but-fake event id + sha256.
  local event_id="evt-edge-e2e-$(date +%s)-$$"
  local artifact_sha="sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
  local now_iso
  now_iso=$(date -u +%Y-%m-%dT%H:%M:%SZ)
  local artifact_uri="artifact://edge/${EDGE_SESSION_ID}/${EDGE_EXECUTION_ID}/${event_id}/tool_result"

  local body
  body=$(cat <<JSON
{
  "event_id": "${event_id}",
  "session_id": "${EDGE_SESSION_ID}",
  "execution_id": "${EDGE_EXECUTION_ID}",
  "principal_id": "${EDGE_PRINCIPAL_ID}",
  "ts": "${now_iso}",
  "layer": "hook",
  "kind": "hook.post_tool_use",
  "agent_product": "cordum-edge-fake-hook-e2e",
  "tool_name": "Edit",
  "input_redacted": {
    "summary": "wrote 64 bytes",
    "file_path": "${FIXTURE_APPROVE_PATH}"
  },
  "decision": "RECORDED",
  "status": "ok",
  "duration_ms": 12,
  "artifact_ptrs": [
    {
      "artifact_type": "edge.tool_result",
      "session_id": "${EDGE_SESSION_ID}",
      "execution_id": "${EDGE_EXECUTION_ID}",
      "event_id": "${event_id}",
      "tenant_id": "${CORDUM_TENANT_ID}",
      "retention_class": "standard",
      "redaction_level": "standard",
      "sha256": "${artifact_sha}",
      "uri": "${artifact_uri}",
      "created_at": "${now_iso}"
    }
  ]
}
JSON
)
  local code
  code=$(curl_request POST "${API_BASE}/api/v1/edge/events" "${body}")
  log_http "${gate}" POST "/api/v1/edge/events" "${code}"
  # Single-event POST returns 201 (created) per the gateway implementation.
  assert_http_status "${gate}" "${code}" "201,200" "POST PostToolUse event"
  # The response body is the persisted EdgeAgentActionEvent — confirm the
  # artifact pointer survived round-trip and matches what we sent.
  assert_json "${gate}" '.event_id == "'"${event_id}"'"' 'event_id mismatch'
  assert_json "${gate}" '.kind == "hook.post_tool_use"' 'kind != hook.post_tool_use'
  assert_json "${gate}" '.decision == "RECORDED"' 'decision != RECORDED'
  assert_json "${gate}" '(.artifact_ptrs | length) == 1' 'artifact_ptrs length != 1'
  assert_json "${gate}" '.artifact_ptrs[0].sha256 == "'"${artifact_sha}"'"' 'artifact pointer sha256 mismatch'
  assert_json "${gate}" '.artifact_ptrs[0].artifact_type == "edge.tool_result"' 'artifact_type != edge.tool_result'
  assert_json "${gate}" '.artifact_ptrs[0].uri == "'"${artifact_uri}"'"' 'artifact_uri mismatch'

  # Negative redaction sanity: response (and persisted event) MUST NOT echo
  # raw tool body bytes. Since we never sent any, common secret tokens
  # cannot legitimately appear; if they do, the redaction layer regressed.
  assert_body_does_not_contain "${gate}" "OPENAI_API_KEY" 'event response contains a real-secret marker'
  assert_body_does_not_contain "${gate}" "AWS_SECRET_ACCESS_KEY" 'event response contains a real-secret marker'

  # Confirm the event is reachable through the session events listing
  # (tenant-isolated read path) with a kind filter.
  code=$(curl_request GET \
    "${API_BASE}/api/v1/edge/sessions/${EDGE_SESSION_ID}/events?kind=hook.post_tool_use&limit=200")
  log_http "${gate}" GET "/api/v1/edge/sessions/${EDGE_SESSION_ID}/events?kind=hook.post_tool_use" "${code}"
  assert_http_status "${gate}" "${code}" "200" "list PostToolUse events"
  assert_json "${gate}" \
    '[.items[]? | select(.event_id == "'"${event_id}"'")] | length == 1' \
    "PostToolUse event ${event_id} not found via session-events listing"
  assert_json "${gate}" \
    '[.items[]? | select(.event_id == "'"${event_id}"'")][0].artifact_ptrs[0].sha256 == "'"${artifact_sha}"'"' \
    'listed event artifact pointer sha256 mismatch'

  pass "${gate}"
}

# ---------------------------------------------------------------------------
# Gate: edge_evidence_export (step 11).
#
# Mode-agnostic: export is admin-side and never flows through
# cordum-hook/agentd in either default or bypass mode. Bundle content
# differs slightly between modes (bypass crafts an explicit artifact
# pointer; hook mode relies on agentd to produce one if applicable), so
# the artifact_ptr assertion is gated on bypass mode below.
#
# Calls POST /api/v1/edge/sessions/{id}/export (verified at OpenAPI
# L1183 — POST, NOT GET as the original plan said) and verifies the
# returned `SessionExportBundle` contains:
#   - session/execution metadata for IDs from gate_session_setup
#   - the DENY decision from gate_pretooluse_deny
#   - the approval issue + consume from gate_approval_flow
#   - the PostToolUse event + artifact pointer from gate_posttooluse_artifact
#
# Negative checks: bundle MUST NOT contain real-secret literal tokens;
# since the script never wrote any, their presence would mean redaction
# regressed.
# ---------------------------------------------------------------------------
gate_evidence_export() {
  local gate=edge_evidence_export

  local code
  code=$(curl_request POST "${API_BASE}/api/v1/edge/sessions/${EDGE_SESSION_ID}/export" '{}')
  log_http "${gate}" POST "/api/v1/edge/sessions/${EDGE_SESSION_ID}/export" "${code}"
  assert_http_status "${gate}" "${code}" "200" "POST session export"

  # Required top-level manifest fields per OpenAPI L1228-1244 + Go
  # SessionExportBundle at core/edge/export.go:106-119.
  assert_json "${gate}" '(.manifest_version | type == "string") and (.manifest_version | length > 0)' \
    'export bundle missing manifest_version'
  assert_json "${gate}" '.tenant_id == "'"${CORDUM_TENANT_ID}"'"' 'export tenant_id mismatch'
  assert_json "${gate}" '.session.session_id == "'"${EDGE_SESSION_ID}"'"' 'export session_id mismatch'
  assert_json "${gate}" \
    '[.executions[]? | select(.execution_id == "'"${EDGE_EXECUTION_ID}"'")] | length >= 1' \
    'export executions[] missing our execution'

  # Step-8 deny event present.
  assert_json "${gate}" \
    '[.events[]? | select(.execution_id == "'"${EDGE_EXECUTION_ID}"'" and .decision == "DENY")] | length >= 1' \
    'export events[] missing the DENY decision from gate_pretooluse_deny'

  # Step-9 approval present and consumed.
  assert_json "${gate}" \
    '[.approvals[]? | select(.session_id == "'"${EDGE_SESSION_ID}"'" and .status == "approved")] | length >= 1' \
    'export approvals[] missing the approved record from gate_approval_flow'

  # Step-10 PostToolUse event present. Bypass mode hand-crafts an
  # artifact_ptr so we additionally assert it round-tripped into the
  # export bundle; hook mode relies on agentd's evaluator to decide
  # whether to attach a pointer based on the synthetic tool_response we
  # send, so we only require event presence in that mode.
  if want_bypass_hook; then
    assert_json "${gate}" \
      '[.events[]? | select(.kind == "hook.post_tool_use" and (.artifact_ptrs | length) >= 1)] | length >= 1' \
      'export events[] missing PostToolUse with artifact pointer'
    assert_json "${gate}" \
      '[.artifacts[]? | select(.session_id == "'"${EDGE_SESSION_ID}"'")] | length >= 1' \
      'export artifacts[] missing entries for our session'
  else
    assert_json "${gate}" \
      '[.events[]? | select(.kind == "hook.post_tool_use" and .execution_id == "'"${EDGE_EXECUTION_ID}"'")] | length >= 1' \
      'export events[] missing PostToolUse from gate_posttooluse_artifact'
  fi

  # Negative redaction sanity. If any of these literal tokens appear in the
  # bundle, redaction is broken (we never wrote them).
  assert_body_does_not_contain "${gate}" "OPENAI_API_KEY" \
    'export bundle leaked literal OPENAI_API_KEY'
  assert_body_does_not_contain "${gate}" "AWS_SECRET_ACCESS_KEY" \
    'export bundle leaked literal AWS_SECRET_ACCESS_KEY'
  assert_body_does_not_contain "${gate}" "BEGIN PRIVATE KEY" \
    'export bundle leaked PEM private key marker'

  pass "${gate}"
}

# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------
main() {
  parse_args "$@"

  API_BASE=$(detect_api_base)
  log "API_BASE=${API_BASE}"

  if want_start_stack; then
    if ! command -v docker >/dev/null 2>&1; then
      if is_integration_mode; then
        printf 'FAIL edge_fake_hook_e2e: CORDUM_EDGE_E2E_START_STACK=1 requires docker on PATH\n' >&2
        exit "${EXIT_PREREQ}"
      fi
      skip "CORDUM_EDGE_E2E_START_STACK=1 set but docker missing"
    fi
    log "starting local stack via 'make dev-up' (CORDUM_EDGE_E2E_START_STACK=1)"
    if ! make dev-up >&2; then
      fail edge_fake_hook_e2e "'make dev-up' failed"
    fi
  fi

  if ! probe_api_base_reachable "${API_BASE}"; then
    if is_integration_mode; then
      printf 'FAIL edge_fake_hook_e2e: %s unreachable in strict mode\n' "${API_BASE}" >&2
      exit "${EXIT_PREREQ}"
    fi
    skip "${API_BASE} unreachable; set CORDUM_INTEGRATION=1 with a running stack"
  fi

  if is_integration_mode && [[ -z "${CORDUM_API_KEY}" ]]; then
    printf 'FAIL edge_fake_hook_e2e: CORDUM_API_KEY required in strict mode\n' >&2
    exit "${EXIT_PREREQ}"
  fi

  if ! is_integration_mode; then
    skip "${API_BASE} reachable but CORDUM_INTEGRATION not set; default mode is non-destructive"
  fi

  # Strict-mode gate execution begins here.
  require_tools
  init_curl_opts
  init_tempdir
  setup_fixture_paths
  verify_demo_policy_overlay edge_session_setup

  if ! want_bypass_hook; then
    locate_or_build_binaries edge_session_setup
    pick_agentd_port
    generate_agentd_nonce edge_session_setup
    compose_agentd_url
    log "agentd loopback URL composed (host=127.0.0.1 port=${AGENTD_PORT} path=/v1/edge/hooks/claude)"
  fi

  gate_session_setup

  if ! want_bypass_hook; then
    # Start agentd AFTER gate_session_setup so the agentd inherits the
    # already-created EdgeSession via CORDUM_EDGE_SESSION_ID/EXECUTION_ID
    # in the per-call run_hook env, rather than spawning its own. This
    # keeps assertions deterministic against the IDs the script already
    # logged.
    start_agentd edge_session_setup
    wait_agentd_ready edge_session_setup
    log 'cordum-agentd ready; subsequent gates use cordum-hook -> agentd path'
  else
    log 'CORDUM_EDGE_E2E_BYPASS_HOOK=1; subsequent gates use Gateway-direct paths'
  fi

  gate_pretooluse_deny
  gate_approval_flow
  gate_approval_rejected
  gate_posttooluse_artifact
  gate_evidence_export
}

main "$@"

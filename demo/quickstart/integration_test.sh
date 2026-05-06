#!/usr/bin/env bash
# =============================================================================
# integration_test.sh — end-to-end test for the demo-quickstart pack.
#
# Pre-conditions (the script does NOT bring these up itself):
#   1. The Cordum stack is already running. Bring it up via:
#        ./tools/scripts/quickstart.sh
#      OR
#        make dev-up
#   2. CORDUM_INTEGRATION=1 must be exported. Without it the script is a
#      no-op so unit-test runs that accidentally pick it up are silent.
#   3. cordumctl is on PATH or pointed at via CORDUMCTL_BIN.
#
# Steps:
#   1. cordumctl pack install ./demo/quickstart/pack
#   2. cordumctl demo run quickstart --timeout 30
#   3. assert all three verdict strings (ALLOW, DENY, REQUIRE_APPROVAL)
#      appear in the rendered output, and that wall-clock < 30s.
#
# Exit codes:
#   0  pass.
#   1  one or more assertions failed (verdict missing, timeout breach,
#      cordumctl exit code != 0).
#   2  prerequisites missing.
# =============================================================================
set -euo pipefail

if [[ "${CORDUM_INTEGRATION:-0}" != "1" ]]; then
  echo "demo/quickstart/integration_test: CORDUM_INTEGRATION=1 required (no-op)."
  exit 0
fi

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cordumctl_bin="${CORDUMCTL_BIN:-${repo_root}/bin/cordumctl}"
if [[ ! -x "${cordumctl_bin}" ]]; then
  if command -v cordumctl >/dev/null 2>&1; then
    cordumctl_bin="$(command -v cordumctl)"
  else
    echo "ERROR: ${cordumctl_bin} not found; build it with 'make build SERVICE=cordumctl' or set CORDUMCTL_BIN." >&2
    exit 2
  fi
fi

to_cordumctl_path() {
  local path="$1"
  if command -v cygpath >/dev/null 2>&1; then
    case "${path}" in
      /*)
        cygpath -w "${path}"
        return 0
        ;;
    esac
  fi
  printf '%s' "${path}"
}

pack_dir="${repo_root}/demo/quickstart/pack"
if [[ ! -f "${pack_dir}/pack.yaml" ]]; then
  echo "ERROR: ${pack_dir}/pack.yaml not found — wrong working directory?" >&2
  exit 2
fi

gateway="${CORDUM_GATEWAY:-}"
tls_ca="${CORDUM_TLS_CA:-}"
if [[ -z "${tls_ca}" && -f "${repo_root}/certs/ca/ca.crt" ]]; then
  tls_ca="${repo_root}/certs/ca/ca.crt"
fi
if [[ -z "${gateway}" ]]; then
  if [[ -n "${tls_ca}" ]]; then
    gateway="https://127.0.0.1:8081"
  else
    gateway="http://127.0.0.1:8081"
  fi
fi

cordumctl_flags=(--gateway "${gateway}" --tenant "${CORDUM_TENANT_ID:-default}")
if [[ -n "${CORDUM_API_KEY:-}" ]]; then
  cordumctl_flags+=(--api-key "${CORDUM_API_KEY}")
fi
if [[ -n "${tls_ca}" ]]; then
  cordumctl_flags+=(--cacert "$(to_cordumctl_path "${tls_ca}")")
fi
if [[ "${CORDUM_TLS_INSECURE:-}" == "1" || "${CORDUM_TLS_INSECURE:-}" == "true" ]]; then
  cordumctl_flags+=(--insecure)
fi

log_file="$(mktemp -t demo-quickstart-XXXXXX.log)"
trap 'echo "[integration] artifacts at ${log_file}"' EXIT

echo "[integration] installing pack ..."
"${cordumctl_bin}" pack install "${cordumctl_flags[@]}" "$(to_cordumctl_path "${pack_dir}")" --upgrade

echo "[integration] running demo ..."
start_epoch="$(date +%s)"
set +e
"${cordumctl_bin}" demo run quickstart "${cordumctl_flags[@]}" --timeout 30 2>&1 | tee "${log_file}"
demo_rc=$?
set -e
elapsed=$(( $(date +%s) - start_epoch ))

echo "[integration] demo exit=${demo_rc} elapsed=${elapsed}s"

if (( demo_rc != 0 )); then
  echo "FAIL: cordumctl demo run exited ${demo_rc}" >&2
  exit 1
fi

if (( elapsed >= 30 )); then
  echo "FAIL: demo wall-clock (${elapsed}s) breached the 30s budget" >&2
  exit 1
fi

# Tighter verdict assertion: use the per-step Verdict column the CLI
# prints rather than a free-text grep. Each output line has the shape:
#   | greet            | job.demo-quickstart.greet           | ALLOW            | ... |
# so we awk the 3rd field of every row whose 1st field is one of the
# three expected step ids, then check that the three distinct verdicts
# are all present. This stops a future reason-text change that happens
# to contain the word "ALLOW" from masking a silent regression.
declare -A observed=()
while IFS= read -r line; do
  # awk the fixed-position columns; FS=|, trim whitespace.
  step="$(printf '%s\n' "${line}" | awk -F'|' '{gsub(/^ +| +$/,"",$2); print $2}')"
  verdict="$(printf '%s\n' "${line}" | awk -F'|' '{gsub(/^ +| +$/,"",$4); print $4}')"
  case "${step}" in
    greet|attempt_delete|escalate_admin)
      observed["${verdict}"]=1
      ;;
  esac
done < "${log_file}"

declare -a missing=()
for verdict in "ALLOW" "DENY" "REQUIRE_APPROVAL"; do
  if [[ -z "${observed[${verdict}]:-}" ]]; then
    missing+=("${verdict}")
  fi
done
if (( ${#missing[@]} > 0 )); then
  echo "FAIL: verdict(s) missing from Verdict column: ${missing[*]}" >&2
  exit 1
fi

# Cross-check against the golden file so drift in the expected verdicts
# fails the integration test rather than being silently accepted.
golden="demo/quickstart/expected-output.json"
if command -v jq >/dev/null 2>&1 && [[ -f "${golden}" ]]; then
  expected_verdicts="$(jq -r '.verdicts[].verdict' "${golden}" | tr -d '\r' | sort -u | paste -sd, -)"
  observed_verdicts="$(printf '%s\n' "${!observed[@]}" | sort -u | paste -sd, -)"
  if [[ "${expected_verdicts}" != "${observed_verdicts}" ]]; then
    echo "FAIL: verdict set drift — expected=${expected_verdicts}, observed=${observed_verdicts}" >&2
    exit 1
  fi
fi

echo "[integration] PASS — all 3 verdicts observed, elapsed=${elapsed}s"

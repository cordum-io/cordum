#!/usr/bin/env bash
# run.sh — synthetic-test fixture for the cordum-agentd keychain bootstrap.
#
# What it does:
#   1. Generates a synthetic (test-only) nonce and API key.
#   2. Provisions both into the local OS keychain via the install.sh
#      --secrets-from-stdin path.
#   3. Starts cordum-agentd and captures stdout / stderr.
#   4. Asserts the synthetic secret bytes do NOT appear in the captured
#      output, journalctl logs, or the service-manager unit file.
#
# Runs on macOS (Keychain) and Linux (Secret Service / libsecret). Exits
# non-zero if any leak is detected so CI can fail the keychain rail.

set -euo pipefail

if [[ "$(uname -s)" != "Darwin" && "$(uname -s)" != "Linux" ]]; then
    echo "synthetic-test/run.sh: skipping on $(uname -s) (POSIX only)" >&2
    exit 0
fi

REPO_ROOT="$(cd "$(dirname "$0")/../../../.." && pwd)"
INSTALL_SH="${REPO_ROOT}/tools/scripts/agentd-install/install.sh"
AGENTD_BIN="${CORDUM_AGENTD_BIN:-${REPO_ROOT}/bin/cordum-agentd}"
LOG_DIR="$(mktemp -d -t cordum-agentd-synth-XXXXXX)"

cleanup() {
    set +e
    if [[ -n "${AGENTD_PID:-}" ]]; then
        kill "$AGENTD_PID" 2>/dev/null
        wait "$AGENTD_PID" 2>/dev/null
    fi
    # Best-effort teardown of synthetic secrets.
    if [[ "$(uname -s)" == "Darwin" ]]; then
        security delete-generic-password -a cordum_agentd_nonce -s cordum-agentd >/dev/null 2>&1 || true
        security delete-generic-password -a cordum_api_key -s cordum-agentd      >/dev/null 2>&1 || true
    elif [[ "$(uname -s)" == "Linux" ]]; then
        secret-tool clear service cordum-agentd username cordum_agentd_nonce >/dev/null 2>&1 || true
        secret-tool clear service cordum-agentd username cordum_api_key      >/dev/null 2>&1 || true
    fi
    rm -rf "$LOG_DIR"
}
trap cleanup EXIT

# Synthetic, prefixed so leak detection is unambiguous.
SYNTH_NONCE="$(openssl rand -base64 48 | tr -d '\n')"
SYNTH_API_KEY="SYNTH-API-KEY-$(openssl rand -hex 16)"

echo "synthetic-test: provisioning into local keychain (test-only values)" >&2
# Defense-in-depth: invoke install.sh via `bash "$INSTALL_SH"` rather than
# direct-exec `"$INSTALL_SH"`. Matches binaries-pr-validation.yml lines
# 71/93 + tools/scripts/install_test.sh:52 post-EDGE-151-reopen-#3 pattern
# (commit a3616c9f). A `git update-index --chmod=+x` flipped install.sh to
# 100755 in the same commit as this change, but the bash-wrapper survives
# any future Windows commit that re-strips the executable bit — the bug
# trips silently otherwise the moment this script gets wired into a Linux
# CI workflow.
printf '%s\n%s\n' "$SYNTH_NONCE" "$SYNTH_API_KEY" \
    | bash "$INSTALL_SH" --secrets-from-stdin --rotate \
    > "${LOG_DIR}/install.log" 2>&1 || {
        echo "synthetic-test: install.sh failed; log:" >&2
        cat "${LOG_DIR}/install.log" >&2
        exit 1
    }

[[ -x "$AGENTD_BIN" ]] || {
    echo "synthetic-test: cordum-agentd binary missing at $AGENTD_BIN — run \`make build SERVICE=cordum-agentd\` first" >&2
    exit 1
}

echo "synthetic-test: starting cordum-agentd in strict mode" >&2
CORDUM_AGENTD_STRICT=true CORDUM_GATEWAY="http://127.0.0.1:8081" \
    CORDUM_TENANT_ID="synthetic-test" \
    "$AGENTD_BIN" >"${LOG_DIR}/stdout.log" 2>"${LOG_DIR}/stderr.log" &
AGENTD_PID=$!
sleep 2

# Even if agentd exits early (gateway unreachable etc), the bootstrap
# path has run — that is what we are asserting against, not steady-state.
if grep -F -q 'BOOTSTRAP-FAIL:' "${LOG_DIR}/stderr.log"; then
    echo "synthetic-test: bootstrap failed after installer provisioning; stderr:" >&2
    cat "${LOG_DIR}/stderr.log" >&2
    exit 1
fi

assert_no_leak() {
    local label="$1" file="$2" needle="$3"
    if grep -F -q "$needle" "$file"; then
        echo "synthetic-test: LEAK in $label ($file): synthetic value found verbatim" >&2
        exit 1
    fi
    echo "synthetic-test: OK — no leak in $label" >&2
}

assert_no_leak "stdout"        "${LOG_DIR}/stdout.log" "$SYNTH_NONCE"
assert_no_leak "stderr"        "${LOG_DIR}/stderr.log" "$SYNTH_NONCE"
assert_no_leak "stdout"        "${LOG_DIR}/stdout.log" "$SYNTH_API_KEY"
assert_no_leak "stderr"        "${LOG_DIR}/stderr.log" "$SYNTH_API_KEY"
assert_no_leak "install.log"   "${LOG_DIR}/install.log" "$SYNTH_NONCE"
assert_no_leak "install.log"   "${LOG_DIR}/install.log" "$SYNTH_API_KEY"

# journalctl / Console.app: best-effort. journalctl --user may not be
# available in every CI shape; skip silently rather than fail-warn.
if command -v journalctl >/dev/null 2>&1; then
    journalctl --user --since '1 min ago' --output cat 2>/dev/null \
        > "${LOG_DIR}/journal.log" || true
    if [[ -s "${LOG_DIR}/journal.log" ]]; then
        assert_no_leak "journalctl" "${LOG_DIR}/journal.log" "$SYNTH_NONCE"
        assert_no_leak "journalctl" "${LOG_DIR}/journal.log" "$SYNTH_API_KEY"
    fi
fi

# Service-manager units shipped in the repo must NEVER contain the
# synthetic values. This re-checks the committed templates after the
# install path may have rendered them.
for unit_path in \
    "${REPO_ROOT}/tools/scripts/launchd/com.cordum.agentd.plist" \
    "${REPO_ROOT}/tools/scripts/systemd/cordum-agentd.service" \
    "${REPO_ROOT}/tools/scripts/windows/cordum-agentd-service.xml" \
    "${HOME}/Library/LaunchAgents/com.cordum.agentd.plist" \
    "${HOME}/.config/systemd/user/cordum-agentd.service"; do
    if [[ -f "$unit_path" ]]; then
        assert_no_leak "$unit_path" "$unit_path" "$SYNTH_NONCE"
        assert_no_leak "$unit_path" "$unit_path" "$SYNTH_API_KEY"
    fi
done

echo "synthetic-test: PASS — no synthetic secret bytes detected in any captured surface" >&2

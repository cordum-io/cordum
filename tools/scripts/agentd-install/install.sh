#!/usr/bin/env bash
# install.sh — provision cordum-agentd as a user-mode service on macOS or
# Linux, sourcing the boot-time secrets from the OS-native keychain rather
# than the developer-shell environment.
#
# Usage:
#   tools/scripts/agentd-install/install.sh [--secrets-from-stdin]
#
# Behavior:
#   - Detects the host OS and copies the matching service-manager template
#     from tools/scripts/{launchd,systemd}/ into the user's service path.
#   - Provisions cordum_agentd_nonce + cordum_api_key in the OS-native
#     credential store. Values are read from a sealed prompt (read -s) and
#     passed only to the OS credential CLI; they never appear in shell
#     history or in service-manager configuration.
#   - Enables + starts the service.
#
# What this script does NOT do:
#   - Build cordum-agentd. The binary must already exist at
#     /usr/local/bin/cordum-agentd (POSIX default) or be on $PATH.
#   - Install package dependencies. On Linux, libsecret-tools must be
#     installed beforehand (apt: libsecret-tools; rpm: libsecret).
#   - Override existing secrets without confirmation. Re-run with
#     --rotate to update an existing entry.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/../../.." && pwd)"
LAUNCHD_PLIST="${REPO_ROOT}/tools/scripts/launchd/com.cordum.agentd.plist"
SYSTEMD_UNIT="${REPO_ROOT}/tools/scripts/systemd/cordum-agentd.service"

ROTATE=0
SECRETS_FROM_STDIN=0
for arg in "$@"; do
    case "$arg" in
        --rotate) ROTATE=1 ;;
        --secrets-from-stdin) SECRETS_FROM_STDIN=1 ;;
        --help|-h)
            sed -n '1,30p' "$0"
            exit 0
            ;;
        *)
            echo "install.sh: unknown argument: $arg" >&2
            exit 2
            ;;
    esac
done

die() {
    echo "install.sh: $*" >&2
    exit 1
}

read_secret() {
    local label="$1" value
    if [[ $SECRETS_FROM_STDIN -eq 1 ]]; then
        IFS= read -r value || die "stdin read failed for $label"
    else
        printf '%s: ' "$label" >&2
        stty -echo
        IFS= read -r value
        stty echo
        printf '\n' >&2
    fi
    if [[ -z "$value" ]]; then
        die "$label is empty — refusing to provision"
    fi
    printf '%s' "$value"
}

provision_macos() {
    local nonce api_key
    if [[ $ROTATE -eq 1 ]]; then
        security delete-generic-password -a cordum_agentd_nonce -s cordum-agentd >/dev/null 2>&1 || true
        security delete-generic-password -a cordum_api_key -s cordum-agentd >/dev/null 2>&1 || true
    fi
    nonce="$(read_secret 'cordum_agentd_nonce (base64, >=32 bytes)')"
    api_key="$(read_secret 'cordum_api_key')"
    # -T scopes ACL to the cordum-agentd binary so Console.app does not
    # prompt on first read. -U updates if already present.
    security add-generic-password -a cordum_agentd_nonce -s cordum-agentd \
        -w "$nonce" -T /usr/local/bin/cordum-agentd -U >/dev/null
    security add-generic-password -a cordum_api_key -s cordum-agentd \
        -w "$api_key" -T /usr/local/bin/cordum-agentd -U >/dev/null
    # Defensive: shred locals before they leave scope.
    nonce="$(printf '%*s' "${#nonce}" '' | tr ' ' '*')"
    api_key="$(printf '%*s' "${#api_key}" '' | tr ' ' '*')"
    unset nonce api_key
    echo "macOS Keychain: provisioned cordum_agentd_nonce + cordum_api_key" >&2

    local target="$HOME/Library/LaunchAgents/com.cordum.agentd.plist"
    mkdir -p "$(dirname "$target")"
    sed "s|REPLACE_WITH_USERNAME|$USER|g" "$LAUNCHD_PLIST" > "$target"
    chmod 600 "$target"
    mkdir -p "$HOME/Library/Logs/Cordum"
    launchctl bootstrap "gui/$(id -u)" "$target" >/dev/null 2>&1 || true
    launchctl kickstart -k "gui/$(id -u)/com.cordum.agentd" >/dev/null
    echo "launchd: loaded com.cordum.agentd" >&2
}

provision_linux() {
    command -v secret-tool >/dev/null 2>&1 || die "secret-tool missing — install libsecret-tools (apt) or libsecret (rpm) first"
    local nonce api_key
    if [[ $ROTATE -eq 1 ]]; then
        secret-tool clear service cordum-agentd username cordum_agentd_nonce >/dev/null 2>&1 || true
        secret-tool clear service cordum-agentd username cordum_api_key >/dev/null 2>&1 || true
    fi
    nonce="$(read_secret 'cordum_agentd_nonce (base64, >=32 bytes)')"
    api_key="$(read_secret 'cordum_api_key')"
    # secret-tool reads the secret value from stdin, never argv — pipe it.
    printf '%s' "$nonce" | secret-tool store --label='cordum-agentd nonce' \
        service cordum-agentd username cordum_agentd_nonce
    printf '%s' "$api_key" | secret-tool store --label='cordum-agentd API key' \
        service cordum-agentd username cordum_api_key
    nonce="$(printf '%*s' "${#nonce}" '' | tr ' ' '*')"
    api_key="$(printf '%*s' "${#api_key}" '' | tr ' ' '*')"
    unset nonce api_key
    echo "libsecret: provisioned cordum_agentd_nonce + cordum_api_key" >&2

    local unit_dir="$HOME/.config/systemd/user"
    mkdir -p "$unit_dir"
    install -m 644 "$SYSTEMD_UNIT" "$unit_dir/cordum-agentd.service"
    systemctl --user daemon-reload
    systemctl --user enable --now cordum-agentd.service
    echo "systemd-user: cordum-agentd.service enabled + started" >&2
}

main() {
    case "$(uname -s)" in
        Darwin) provision_macos ;;
        Linux)  provision_linux ;;
        *)
            die "unsupported OS: $(uname -s) — see tools/scripts/windows/cordum-agentd-service.xml for Windows"
            ;;
    esac
}

main "$@"

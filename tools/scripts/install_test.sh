#!/usr/bin/env bash
# install_test.sh — synthetic-tampered + unsigned scenarios for install.sh,
# plus EDGE-151-DOWNGRADE scenarios (downgrade-refused / legit-upgrade /
# operator-override-rollback).
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && pwd)"
REPO_ROOT="$(cd -- "$SCRIPT_DIR/../.." >/dev/null 2>&1 && pwd)"
INSTALL_SH="$SCRIPT_DIR/install.sh"
TEST_KEYS_DIR="$REPO_ROOT/tools/test-keys"

if ! command -v gpg >/dev/null 2>&1; then
  echo "install_test.sh: gpg required for synthetic-signing fixtures" >&2
  exit 2
fi
if [ ! -f "$TEST_KEYS_DIR/TEST-ONLY-release.priv.asc" ]; then
  echo "install_test.sh: missing $TEST_KEYS_DIR/TEST-ONLY-release.priv.asc — run tools/test-keys/gen.sh" >&2
  exit 2
fi

WORK="$(mktemp -d -t cordum-install-test-XXXXXX)"
cleanup() { rm -rf "$WORK"; }
trap cleanup EXIT

GPG_HOME="$WORK/gpg"
mkdir -p "$GPG_HOME"
chmod 700 "$GPG_HOME" 2>/dev/null || true
gpg --homedir "$GPG_HOME" --batch --quiet \
  --import "$TEST_KEYS_DIR/TEST-ONLY-release.priv.asc"

# Synthetic release dir with one fake binary + signed SHA256SUMS manifest.
# If $2 is set it is embedded as a `# version: vN.N.N` comment line at the
# top of the manifest (covered by the GPG signature), as the release-local
# pipeline produces for EDGE-151-DOWNGRADE floor enforcement.
build_release_dir() {
  local dest="$1"
  local version="${2:-}"
  mkdir -p "$dest"
  printf '#!/bin/sh\nexit 0\n' >"$dest/cordum-hook"
  chmod +x "$dest/cordum-hook"
  # Use --text so MSYS/Windows GnuPG produces the same "<hash>  <path>"
  # two-space form that GNU coreutils on Linux emits by default.
  ( cd "$dest" && sha256sum --text cordum-hook ) >"$dest/SHA256SUMS.body"
  if [ -n "$version" ]; then
    {
      printf '# version: %s\n' "$version"
      cat "$dest/SHA256SUMS.body"
    } >"$dest/SHA256SUMS"
  else
    mv -f "$dest/SHA256SUMS.body" "$dest/SHA256SUMS"
  fi
  rm -f "$dest/SHA256SUMS.body"
  gpg --homedir "$GPG_HOME" --batch --yes --quiet --detach-sign --armor \
    --output "$dest/SHA256SUMS.asc" "$dest/SHA256SUMS"
}

# Pre-seed a binary-version-floor.json file at the given path. The minimal
# JSON shape mirrors what AdvanceFloor writes on a successful upgrade.
seed_floor() {
  local path="$1" version="$2"
  mkdir -p "$(dirname "$path")"
  printf '{"version":"%s","advanced_at":"2026-01-01T00:00:00Z","sig_scheme":"dev","fingerprint":"","operator":"seed"}\n' \
    "$version" >"$path"
}

expect_verify_fail() {
  local label="$1" needle="$2" release_dir="$3"
  local out rc
  set +e
  out=$("$INSTALL_SH" --dev-allow-unsigned --release-dir "$release_dir" 2>&1)
  rc=$?
  set -e
  if [ "$rc" -eq 0 ]; then
    echo "FAIL [$label]: install.sh exit=0; expected nonzero" >&2
    printf '%s\n' "$out" >&2
    return 1
  fi
  if ! printf '%s\n' "$out" | grep -q "$needle"; then
    echo "FAIL [$label]: stderr missing '$needle'" >&2
    printf '%s\n' "$out" >&2
    return 1
  fi
  echo "PASS [$label]: install.sh refused with '$needle'"
}

# expect_install_ok runs install.sh with the given args and expects exit-0
# AND a specific stderr needle (e.g. an audit event JSON-line). It is the
# happy-path counterpart to expect_verify_fail.
expect_install_ok() {
  local label="$1" needle="$2" release_dir="$3"
  shift 3
  local out rc
  set +e
  out=$("$INSTALL_SH" --dev-allow-unsigned --release-dir "$release_dir" "$@" 2>&1)
  rc=$?
  set -e
  if [ "$rc" -ne 0 ]; then
    echo "FAIL [$label]: install.sh exit=$rc; expected 0" >&2
    printf '%s\n' "$out" >&2
    return 1
  fi
  if ! printf '%s\n' "$out" | grep -q "$needle"; then
    echo "FAIL [$label]: stderr missing '$needle'" >&2
    printf '%s\n' "$out" >&2
    return 1
  fi
  echo "PASS [$label]: install.sh installed and emitted '$needle'"
}

failures=0

# Scenario 1 — tampered binary: hash recomputed inside install.sh must NOT
# match the manifest entry, so install must abort.
build_release_dir "$WORK/release-tampered"
echo 'tamper' >>"$WORK/release-tampered/cordum-hook"
if ! expect_verify_fail "tampered-binary" \
    "BINARY-VERIFY-FAIL: hash mismatch cordum-hook" \
    "$WORK/release-tampered"; then
  failures=$((failures+1))
fi

# Scenario 2 — unsigned manifest: SHA256SUMS.asc removed; install must abort
# even though SHA256SUMS itself is intact.
build_release_dir "$WORK/release-unsigned"
rm -f "$WORK/release-unsigned/SHA256SUMS.asc"
if ! expect_verify_fail "unsigned-manifest" \
    "BINARY-VERIFY-FAIL: unsigned manifest" \
    "$WORK/release-unsigned"; then
  failures=$((failures+1))
fi

# ----------------------------------------------------------------------------
# EDGE-151-DOWNGRADE scenarios — version-floor enforcement.
# ----------------------------------------------------------------------------

# Scenario 3 — downgrade-refused: floor is v1.2.0; release manifest embeds
# v1.0.0; install must refuse with BINARY-VERIFY-FAIL: downgrade attempt.
FLOOR_DIR="$WORK/floor-downgrade"
FLOOR_FILE="$FLOOR_DIR/binary-version-floor.json"
seed_floor "$FLOOR_FILE" "v1.2.0"
build_release_dir "$WORK/release-downgrade" "v1.0.0"
set +e
out=$(CORDUM_BINARY_FLOOR_FILE="$FLOOR_FILE" "$INSTALL_SH" \
  --dev-allow-unsigned --release-dir "$WORK/release-downgrade" 2>&1)
rc=$?
set -e
if [ "$rc" -eq 0 ]; then
  echo "FAIL [downgrade-refused]: install.sh exit=0; expected nonzero" >&2
  printf '%s\n' "$out" >&2
  failures=$((failures+1))
elif ! printf '%s\n' "$out" | grep -q "BINARY-VERIFY-FAIL: downgrade attempt v1.0.0 < v1.2.0"; then
  echo "FAIL [downgrade-refused]: stderr missing downgrade-attempt message" >&2
  printf '%s\n' "$out" >&2
  failures=$((failures+1))
else
  echo "PASS [downgrade-refused]: install.sh refused v1.0.0 < v1.2.0"
fi

# Scenario 4 — legit-upgrade: floor is v1.2.0; release embeds v1.3.0;
# install must succeed AND advance the floor file AND emit
# binary-floor-advance JSON-line.
FLOOR_FILE="$WORK/floor-upgrade/binary-version-floor.json"
seed_floor "$FLOOR_FILE" "v1.2.0"
build_release_dir "$WORK/release-upgrade" "v1.3.0"
INSTALL_TO="$WORK/install-upgrade"
set +e
out=$(CORDUM_BINARY_FLOOR_FILE="$FLOOR_FILE" "$INSTALL_SH" \
  --dev-allow-unsigned \
  --release-dir "$WORK/release-upgrade" \
  --install-dir "$INSTALL_TO" 2>&1)
rc=$?
set -e
if [ "$rc" -ne 0 ]; then
  echo "FAIL [legit-upgrade]: install.sh exit=$rc; expected 0" >&2
  printf '%s\n' "$out" >&2
  failures=$((failures+1))
elif ! printf '%s\n' "$out" | grep -q '"event":"binary-floor-advance"'; then
  echo "FAIL [legit-upgrade]: stderr missing binary-floor-advance audit event" >&2
  printf '%s\n' "$out" >&2
  failures=$((failures+1))
elif ! grep -q '"version":"v1.3.0"' "$FLOOR_FILE"; then
  echo "FAIL [legit-upgrade]: floor file not advanced to v1.3.0" >&2
  cat "$FLOOR_FILE" >&2 || true
  failures=$((failures+1))
else
  echo "PASS [legit-upgrade]: install.sh advanced floor v1.2.0 -> v1.3.0"
fi

# Scenario 5 — operator-override-rollback: floor is v1.2.0; release embeds
# v1.1.0; install with --rollback-operator-override --rollback-reason
# 'CVE-rollback' must succeed, write the floor DOWN to v1.1.0, and emit
# binary-floor-rollback JSON-line.
FLOOR_FILE="$WORK/floor-rollback/binary-version-floor.json"
seed_floor "$FLOOR_FILE" "v1.2.0"
build_release_dir "$WORK/release-rollback" "v1.1.0"
INSTALL_TO="$WORK/install-rollback"
set +e
out=$(CORDUM_BINARY_FLOOR_FILE="$FLOOR_FILE" "$INSTALL_SH" \
  --dev-allow-unsigned \
  --release-dir "$WORK/release-rollback" \
  --install-dir "$INSTALL_TO" \
  --rollback-operator-override \
  --rollback-reason 'CVE-rollback' 2>&1)
rc=$?
set -e
if [ "$rc" -ne 0 ]; then
  echo "FAIL [operator-rollback]: install.sh exit=$rc; expected 0" >&2
  printf '%s\n' "$out" >&2
  failures=$((failures+1))
elif ! printf '%s\n' "$out" | grep -q '"event":"binary-floor-rollback"'; then
  echo "FAIL [operator-rollback]: stderr missing binary-floor-rollback audit event" >&2
  printf '%s\n' "$out" >&2
  failures=$((failures+1))
elif ! printf '%s\n' "$out" | grep -q '"reason":"CVE-rollback"'; then
  echo "FAIL [operator-rollback]: audit event missing reason=CVE-rollback" >&2
  printf '%s\n' "$out" >&2
  failures=$((failures+1))
elif ! grep -q '"version":"v1.1.0"' "$FLOOR_FILE"; then
  echo "FAIL [operator-rollback]: floor file not rolled back to v1.1.0" >&2
  cat "$FLOOR_FILE" >&2 || true
  failures=$((failures+1))
else
  echo "PASS [operator-rollback]: install.sh rolled back floor v1.2.0 -> v1.1.0"
fi

# Scenario 6a — garbage version in manifest must be rejected even when no
# floor is set (defense-in-depth — silent acceptance would let a future
# malformed release ride the gate).
build_release_dir "$WORK/release-garbage-ver" "v1.0-garbage"
set +e
out=$("$INSTALL_SH" --dev-allow-unsigned \
  --release-dir "$WORK/release-garbage-ver" 2>&1)
rc=$?
set -e
if [ "$rc" -eq 0 ]; then
  echo "FAIL [garbage-version]: install.sh exit=0; expected nonzero" >&2
  printf '%s\n' "$out" >&2
  failures=$((failures+1))
elif ! printf '%s\n' "$out" | grep -q 'invalid manifest version'; then
  echo "FAIL [garbage-version]: stderr missing 'invalid manifest version'" >&2
  printf '%s\n' "$out" >&2
  failures=$((failures+1))
else
  echo "PASS [garbage-version]: install.sh refused malformed version"
fi

# Scenario 6 — rollback override without reason: must refuse.
FLOOR_FILE="$WORK/floor-rollback-noreason/binary-version-floor.json"
seed_floor "$FLOOR_FILE" "v1.2.0"
build_release_dir "$WORK/release-rollback-noreason" "v1.1.0"
set +e
out=$(CORDUM_BINARY_FLOOR_FILE="$FLOOR_FILE" "$INSTALL_SH" \
  --dev-allow-unsigned \
  --release-dir "$WORK/release-rollback-noreason" \
  --rollback-operator-override 2>&1)
rc=$?
set -e
if [ "$rc" -eq 0 ]; then
  echo "FAIL [rollback-no-reason]: install.sh exit=0; expected nonzero" >&2
  printf '%s\n' "$out" >&2
  failures=$((failures+1))
elif ! printf '%s\n' "$out" | grep -q 'rollback-reason'; then
  echo "FAIL [rollback-no-reason]: stderr missing rollback-reason requirement" >&2
  printf '%s\n' "$out" >&2
  failures=$((failures+1))
else
  echo "PASS [rollback-no-reason]: install.sh refused rollback without reason"
fi

# The stand-in tests argv and signed-byte binding, not Sigstore cryptography.
DEFAULT_IDENTITY='^https://github\.com/cordum-io/cordum/\.github/workflows/release\.yml@refs/tags/v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$'
mkdir -p "$WORK/fakebin"
cat >"$WORK/fakebin/cosign" <<'FAKE'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$@" >"$FAKE_COSIGN_ARGS"
[ "$#" -eq 8 ] && [ "$1" = verify-blob ] && [ "$2" = --bundle ] \
  && [ "$4" = --certificate-identity-regexp ] \
  && [ "$6" = --certificate-oidc-issuer ] || exit 64
[ "$5" = "$FAKE_COSIGN_EXPECT_IDENTITY" ] \
  && [ "$7" = https://token.actions.githubusercontent.com ] || exit 1
[ "$(cat "$3")" = "$(sha256sum --text "$8" | awk '{print $1}')" ] || exit 1
FAKE
chmod +x "$WORK/fakebin/cosign"

build_keyless_release_dir() {
  local dest="$1" version="${2:-}"
  mkdir -p "$dest"
  printf '#!/bin/sh\nexit 0\n' >"$dest/cordum-hook"
  {
    if [ -n "$version" ]; then printf '# version: %s\n' "$version"; fi
    (cd "$dest" && sha256sum --text cordum-hook)
  } >"$dest/SHA256SUMS"
  sha256sum --text "$dest/SHA256SUMS" | awk '{print $1}' >"$dest/SHA256SUMS.sigstore.json"
}

assert_keyless_result() {
  if [ -n "$failure" ]; then
    [ "$rc" -ne 0 ] && grep -qF "BINARY-VERIFY-FAIL: $failure" "$log" || return 1
    [ ! -e "$floor" ] || return 1
    [ -z "$(find "$dest" -type f 2>/dev/null || true)" ] || return 1
    return 0
  fi
  [ "$rc" -eq 0 ] && [ -f "$dest/cordum-hook" ] || return 1
  [ "$(sha256sum --text "$dest/cordum-hook" | awk '{print $1}')" = "$hash" ] || return 1
  grep '"event":"binary-verify-ok"' "$log" | grep -qF "\"sig_scheme\":\"$scheme\"" || return 1
  grep -qF "\"sig_scheme\":\"$scheme\"" "$floor" || return 1
  if [ "$scheme" = unverified ]; then
    grep '"event":"binary-signature-unverified"' "$log" | grep -qF -- '--insecure-allow-missing-cosign' || return 1
    grep -qF WARNING "$log" && [ ! -e "$args_log" ] || return 1
  else
    printf '%s\n' verify-blob --bundle "$release/SHA256SUMS.sigstore.json" \
      --certificate-identity-regexp "$identity" --certificate-oidc-issuer \
      https://token.actions.githubusercontent.com "$release/SHA256SUMS" >"$base/expected.args"
    cmp -s "$base/expected.args" "$args_log" || return 1
  fi
  if [ "$label" = keyless-identity-override ]; then
    grep -qF '"event":"binary-signer-identity-override"' "$log" || return 1
  fi
}

run_keyless_scenario() {
  local label="$1" base="$WORK/$1" failure="" scheme=cosign-keyless
  local release="$base/release" dest="$base/install" floor="$base/floor.json"
  local log="$base/output" args_log="$base/cosign.args" rc hash
  local verifier="$WORK/fakebin/cosign" identity="$DEFAULT_IDENTITY" override_identity=""
  local flags=()
  build_keyless_release_dir "$release" v1.3.0
  hash=$(sha256sum --text "$release/cordum-hook" | awk '{print $1}')
  case "$label" in
    keyless-one-byte|keyless-missing-override-tampered)
      printf X | dd of="$release/cordum-hook" bs=1 count=1 conv=notrunc 2>/dev/null
      failure='hash mismatch cordum-hook' ;;
    keyless-manifest-tampered)
      printf '# changed\n' >>"$release/SHA256SUMS"; failure='cosign signature invalid' ;;
    keyless-cosign-missing) failure='cosign required' ;;
    keyless-identity-override) identity='^https://example\.invalid/release$'; override_identity="$identity" ;;
    keyless-precedence)
      gpg --homedir "$GPG_HOME" --batch --yes --quiet --detach-sign --armor \
        --output "$release/SHA256SUMS.asc" "$release/SHA256SUMS" ;;
  esac
  case "$label" in
    keyless-*missing*) verifier="$base/not-installed-cosign" ;;
  esac
  case "$label" in
    keyless-missing-override*) flags=(--insecure-allow-missing-cosign); scheme=unverified ;;
  esac
  if FAKE_COSIGN_ARGS="$args_log" FAKE_COSIGN_EXPECT_IDENTITY="$identity" \
      CORDUM_COSIGN_BIN="$verifier" CORDUM_COSIGN_IDENTITY_REGEXP="$override_identity" \
      CORDUM_BINARY_FLOOR_FILE="$floor" "$INSTALL_SH" --release-dir "$release" \
      --install-dir "$dest" "${flags[@]}" >"$log" 2>&1; then rc=0; else rc=$?; fi
  if assert_keyless_result; then
    echo "PASS [$label]: signature, checksum and activation effects checked"
  else
    echo "FAIL [$label]: exit=$rc; message, argv or activation assertion failed" >&2
    cat "$log" >&2
    return 1
  fi
}

for scenario in valid one-byte manifest-tampered cosign-missing missing-override \
    missing-override-tampered identity-override precedence; do
  if ! run_keyless_scenario "keyless-$scenario"; then failures=$((failures+1)); fi
done

if [ "$failures" -ne 0 ]; then
  echo "install_test.sh: $failures scenario(s) failed" >&2
  exit 1
fi
echo "install_test.sh: all synthetic verification scenarios passed"

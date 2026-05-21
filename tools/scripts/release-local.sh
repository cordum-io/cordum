#!/usr/bin/env bash
# release-local.sh — produce a local dev release in bin/release-local/
# (cordum-hook + cordum-agentd + cordum-claude for the host platform) with a
# SHA256SUMS manifest detached-signed by the TEST-ONLY key under
# tools/test-keys/. Used by `make release-local` and by install_test.sh.
#
# Output binaries are pinned at build time to the TEST-ONLY fingerprint via
#   -ldflags '-X github.com/cordum/cordum/tools/sign.PinnedReleaseFingerprint=<fpr>'
# so the install.sh pre-activation gate (step-5) refuses to activate them in
# production mode and only accepts them when `--dev-allow-unsigned` is set.
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && pwd)"
REPO_ROOT="$(cd -- "$SCRIPT_DIR/../.." >/dev/null 2>&1 && pwd)"
cd -- "$REPO_ROOT"

if ! command -v gpg >/dev/null 2>&1; then
  echo "release-local.sh: gpg required (GnuPG 2.x)" >&2
  exit 2
fi
if ! command -v go >/dev/null 2>&1; then
  echo "release-local.sh: go required" >&2
  exit 2
fi

TEST_KEYS_DIR="$REPO_ROOT/tools/test-keys"
PRIV="$TEST_KEYS_DIR/TEST-ONLY-release.priv.asc"
if [ ! -f "$PRIV" ]; then
  echo "release-local.sh: $PRIV missing; run tools/test-keys/gen.sh first" >&2
  exit 2
fi

OUT_DIR="$REPO_ROOT/bin/release-local"
rm -rf "$OUT_DIR"
mkdir -p "$OUT_DIR"

WORK="$(mktemp -d -t cordum-release-local-XXXXXX)"
cleanup() { rm -rf "$WORK"; }
trap cleanup EXIT
GPG_HOME="$WORK/gpg"
mkdir -p "$GPG_HOME"
chmod 700 "$GPG_HOME" 2>/dev/null || true
gpg --homedir "$GPG_HOME" --batch --quiet --import "$PRIV"

FPR=$(gpg --homedir "$GPG_HOME" --with-colons --list-keys \
  | awk -F: '$1=="fpr" {print $10; exit}')
if [ -z "$FPR" ]; then
  echo "release-local.sh: failed to extract TEST-ONLY fingerprint" >&2
  exit 1
fi

ext=""
case "$(go env GOOS)" in
  windows) ext=".exe" ;;
esac

for target in cordum-hook cordum-agentd cordum-claude; do
  go build \
    -trimpath \
    -ldflags "-s -w -X github.com/cordum/cordum/tools/sign.PinnedReleaseFingerprint=$FPR" \
    -o "$OUT_DIR/${target}${ext}" \
    "./cmd/${target}"
done

( cd "$OUT_DIR" && sha256sum --text cordum-* ) >"$OUT_DIR/SHA256SUMS"

# EDGE-151-DOWNGRADE: embed the source semver into the manifest as a
# `# version: vN.N.N` comment line so install.{sh,ps1} can enforce a
# monotonic version floor. Dev releases default to v0.0.0-dev so they
# never block a subsequent production install of any released tag.
RELEASE_LOCAL_VERSION="${RELEASE_LOCAL_VERSION:-v0.0.0-dev}"
tmp_manifest="$(mktemp -t cordum-release-local-manifest-XXXXXX)"
{
  printf '# version: %s\n' "$RELEASE_LOCAL_VERSION"
  cat "$OUT_DIR/SHA256SUMS"
} >"$tmp_manifest"
mv -f "$tmp_manifest" "$OUT_DIR/SHA256SUMS"

gpg --homedir "$GPG_HOME" --batch --yes --quiet --detach-sign --armor \
  --local-user "$FPR" \
  --output "$OUT_DIR/SHA256SUMS.asc" \
  "$OUT_DIR/SHA256SUMS"

echo "release-local: built $OUT_DIR/{cordum-hook${ext},cordum-agentd${ext},cordum-claude${ext}}"
echo "release-local: signed manifest with TEST-ONLY fingerprint $FPR"
echo "release-local: embedded version $RELEASE_LOCAL_VERSION (override via RELEASE_LOCAL_VERSION env)"

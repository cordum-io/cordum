#!/usr/bin/env bash
# =============================================================================
# verify_published_images.sh
#
# End-to-end verification that the images published for the version in
# cordum/VERSION are (a) present on ghcr.io, (b) signed by the expected
# release workflow identity via cosign, and (c) multi-arch (linux/amd64
# AND linux/arm64) — except the dashboard, which is allowed to ship
# amd64-only during the arm64 migration period.
#
# Guarded by CORDUM_VERIFY_IMAGES=1 because it pulls the full image set
# (~250 MB compressed) and makes live registry calls.
#
# Exit codes:
#   0  — every image present, signed, and multi-arch.
#   1  — one or more verifications failed. stderr lists the offenders.
#   2  — script was invoked in an unsupported state (missing dep,
#        VERSION unreadable, etc.).
#
# Hooks into the Makefile as `make verify-images`.
# =============================================================================
set -euo pipefail

if [[ "${CORDUM_VERIFY_IMAGES:-0}" != "1" ]]; then
  echo "verify_published_images: CORDUM_VERIFY_IMAGES=1 required to run (no-op)."
  exit 0
fi

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${repo_root}"

# Version resolution order:
#   1. $CORDUM_VERSION env var (explicit override).
#   2. cordum/VERSION file if present (release-tag tree).
#   3. git describe --tags to pick up the latest tagged version.
# Exits 2 only if all three fail.
if [[ -n "${CORDUM_VERSION:-}" ]]; then
  TAG="${CORDUM_VERSION}"
elif [[ -f "${repo_root}/VERSION" ]]; then
  TAG="$(tr -d '[:space:]' < "${repo_root}/VERSION")"
elif git -C "${repo_root}" describe --tags --abbrev=0 >/dev/null 2>&1; then
  TAG="$(git -C "${repo_root}" describe --tags --abbrev=0 | sed 's/^v//')"
else
  echo "ERROR: cannot determine version — set CORDUM_VERSION, add a VERSION file, or tag the repo" >&2
  exit 2
fi
if [[ -z "${TAG}" ]]; then
  echo "ERROR: resolved version is empty" >&2
  exit 2
fi

for bin in docker cosign jq; do
  if ! command -v "${bin}" >/dev/null 2>&1; then
    echo "ERROR: required binary '${bin}' not found in PATH" >&2
    exit 2
  fi
done

# Packages: full list + dashboard marker for arm64 exception.
PACKAGES=(
  api-gateway
  scheduler
  safety-kernel
  workflow-engine
  context-engine
  mcp
  cordumctl
  dashboard
)

COSIGN_ISSUER="https://token.actions.githubusercontent.com"
COSIGN_IDENTITY_REGEX='https://github\.com/cordum-io/cordum/\.github/workflows/docker\.yml@refs/tags/v.*'

declare -a failures=()

record_fail() {
  local msg="$1"
  failures+=("${msg}")
  echo "FAIL ${msg}" >&2
}

for pkg in "${PACKAGES[@]}"; do
  image="ghcr.io/cordum-io/cordum/${pkg}:${TAG}"
  echo "--- ${image}"

  # (b) docker pull — proves the tag exists and the registry is reachable.
  if ! docker pull --quiet "${image}" >/dev/null; then
    record_fail "${pkg}: docker pull failed"
    continue
  fi
  echo "pulled"

  # (c) cosign verify — proves the image was signed by our release workflow.
  if ! cosign verify "${image}" \
        --certificate-oidc-issuer "${COSIGN_ISSUER}" \
        --certificate-identity-regexp "${COSIGN_IDENTITY_REGEX}" \
        >/dev/null 2>&1; then
    record_fail "${pkg}: cosign verify failed"
    # continue — still worth checking the manifest shape for triage.
  else
    echo "cosign OK"
  fi

  # (d) multi-arch coverage. Dashboard is allowed to be amd64-only for now.
  manifest="$(docker manifest inspect "${image}" 2>/dev/null || echo '{}')"
  arches="$(echo "${manifest}" | jq -r '.manifests[].platform.architecture' 2>/dev/null | sort -u | paste -sd, -)"
  if [[ -z "${arches}" ]]; then
    record_fail "${pkg}: docker manifest inspect returned no architectures"
    continue
  fi
  echo "arches: ${arches}"

  want_amd64=1
  want_arm64=1
  if [[ "${pkg}" == "dashboard" && "${CORDUM_ALLOW_AMD64_ONLY_DASHBOARD:-1}" == "1" ]]; then
    # Allow dashboard to be amd64-only unless the caller explicitly
    # tightens the check by setting CORDUM_ALLOW_AMD64_ONLY_DASHBOARD=0.
    want_arm64=0
  fi

  if (( want_amd64 == 1 )) && [[ ",${arches}," != *",amd64,"* ]]; then
    record_fail "${pkg}: missing linux/amd64 manifest"
  fi
  if (( want_arm64 == 1 )) && [[ ",${arches}," != *",arm64,"* ]]; then
    record_fail "${pkg}: missing linux/arm64 manifest"
  fi
done

if (( ${#failures[@]} > 0 )); then
  echo ""
  echo "VERIFY FAILED (${#failures[@]} issue(s)):" >&2
  printf '  - %s\n' "${failures[@]}" >&2
  exit 1
fi

echo ""
echo "verify_published_images: PASS — all ${#PACKAGES[@]} images @ ${TAG} pulled, signed, multi-arch."

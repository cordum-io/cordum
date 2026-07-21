#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
workflow="$root/.github/workflows/ci.yml"
go_mod="$root/go.mod"

require() {
  local pattern="$1"
  local description="$2"
  if ! grep -Eq -- "$pattern" "$workflow"; then
    echo "missing handshake interop workflow contract: $description" >&2
    exit 1
  fi
}

require '^  handshake-trust-interop:$' 'required job'
require '^    name: Handshake Trust Interop$' 'stable required-check name'
require 'redis:7\.4\.2-alpine@sha256:02419de7eddf55aa5bcf49efb74e88fa8d931b4d77c07eff8a6b2144472b6952' 'pinned Redis image digest'
require 'CAP_HANDSHAKE_REDIS_URL: redis://127\.0\.0\.1:6379/0' 'declared Redis endpoint'
require 'repository: cordum-io/cap' 'CAP checkout'
require 'ref: \$\{\{ env\.CAP_REV \}\}' 'exact CAP checkout revision'
require 'run: ./tests/handshakeinterop/run\.sh "\$GITHUB_WORKSPACE/cap"' 'mandatory installed-artifact runner'

cap_rev="$(sed -nE 's/^[[:space:]]+CAP_REV: ([0-9a-f]{40})$/\1/p' "$workflow")"
if [[ ! "$cap_rev" =~ ^[0-9a-f]{40}$ ]]; then
  echo 'CAP_REV must be one full immutable commit SHA' >&2
  exit 1
fi
runner_sha="$(sed -nE 's/^[[:space:]]+CAP_HANDSHAKE_CAP_SHA: ([0-9a-f]{40})$/\1/p' "$workflow")"
if [[ "$runner_sha" != "$cap_rev" ]]; then
  echo "runner CAP SHA $runner_sha does not match checkout revision $cap_rev" >&2
  exit 1
fi
cap_module_version="$(sed -nE 's#^\tgithub.com/cordum-io/cap/v2 (v[^ ]+)$#\1#p' "$go_mod")"
if [[ -z "$cap_module_version" ]]; then
  echo 'go.mod has no github.com/cordum-io/cap/v2 requirement line to check against' >&2
  exit 1
fi
if [[ "$cap_module_version" =~ -([0-9a-f]{12})$ ]]; then
  # Pseudo-version (e.g. v2.14.1-0.20260720175350-2a8983f57629): the commit
  # hash is embedded in the string itself, so it can be cross-checked
  # offline against CAP_REV without a live cap checkout.
  module_suffix="${BASH_REMATCH[1]}"
  if [[ "${cap_rev:0:12}" != "$module_suffix" ]]; then
    echo "CAP_REV $cap_rev does not match go.mod CAP pseudo-version $module_suffix" >&2
    exit 1
  fi
else
  # Clean release tag (e.g. v2.15.1): no commit hash is embedded in the
  # version string, so this self-test cannot verify CAP_REV against it
  # without fetching cap's repo -- that's the job's own "Check out exact
  # CAP dependency" step's responsibility. Whoever bumps CAP_REV here must
  # point it at the exact commit the go.mod tag resolves to.
  echo "go.mod pins a release tag ($cap_module_version); skipping the offline pseudo-version cross-check for CAP_REV $cap_rev" >&2
fi

echo "handshake interop workflow contract: PASS cap_rev=$cap_rev"

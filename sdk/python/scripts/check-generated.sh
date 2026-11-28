#!/usr/bin/env bash
# Regenerates cordum_sdk._generated with generate.sh, then fails when the result differs from the
# committed tree. CI runs it in the required OpenAPI Validate job, so a spec change that does not
# ship the regenerated client fails there. It rewrites src/cordum_sdk/_generated in place.
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd -- "${SCRIPT_DIR}/.." && pwd)"

bash "${SCRIPT_DIR}/generate.sh"

# Untracked files count too: a module missing from the commit comes back as a new file.
status="$(git -C "${ROOT_DIR}" status --porcelain --untracked-files=all -- src/cordum_sdk/_generated)"
if [[ -n "${status}" ]]; then
  printf '%s\n' "${status}" >&2
  echo "error: sdk/python/src/cordum_sdk/_generated is out of date; run bash sdk/python/scripts/generate.sh and commit the result" >&2
  exit 1
fi
echo "Python SDK generated client is current"

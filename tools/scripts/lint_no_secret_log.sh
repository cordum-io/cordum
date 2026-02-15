#!/usr/bin/env bash
# Lint check: ensure shell scripts do not print raw secret values to stdout/stderr.
# Allowed patterns: masked output (first N chars), retrieval commands, env var names.
# Add "# no-secret-lint" to a line to suppress a false positive.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
FAILED=0

# Patterns that expand secret variables in print/log statements.
# Only match actual secret value variables, not names like SECRET_NAME.
SECRET_PATTERN='(echo|info|warn|log|printf).*\$\{?(CORDUM_)?(API_KEY|ADMIN_PASSWORD)\}?'
# Patterns that indicate safe usage (masking, retrieval commands, suppression).
SAFE_PATTERN='masked|:0:[0-9]+|base64|kubectl.*secret|no-secret-lint|\\\$CORDUM_API_KEY|\\\$API_KEY'

for f in "$REPO_ROOT"/tools/scripts/*.sh; do
  # Skip this lint script itself.
  [[ "$f" == *lint_no_secret_log* ]] && continue

  matches=$(grep -nE "$SECRET_PATTERN" "$f" | grep -vE "$SAFE_PATTERN" || true)
  if [[ -n "$matches" ]]; then
    echo "FAIL: $f may log raw secrets:"
    echo "$matches"
    FAILED=1
  fi
done

if [[ "$FAILED" -eq 0 ]]; then
  echo "OK: No raw secret logging found in tools/scripts/*.sh"
fi
exit $FAILED

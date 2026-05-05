#!/usr/bin/env bash
# Lint check: ensure shell scripts do not print raw secret values to stdout/stderr,
# AND that test fixtures / snapshot files do not embed real secret-shaped values
# (EDGE-071 audit invariant). Real secret content in committed test fixtures
# would defeat the redaction call-site tests by giving them legitimate-looking
# data that downstream tools could match later.
#
# Allowed patterns: masked output (first N chars), retrieval commands, env var names.
# Add "# no-secret-lint" to a line to suppress a false positive.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
FAILED=0

# --- Phase 1: shell-script raw-secret-log lint (pre-EDGE-071 behavior) --------
# Patterns that expand secret variables in print/log statements.
# Only match actual secret value variables, not names like SECRET_NAME.
SECRET_PATTERN='(echo|info|warn|log|printf).*\$\{?(CORDUM_)?(API_KEY|ADMIN_PASSWORD)\}?'
# Patterns that indicate safe usage (masking, retrieval commands, suppression).
SAFE_PATTERN='masked|:0:[0-9]+|base64|kubectl.*secret|no-secret-lint|\\\$CORDUM_API_KEY|\\\$API_KEY'

# Files scanned: every shell script under tools/scripts and docs/LOCAL_E2E.md
# (the latter carries example commands; per EDGE-027 it must not embed real
# secret values).
SCAN_TARGETS=("$REPO_ROOT"/tools/scripts/*.sh "$REPO_ROOT"/docs/LOCAL_E2E.md)

for f in "${SCAN_TARGETS[@]}"; do
  # Skip this lint script itself.
  [[ "$f" == *lint_no_secret_log* ]] && continue
  [[ -f "$f" ]] || continue

  matches=$(grep -nE "$SECRET_PATTERN" "$f" | grep -vE "$SAFE_PATTERN" || true)
  if [[ -n "$matches" ]]; then
    echo "FAIL: $f may log raw secrets:"
    echo "$matches"
    FAILED=1
  fi
done

# --- Phase 2: EDGE-071 test-fixture raw-secret-shape audit -------------------
# Patterns that match REAL secret-shaped values (not synthetic test fixtures
# using "cordum_fake_" / "test-secret-" / "EXAMPLE" / "AKIAIOSFODNN7EXAMPLE"-
# documented placeholders).
#   - sk- followed by ≥20 chars: OpenAI-style API key shape.
#   - ghp_ / github_pat_ followed by ≥20 chars: GitHub PAT shape.
#   - AKIA followed by exactly 16 uppercase alphanumerics: AWS access key id.
#   - "-----BEGIN [...] PRIVATE KEY-----": PEM private key block.
TEST_FIXTURE_PATTERN='(sk-[A-Za-z0-9_-]{20,}|ghp_[A-Za-z0-9_]{20,}|github_pat_[A-Za-z0-9_]{20,}|\bAKIA[0-9A-Z]{16}\b|-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----)'
# Patterns that mark known-synthetic fixtures the lint must allow through:
#   - cordum_fake_* — explicit marker prefix used by core/edge tests.
#   - sk-test* / sk-fake* / sk-edge* / sk-proj-* / sk-redaction* —
#     synthetic OpenAI-shape sentinels (sk-edge*, sk-proj-abc-* are the
#     EDGE-* test-id prefix conventions used across core/edge tests).
#   - ghp_test* / ghp_fake* / ghp_edge* / ghp_redaction* — synthetic
#     GitHub-shape sentinels.
#   - github_pat_test* / github_pat_fake* / github_pat_edge* — same for
#     GitHub PAT shape.
#   - AKIAIOSFODNN7EXAMPLE — AWS-documented public placeholder.
#   - EXAMPLEKEY / EXAMPLE / TESTKEY suffixes on AWS-shape access keys.
#   - DUMMY*, synthetic, placeholder prefix tokens.
#   - pemBody / privateKeyBody — synthetic PEM-block body sentinels in
#     redaction tests.
#   - sentinels\[ — Go map-indexed test sentinel pattern.
#   - // no-secret-lint — explicit suppression marker.
TEST_FIXTURE_SAFE_PATTERN='cordum_fake_|sk-test|sk-fake|sk-edge|sk-proj-|sk-redaction|sk-leaked|ghp_test|ghp_fake|ghp_edge|ghp_redaction|ghp_leaked|github_pat_test|github_pat_fake|github_pat_edge|github_pat_leaked|AKIAIOSFODNN7EXAMPLE|EXAMPLEKEY|EXAMPLE\b|TESTKEY|DUMMY|synthetic|placeholder|pemBody|privateKeyBody|sentinels\[|no-secret-lint'

# EDGE-071 audit invariant — scan test source + fixture files for raw secrets.
TEST_SCAN_GLOBS=(
  "$REPO_ROOT/core/edge"
  "$REPO_ROOT/core/controlplane/gateway"
  "$REPO_ROOT/cmd"
)

# Find Go test sources + JSON fixtures, then grep each for the real-shape pattern,
# excluding lines that match the safe pattern. Single-pass to keep CI runtime low.
while IFS= read -r f; do
  matches=$(grep -nE "$TEST_FIXTURE_PATTERN" "$f" 2>/dev/null | grep -vE "$TEST_FIXTURE_SAFE_PATTERN" || true)
  if [[ -n "$matches" ]]; then
    echo "FAIL: $f embeds real secret-shaped value (use cordum_fake_ / sk-test* / ghp_test* / AKIAIOSFODNN7EXAMPLE):"
    echo "$matches"
    FAILED=1
  fi
done < <(find "${TEST_SCAN_GLOBS[@]}" \( -name '*_test.go' -o -path '*/testdata/*' \) -type f 2>/dev/null)

# --- Phase 3: EDGE-068 argv-only exec guard ----------------------------------
# Hook-boundary subprocesses must never route untrusted payloads through a
# shell interpreter. Keep the check intentionally grep-based so it works in
# local Git Bash/CI without extra tooling. Suppress audited false positives
# with "# no-shell-exec-lint".
SHELL_EXEC_PATTERN='exec\.Command(Context)?\([^)]*"(sh|bash|cmd|cmd\.exe|powershell|powershell\.exe|pwsh|pwsh\.exe)"[^)]*("-c"|"/[cC]"|"-Command")'

while IFS= read -r f; do
  matches=$(grep -nE "$SHELL_EXEC_PATTERN" "$f" 2>/dev/null | grep -v 'no-shell-exec-lint' || true)
  if [[ -n "$matches" ]]; then
    echo "FAIL: $f may spawn a shell interpreter via exec.Command; use argv-only safeexec:"
    echo "$matches"
    FAILED=1
  fi
done < <(find "$REPO_ROOT/cmd" "$REPO_ROOT/core" -name '*.go' -type f 2>/dev/null)

if [[ "$FAILED" -eq 0 ]]; then
  echo "OK: No raw secret logging, test-fixture leaks, or shell exec patterns found"
fi
exit $FAILED

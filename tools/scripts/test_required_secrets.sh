#!/usr/bin/env bash
# =============================================================================
# test_required_secrets.sh — Verify release assets reject missing REDIS_PASSWORD
#
# Run from the project root:
#   bash tools/scripts/test_required_secrets.sh
# =============================================================================
set -euo pipefail

PASS=0
FAIL=0

pass() { echo "  PASS: $1"; PASS=$((PASS + 1)); }
fail() { echo "  FAIL: $1" >&2; FAIL=$((FAIL + 1)); }

echo "=== Required Secrets Checks ==="

# -------------------------------------------------------
# Test 1: docker-compose.release.yml rejects missing REDIS_PASSWORD
# -------------------------------------------------------
if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
  if REDIS_PASSWORD="" CORDUM_API_KEY="" docker compose -f docker-compose.release.yml config >/dev/null 2>&1; then
    fail "docker-compose.release.yml accepted missing REDIS_PASSWORD"
  else
    pass "docker-compose.release.yml rejects missing REDIS_PASSWORD"
  fi
else
  echo "  SKIP: docker compose not available"
fi

# -------------------------------------------------------
# Test 2: Grep guard — no weak fallbacks in release assets
# -------------------------------------------------------
if grep -q ':-cordum-dev' docker-compose.release.yml 2>/dev/null; then
  fail "docker-compose.release.yml contains :-cordum-dev fallback"
else
  pass "docker-compose.release.yml has no :-cordum-dev fallback"
fi

if grep -q ':-cordum-dev' Dockerfile 2>/dev/null; then
  fail "Dockerfile contains :-cordum-dev fallback"
else
  pass "Dockerfile has no :-cordum-dev fallback"
fi

if grep -q 'cordum-dev' Dockerfile 2>/dev/null; then
  fail "Dockerfile contains hardcoded cordum-dev"
else
  pass "Dockerfile has no hardcoded cordum-dev"
fi

# -------------------------------------------------------
# Summary
# -------------------------------------------------------
echo ""
echo "=== Results: ${PASS} passed, ${FAIL} failed ==="
if (( FAIL > 0 )); then
  exit 1
fi

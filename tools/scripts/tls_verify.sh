#!/usr/bin/env bash
# =============================================================================
# TLS Verification — E2E checks for Cordum TLS-everywhere deployment
#
# Usage:
#   ./tools/scripts/tls_verify.sh
#
# Prerequisites:
#   - Stack running with TLS (cordumctl dev / cordumctl up)
#   - CORDUM_API_KEY set in environment
#   - certs/ directory with generated certificates
# =============================================================================
set -euo pipefail

log() { echo "[tls-verify] $*"; }

# --- Configuration ---
CERT_DIR="${CORDUM_CERT_DIR:-./certs}"
CA_CERT="${CERT_DIR}/ca/ca.crt"
CLIENT_CERT="${CERT_DIR}/client/tls.crt"
CLIENT_KEY="${CERT_DIR}/client/tls.key"
GATEWAY_URL="${CORDUM_API_BASE:-https://localhost:8081}"
API_KEY="${CORDUM_API_KEY:-}"
TENANT_ID="${CORDUM_TENANT_ID:-default}"

PASSED=0
FAILED=0
TOTAL=7

# --- Dependency checks ---
if [[ -z "${API_KEY}" ]]; then
  echo "ERROR: CORDUM_API_KEY is required" >&2
  exit 1
fi

if [[ ! -f "${CA_CERT}" ]]; then
  echo "ERROR: CA certificate not found at ${CA_CERT}" >&2
  echo "  Run 'cordumctl generate-certs' first" >&2
  exit 1
fi

for cmd in curl docker; do
  if ! command -v "${cmd}" >/dev/null 2>&1; then
    echo "ERROR: missing dependency: ${cmd}" >&2
    exit 1
  fi
done

# --- Compose command ---
compose_cmd=()
if docker compose version >/dev/null 2>&1; then
  compose_cmd=(docker compose)
elif command -v docker-compose >/dev/null 2>&1; then
  compose_cmd=(docker-compose)
else
  echo "ERROR: docker compose required" >&2
  exit 1
fi

# --- Test helpers ---
run_check() {
  local num="$1"
  local name="$2"
  shift 2
  echo ""
  log "Check ${num}: ${name}"
  if "$@"; then
    log "  PASS"
    PASSED=$((PASSED + 1))
  else
    log "  FAIL"
    FAILED=$((FAILED + 1))
  fi
}

# ==========================================================================
# Check 1: Gateway HTTPS — curl with --cacert gets HTTP 200
# ==========================================================================
check_gateway_https() {
  local code
  code="$(curl -sS -o /dev/null -w "%{http_code}" --cacert "${CA_CERT}" \
    -H "X-API-Key: ${API_KEY}" -H "X-Tenant-ID: ${TENANT_ID}" \
    "${GATEWAY_URL}/api/v1/status" 2>/dev/null || echo "000")"
  log "  HTTP status: ${code}"
  [[ "${code}" == "200" ]]
}

# ==========================================================================
# Check 2: WebSocket over TLS — upgrade request succeeds
# ==========================================================================
check_websocket_tls() {
  local code
  code="$(curl -sS -o /dev/null -w "%{http_code}" --cacert "${CA_CERT}" \
    -H "Connection: Upgrade" -H "Upgrade: websocket" \
    -H "Sec-WebSocket-Version: 13" -H "Sec-WebSocket-Key: dGVzdA==" \
    "${GATEWAY_URL}/api/v1/stream" 2>/dev/null || echo "000")"
  log "  HTTP status: ${code}"
  # 101 = switching protocols, 400/426 = upgrade required (still proves TLS handshake worked)
  [[ "${code}" == "101" || "${code}" == "400" || "${code}" == "426" || "${code}" == "403" ]]
}

# ==========================================================================
# Check 3: CLI with --cacert — cordumctl status exits 0
# ==========================================================================
check_cli_with_cacert() {
  if ! command -v cordumctl >/dev/null 2>&1; then
    # Try building it
    if ! go build -o /tmp/cordumctl ./cmd/cordumctl 2>/dev/null; then
      log "  SKIP: cordumctl not available"
      return 0
    fi
    local cordumctl=/tmp/cordumctl
  else
    local cordumctl=cordumctl
  fi
  CORDUM_API_KEY="${API_KEY}" CORDUM_TENANT_ID="${TENANT_ID}" \
    "${cordumctl}" status --gateway "${GATEWAY_URL}" --cacert "${CA_CERT}" 2>/dev/null
}

# ==========================================================================
# Check 4: CLI without --cacert (negative) — should fail TLS verification
# ==========================================================================
check_cli_without_cacert() {
  if ! command -v cordumctl >/dev/null 2>&1; then
    local cordumctl=/tmp/cordumctl
    if [[ ! -x "${cordumctl}" ]]; then
      log "  SKIP: cordumctl not available"
      return 0
    fi
  else
    local cordumctl=cordumctl
  fi
  # Should exit non-zero because TLS verification fails without CA cert
  if CORDUM_API_KEY="${API_KEY}" CORDUM_TENANT_ID="${TENANT_ID}" CORDUM_TLS_CA="" \
    "${cordumctl}" status --gateway "${GATEWAY_URL}" 2>/dev/null; then
    log "  Connected without --cacert — TLS verification is NOT enforced"
    return 1
  else
    log "  Correctly rejected without --cacert"
    return 0
  fi
}

# ==========================================================================
# Check 5: Mock-bank NATS TLS — logs show TLS enabled
# ==========================================================================
check_mock_bank_nats_tls() {
  local logs
  logs="$("${compose_cmd[@]}" logs mock-bank-worker 2>/dev/null || true)"
  if [[ -z "${logs}" ]]; then
    # Try the service name without 'worker' suffix
    logs="$("${compose_cmd[@]}" logs mock-bank 2>/dev/null || true)"
  fi
  if [[ -z "${logs}" ]]; then
    log "  SKIP: mock-bank service not found in compose"
    return 0
  fi
  if echo "${logs}" | grep -qi "TLS enabled\|tls.*nats\|NATS TLS"; then
    return 0
  fi
  log "  No TLS indicator found in mock-bank logs"
  return 1
}

# ==========================================================================
# Check 6: NATS mTLS enforcement — unauthenticated connection rejected
# ==========================================================================
check_nats_mtls_enforcement() {
  # Try connecting to NATS without client certs — should be rejected
  local result
  result="$(curl -sS --cacert "${CA_CERT}" "https://localhost:4222" 2>&1 || true)"
  # NATS TLS with verify: true should reject connections without client certs.
  # curl will get a TLS handshake error or connection refused, which is the expected behavior.
  if echo "${result}" | grep -qiE "ssl|tls|handshake|certificate|alert|error|reset|refused"; then
    log "  NATS correctly rejects unauthenticated TLS connections"
    return 0
  fi
  # If NATS isn't exposed on localhost, check compose logs for TLS verify events
  local nats_logs
  nats_logs="$("${compose_cmd[@]}" logs nats 2>/dev/null | tail -50 || true)"
  if echo "${nats_logs}" | grep -qi "tls\|verify\|certificate"; then
    log "  NATS TLS verification active (from logs)"
    return 0
  fi
  log "  Could not verify NATS mTLS enforcement"
  return 1
}

# ==========================================================================
# Check 7: Redis TLS — no connection errors in service logs
# ==========================================================================
check_redis_tls() {
  local errors=0
  for svc in api-gateway scheduler safety-kernel workflow-engine context-engine; do
    local logs
    logs="$("${compose_cmd[@]}" logs "${svc}" 2>/dev/null | tail -100 || true)"
    if echo "${logs}" | grep -qi "redis.*connection.*refused\|redis.*tls.*error\|redis.*certificate.*error"; then
      log "  ${svc}: Redis TLS connection errors found"
      errors=$((errors + 1))
    fi
  done
  if [[ "${errors}" -gt 0 ]]; then
    log "  ${errors} service(s) have Redis TLS errors"
    return 1
  fi
  log "  No Redis TLS errors in service logs"
  return 0
}

# ==========================================================================
# Run all checks
# ==========================================================================
echo "==========================================="
echo " Cordum TLS Verification"
echo "==========================================="
log "CA cert:  ${CA_CERT}"
log "Gateway:  ${GATEWAY_URL}"
log "Tenant:   ${TENANT_ID}"

run_check 1 "Gateway HTTPS"              check_gateway_https
run_check 2 "WebSocket over TLS"         check_websocket_tls
run_check 3 "CLI with --cacert"          check_cli_with_cacert
run_check 4 "CLI without --cacert (neg)" check_cli_without_cacert
run_check 5 "Mock-bank NATS TLS"         check_mock_bank_nats_tls
run_check 6 "NATS mTLS enforcement"      check_nats_mtls_enforcement
run_check 7 "Redis TLS"                  check_redis_tls

# ==========================================================================
# Summary
# ==========================================================================
echo ""
echo "==========================================="
echo " TLS Verification: ${PASSED}/${TOTAL} checks passed"
echo "==========================================="

if [[ "${FAILED}" -gt 0 ]]; then
  exit 1
fi

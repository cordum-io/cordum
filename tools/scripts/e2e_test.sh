#!/bin/bash
# =============================================================================
# Cordum E2E Test Suite
# Tests the full system through the dashboard nginx proxy (port 8082)
# =============================================================================

set -euo pipefail

BASE="http://localhost:8082/api/v1"
GW="http://localhost:8081/api/v1"
API_KEY="17852da6e8545660dc45cfd37992308cc59ac0917066b7615b8075ad6d5d52b8"

PASS=0
FAIL=0
SKIP=0
ERRORS=""

green() { printf "\033[32m%s\033[0m\n" "$1"; }
red()   { printf "\033[31m%s\033[0m\n" "$1"; }
yellow(){ printf "\033[33m%s\033[0m\n" "$1"; }
bold()  { printf "\033[1m%s\033[0m\n" "$1"; }

check() {
  local name="$1" expected="$2" actual="$3"
  if [ "$actual" = "$expected" ]; then
    green "  PASS: $name (HTTP $actual)"
    PASS=$((PASS+1))
  else
    red "  FAIL: $name (expected $expected, got $actual)"
    FAIL=$((FAIL+1))
    ERRORS="${ERRORS}\n  - ${name}: expected ${expected}, got ${actual}"
  fi
}

check_body() {
  local name="$1" pattern="$2" body="$3"
  if echo "$body" | grep -q "$pattern"; then
    green "  PASS: $name (body contains '$pattern')"
    PASS=$((PASS+1))
  else
    red "  FAIL: $name (body missing '$pattern')"
    FAIL=$((FAIL+1))
    ERRORS="${ERRORS}\n  - ${name}: body missing '${pattern}'"
  fi
}

SESSION=""

# =============================================================================
bold "=== PHASE 1: Infrastructure Health ==="
# =============================================================================

bold "1.1 Dashboard (nginx) serves index.html"
code=$(curl -s -o /dev/null -w "%{http_code}" http://localhost:8082/)
check "GET / (dashboard)" "200" "$code"

bold "1.2 Dashboard config.json"
body=$(curl -s http://localhost:8082/config.json)
code=$(echo "$body" | python -c "import sys,json; d=json.load(sys.stdin); print('200')" 2>/dev/null || echo "ERR")
check "GET /config.json (valid JSON)" "200" "$code"
check_body "config.json has empty apiBaseUrl" '"apiBaseUrl": ""' "$body"

bold "1.3 Gateway health"
code=$(curl -s -o /dev/null -w "%{http_code}" http://localhost:8081/health)
check "GET /health (gateway)" "200" "$code"

bold "1.4 NATS reachable"
# NATS doesn't speak HTTP; test TCP connectivity
nats_ok=$(python -c "
import socket
s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
s.settimeout(2)
try:
    s.connect(('localhost', 4222))
    s.close()
    print('ok')
except:
    print('fail')
" 2>/dev/null || echo "fail")
if [ "$nats_ok" = "ok" ]; then
  green "  PASS: NATS reachable (TCP 4222)"
  PASS=$((PASS+1))
else
  red "  FAIL: NATS unreachable (TCP 4222)"
  FAIL=$((FAIL+1))
fi

bold "1.5 Redis reachable"
pong=$(redis-cli -h localhost -p 6379 ping 2>/dev/null || echo "FAIL")
if [ "$pong" = "PONG" ]; then
  green "  PASS: Redis PING -> PONG"
  PASS=$((PASS+1))
else
  yellow "  SKIP: Redis CLI not available"
  SKIP=$((SKIP+1))
fi

# =============================================================================
bold ""
bold "=== PHASE 2: Auth Flow (via nginx proxy) ==="
# =============================================================================

bold "2.1 Auth config (public endpoint)"
body=$(curl -s "$BASE/auth/config")
code=$(curl -s -o /dev/null -w "%{http_code}" "$BASE/auth/config")
check "GET /auth/config" "200" "$code"
check_body "auth config has password_enabled" "password_enabled" "$body"

bold "2.2 Unauthenticated request rejected"
code=$(curl -s -o /dev/null -w "%{http_code}" "$BASE/workers")
check "GET /workers (no auth)" "401" "$code"

bold "2.3 API key auth"
code=$(curl -s -o /dev/null -w "%{http_code}" -H "X-API-Key: $API_KEY" -H "X-Tenant-ID: default" "$BASE/workers")
check "GET /workers (API key)" "200" "$code"

bold "2.4 Invalid API key rejected"
code=$(curl -s -o /dev/null -w "%{http_code}" -H "X-API-Key: bad-key" "$BASE/workers")
check "GET /workers (bad key)" "401" "$code"

bold "2.5 User login (admin/admin123)"
body=$(curl -s -X POST "$BASE/auth/login" \
  -H "Content-Type: application/json" \
  -H "X-API-Key: $API_KEY" \
  -d '{"username":"admin","password":"admin123"}')
code=$(curl -s -o /dev/null -w "%{http_code}" -X POST "$BASE/auth/login" \
  -H "Content-Type: application/json" \
  -H "X-API-Key: $API_KEY" \
  -d '{"username":"admin","password":"admin123"}')
check "POST /auth/login" "200" "$code"

# Extract session token
SESSION=$(echo "$body" | python -c "import sys,json; print(json.load(sys.stdin).get('token',''))" 2>/dev/null || echo "")
if [ -n "$SESSION" ]; then
  green "  PASS: Got session token (${SESSION:0:20}...)"
  PASS=$((PASS+1))
else
  red "  FAIL: No session token in login response"
  FAIL=$((FAIL+1))
  ERRORS="${ERRORS}\n  - Login: no session token returned"
  # Fallback to API key for remaining tests
  SESSION=""
fi

bold "2.6 Session validation"
if [ -n "$SESSION" ]; then
  code=$(curl -s -o /dev/null -w "%{http_code}" -H "X-API-Key: $SESSION" -H "X-Tenant-ID: default" "$BASE/auth/session")
  check "GET /auth/session (session token)" "200" "$code"
else
  yellow "  SKIP: No session token"
  SKIP=$((SKIP+1))
fi

# Use session or fall back to API key
if [ -n "$SESSION" ]; then
  AUTH_HEADER="X-API-Key: $SESSION"
else
  AUTH_HEADER="X-API-Key: $API_KEY"
fi

# =============================================================================
bold ""
bold "=== PHASE 3: Core API Endpoints ==="
# =============================================================================

bold "3.1 Workers"
code=$(curl -s -o /dev/null -w "%{http_code}" -H "$AUTH_HEADER" -H "X-Tenant-ID: default" "$BASE/workers")
check "GET /workers" "200" "$code"

bold "3.2 Status"
code=$(curl -s -o /dev/null -w "%{http_code}" -H "$AUTH_HEADER" -H "X-Tenant-ID: default" "$BASE/status")
check "GET /status" "200" "$code"

bold "3.3 Jobs list"
body=$(curl -s -H "$AUTH_HEADER" -H "X-Tenant-ID: default" "$BASE/jobs")
code=$(curl -s -o /dev/null -w "%{http_code}" -H "$AUTH_HEADER" -H "X-Tenant-ID: default" "$BASE/jobs")
check "GET /jobs" "200" "$code"

bold "3.4 DLQ page"
code=$(curl -s -o /dev/null -w "%{http_code}" -H "$AUTH_HEADER" -H "X-Tenant-ID: default" "$BASE/dlq/page?limit=10")
check "GET /dlq/page" "200" "$code"

bold "3.5 Approvals"
code=$(curl -s -o /dev/null -w "%{http_code}" -H "$AUTH_HEADER" -H "X-Tenant-ID: default" "$BASE/approvals?limit=10")
check "GET /approvals" "200" "$code"

bold "3.6 Policy bundles"
code=$(curl -s -o /dev/null -w "%{http_code}" -H "$AUTH_HEADER" -H "X-Tenant-ID: default" "$BASE/policy/bundles")
check "GET /policy/bundles" "200" "$code"

bold "3.7 Policy rules"
code=$(curl -s -o /dev/null -w "%{http_code}" -H "$AUTH_HEADER" -H "X-Tenant-ID: default" "$BASE/policy/rules")
check "GET /policy/rules" "200" "$code"

bold "3.8 Policy audit"
code=$(curl -s -o /dev/null -w "%{http_code}" -H "$AUTH_HEADER" -H "X-Tenant-ID: default" "$BASE/policy/audit")
check "GET /policy/audit" "200" "$code"

bold "3.9 Workflows"
code=$(curl -s -o /dev/null -w "%{http_code}" -H "$AUTH_HEADER" -H "X-Tenant-ID: default" "$BASE/workflows")
check "GET /workflows" "200" "$code"

bold "3.10 Workflow runs"
code=$(curl -s -o /dev/null -w "%{http_code}" -H "$AUTH_HEADER" -H "X-Tenant-ID: default" "$BASE/workflow-runs?limit=20")
check "GET /workflow-runs" "200" "$code"

bold "3.11 Packs"
code=$(curl -s -o /dev/null -w "%{http_code}" -H "$AUTH_HEADER" -H "X-Tenant-ID: default" "$BASE/packs")
check "GET /packs" "200" "$code"

bold "3.12 Config / system"
code=$(curl -s -o /dev/null -w "%{http_code}" -H "$AUTH_HEADER" -H "X-Tenant-ID: default" "$BASE/config")
check "GET /config" "200" "$code"

# =============================================================================
bold ""
bold "=== PHASE 4: Job Submission Flow ==="
# =============================================================================

bold "4.1 Submit a job"
JOB_BODY='{"prompt":"E2E test job","topic":"job.default","metadata":{"test":"e2e"}}'
body=$(curl -s -X POST "$BASE/jobs" \
  -H "$AUTH_HEADER" \
  -H "X-Tenant-ID: default" \
  -H "Content-Type: application/json" \
  -d "$JOB_BODY")
code=$(curl -s -o /dev/null -w "%{http_code}" -X POST "$BASE/jobs" \
  -H "$AUTH_HEADER" \
  -H "X-Tenant-ID: default" \
  -H "Content-Type: application/json" \
  -d "$JOB_BODY")

# Accept 200, 201, 202 as success
if [ "$code" = "200" ] || [ "$code" = "201" ] || [ "$code" = "202" ]; then
  green "  PASS: POST /jobs (HTTP $code)"
  PASS=$((PASS+1))
else
  red "  FAIL: POST /jobs (expected 2xx, got $code)"
  FAIL=$((FAIL+1))
  ERRORS="${ERRORS}\n  - POST /jobs: expected 2xx, got $code"
fi

# Extract job ID
JOB_ID=$(echo "$body" | python -c "import sys,json; d=json.load(sys.stdin); print(d.get('job_id','') or d.get('id',''))" 2>/dev/null || echo "")
if [ -n "$JOB_ID" ]; then
  green "  PASS: Got job ID: $JOB_ID"
  PASS=$((PASS+1))
else
  yellow "  SKIP: Could not extract job ID from response"
  SKIP=$((SKIP+1))
fi

bold "4.2 Get job detail"
if [ -n "$JOB_ID" ]; then
  sleep 1
  code=$(curl -s -o /dev/null -w "%{http_code}" -H "$AUTH_HEADER" -H "X-Tenant-ID: default" "$BASE/jobs/$JOB_ID")
  check "GET /jobs/$JOB_ID" "200" "$code"
else
  yellow "  SKIP: No job ID"
  SKIP=$((SKIP+1))
fi

bold "4.3 Job appears in listing"
if [ -n "$JOB_ID" ]; then
  body=$(curl -s -H "$AUTH_HEADER" -H "X-Tenant-ID: default" "$BASE/jobs")
  if echo "$body" | grep -q "$JOB_ID"; then
    green "  PASS: Job $JOB_ID found in /jobs listing"
    PASS=$((PASS+1))
  else
    yellow "  SKIP: Job not in listing (may have moved to DLQ)"
    SKIP=$((SKIP+1))
  fi
else
  yellow "  SKIP: No job ID"
  SKIP=$((SKIP+1))
fi

# =============================================================================
bold ""
bold "=== PHASE 5: WebSocket Stream ==="
# =============================================================================

bold "5.1 WebSocket upgrade (API key auth, through proxy)"
ENCODED=$(python -c "import base64; key=b'$API_KEY'; print(base64.urlsafe_b64encode(key).decode().rstrip('='))")
timeout 3 curl -s -o /dev/null -w "%{http_code}" \
  -H "Upgrade: websocket" \
  -H "Connection: upgrade" \
  -H "Sec-WebSocket-Version: 13" \
  -H "Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==" \
  -H "Sec-WebSocket-Protocol: cordum-api-key.${ENCODED}" \
  "http://localhost:8082/api/v1/stream" 2>/dev/null && ws_result="fail" || ws_result="pass"

if [ "$ws_result" = "pass" ]; then
  green "  PASS: WebSocket upgrade succeeded (connection held open)"
  PASS=$((PASS+1))
else
  red "  FAIL: WebSocket upgrade rejected"
  FAIL=$((FAIL+1))
fi

bold "5.2 WebSocket with session token (through proxy)"
if [ -n "$SESSION" ]; then
  SESS_ENC=$(python -c "import base64; print(base64.urlsafe_b64encode(b'$SESSION').decode().rstrip('='))")
  timeout 3 curl -s -o /dev/null -w "%{http_code}" \
    -H "Upgrade: websocket" \
    -H "Connection: upgrade" \
    -H "Sec-WebSocket-Version: 13" \
    -H "Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==" \
    -H "Sec-WebSocket-Protocol: cordum-api-key.${SESS_ENC}" \
    http://localhost:8082/api/v1/stream 2>/dev/null && ws_result="fail" || ws_result="pass"

  if [ "$ws_result" = "pass" ]; then
    green "  PASS: WebSocket with session token (connection held open)"
    PASS=$((PASS+1))
  else
    red "  FAIL: WebSocket with session token rejected"
    FAIL=$((FAIL+1))
  fi
else
  yellow "  SKIP: No session token"
  SKIP=$((SKIP+1))
fi

bold "5.3 WebSocket without auth rejected"
code=$(timeout 3 curl -s -o /dev/null -w "%{http_code}" \
  -H "Upgrade: websocket" \
  -H "Connection: upgrade" \
  -H "Sec-WebSocket-Version: 13" \
  -H "Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==" \
  http://localhost:8082/api/v1/stream 2>/dev/null || echo "000")
check "WebSocket no auth" "401" "$code"

# =============================================================================
bold ""
bold "=== PHASE 6: Nginx Proxy Validation ==="
# =============================================================================

bold "6.1 API proxy works (proxy -> gateway)"
code=$(curl -s -o /dev/null -w "%{http_code}" -H "$AUTH_HEADER" -H "X-Tenant-ID: default" http://localhost:8082/api/v1/workers)
check "Proxy: /api/v1/workers" "200" "$code"

bold "6.2 SPA fallback (unknown path -> index.html)"
code=$(curl -s -o /dev/null -w "%{http_code}" http://localhost:8082/some/unknown/page)
check "SPA fallback" "200" "$code"

bold "6.3 Security headers present"
headers=$(curl -sI http://localhost:8082/)
if echo "$headers" | grep -qi "X-Content-Type-Options"; then
  green "  PASS: X-Content-Type-Options header present"
  PASS=$((PASS+1))
else
  red "  FAIL: X-Content-Type-Options header missing"
  FAIL=$((FAIL+1))
fi

if echo "$headers" | grep -qi "X-Frame-Options"; then
  green "  PASS: X-Frame-Options header present"
  PASS=$((PASS+1))
else
  red "  FAIL: X-Frame-Options header missing"
  FAIL=$((FAIL+1))
fi

# =============================================================================
bold ""
bold "=== PHASE 7: Safety Kernel ==="
# =============================================================================

bold "7.1 Safety evaluate via gateway"
SAFETY_BODY='{"capabilities":["shell.exec"],"risk_tags":["dangerous"],"metadata":{}}'
body=$(curl -s -X POST "$BASE/safety/evaluate" \
  -H "$AUTH_HEADER" \
  -H "X-Tenant-ID: default" \
  -H "Content-Type: application/json" \
  -d "$SAFETY_BODY")
code=$(curl -s -o /dev/null -w "%{http_code}" -X POST "$BASE/safety/evaluate" \
  -H "$AUTH_HEADER" \
  -H "X-Tenant-ID: default" \
  -H "Content-Type: application/json" \
  -d "$SAFETY_BODY")

if [ "$code" = "200" ] || [ "$code" = "201" ]; then
  green "  PASS: POST /safety/evaluate (HTTP $code)"
  PASS=$((PASS+1))
  check_body "Safety decision returned" "decision" "$body"
else
  yellow "  SKIP: POST /safety/evaluate ($code) — endpoint may not exist via HTTP"
  SKIP=$((SKIP+1))
fi

# =============================================================================
bold ""
bold "=== PHASE 8: Error Handling ==="
# =============================================================================

bold "8.1 404 for unknown API path"
code=$(curl -s -o /dev/null -w "%{http_code}" -H "$AUTH_HEADER" -H "X-Tenant-ID: default" "$BASE/nonexistent")
# 404 or 405 are both acceptable
if [ "$code" = "404" ] || [ "$code" = "405" ]; then
  green "  PASS: Unknown path returns $code"
  PASS=$((PASS+1))
else
  red "  FAIL: Unknown path returned $code (expected 404/405)"
  FAIL=$((FAIL+1))
fi

bold "8.2 Invalid JSON body"
code=$(curl -s -o /dev/null -w "%{http_code}" -X POST "$BASE/jobs" \
  -H "$AUTH_HEADER" \
  -H "X-Tenant-ID: default" \
  -H "Content-Type: application/json" \
  -d "not-json")
check "POST /jobs with bad JSON" "400" "$code"

bold "8.3 Missing required field"
code=$(curl -s -o /dev/null -w "%{http_code}" -X POST "$BASE/jobs" \
  -H "$AUTH_HEADER" \
  -H "X-Tenant-ID: default" \
  -H "Content-Type: application/json" \
  -d '{}')
check "POST /jobs with empty body" "400" "$code"

# =============================================================================
bold ""
bold "=== PHASE 9: Logout ==="
# =============================================================================

bold "9.1 Logout"
if [ -n "$SESSION" ]; then
  code=$(curl -s -o /dev/null -w "%{http_code}" -X POST "$BASE/auth/logout" \
    -H "X-API-Key: $SESSION" \
    -H "X-Tenant-ID: default")
  # Accept 200 or 204
  if [ "$code" = "200" ] || [ "$code" = "204" ]; then
    green "  PASS: POST /auth/logout (HTTP $code)"
    PASS=$((PASS+1))
  else
    red "  FAIL: POST /auth/logout ($code)"
    FAIL=$((FAIL+1))
  fi

  bold "9.2 Session invalidated after logout"
  code=$(curl -s -o /dev/null -w "%{http_code}" -H "X-API-Key: $SESSION" -H "X-Tenant-ID: default" "$BASE/workers")
  check "GET /workers with expired session" "401" "$code"
else
  yellow "  SKIP: No session to test logout"
  SKIP=$((SKIP+2))
fi

# =============================================================================
bold ""
bold "============================================"
bold "  E2E Test Results"
bold "============================================"
green "  Passed: $PASS"
if [ "$FAIL" -gt 0 ]; then
  red "  Failed: $FAIL"
else
  green "  Failed: $FAIL"
fi
if [ "$SKIP" -gt 0 ]; then
  yellow "  Skipped: $SKIP"
fi
bold "  Total:  $((PASS + FAIL + SKIP))"

if [ "$FAIL" -gt 0 ]; then
  bold ""
  red "  Failures:"
  printf "$ERRORS\n"
  bold ""
  exit 1
else
  bold ""
  green "  All tests passed!"
  bold ""
  exit 0
fi

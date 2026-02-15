#!/usr/bin/env bash
# =============================================================================
# Cordum Soak Test
# Long-running test to detect retry storms, log floods, and resource drift.
#
# Usage:
#   CORDUM_API_KEY=... bash tools/scripts/soak_test.sh
#
# Environment:
#   CORDUM_API_KEY            Required. API key for gateway auth.
#   CORDUM_API_BASE           Gateway base URL (default: http://localhost:8081/api/v1)
#   SOAK_DURATION_MINUTES     Test duration in minutes (default: 60)
#   SOAK_INTERVAL_SECONDS     Seconds between request batches (default: 5)
#   SOAK_MEMORY_DRIFT_PCT     Max allowed memory growth % (default: 50)
#   SOAK_LOG_STORM_THRESHOLD  Max identical log lines per 60s window (default: 100)
#   SOAK_ERROR_RATE_PCT       Max allowed 4xx/5xx rate % (default: 1)
#   SOAK_RESULTS_FILE         Output JSON file (default: soak_results.json)
#   SOAK_METRICS_FILE         Metrics log file (default: soak_metrics.log)
#   SOAK_HTTP_LOG             HTTP status log (default: soak_http.log)
# =============================================================================
set -euo pipefail

# ---------------------------------------------------------------------------
# Configuration
# ---------------------------------------------------------------------------
API_BASE="${CORDUM_API_BASE:-http://localhost:8081/api/v1}"
API_KEY="${CORDUM_API_KEY:-}"
TENANT_ID="${CORDUM_TENANT_ID:-default}"
DURATION_MINUTES="${SOAK_DURATION_MINUTES:-60}"
INTERVAL_SECONDS="${SOAK_INTERVAL_SECONDS:-5}"
MEMORY_DRIFT_PCT="${SOAK_MEMORY_DRIFT_PCT:-50}"
LOG_STORM_THRESHOLD="${SOAK_LOG_STORM_THRESHOLD:-100}"
ERROR_RATE_PCT="${SOAK_ERROR_RATE_PCT:-1}"
RESULTS_FILE="${SOAK_RESULTS_FILE:-soak_results.json}"
METRICS_FILE="${SOAK_METRICS_FILE:-soak_metrics.log}"
HTTP_LOG="${SOAK_HTTP_LOG:-soak_http.log}"

SOAK_ID="soak-$(date +%s)"

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------
require() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "[soak] missing dependency: $1" >&2
    exit 1
  fi
}

log() { echo "[soak] $(date +%H:%M:%S) $*"; }
die() { echo "[soak] ERROR: $*" >&2; exit 1; }

api() {
  local method="$1" path="$2"
  shift 2
  local url="${API_BASE}${path}"
  local status
  status=$(curl -s -o /dev/null -w "%{http_code}" \
    -X "$method" "$url" \
    -H "X-API-Key: ${API_KEY}" \
    -H "X-Tenant-ID: ${TENANT_ID}" \
    -H "Content-Type: application/json" \
    "$@" 2>/dev/null || echo "000")
  echo "$status"
}

api_body() {
  local method="$1" path="$2"
  shift 2
  local url="${API_BASE}${path}"
  curl -s -X "$method" "$url" \
    -H "X-API-Key: ${API_KEY}" \
    -H "X-Tenant-ID: ${TENANT_ID}" \
    -H "Content-Type: application/json" \
    "$@" 2>/dev/null || echo "{}"
}

record_status() {
  local endpoint="$1" status="$2"
  echo "$(date +%s) ${endpoint} ${status}" >> "${HTTP_LOG}"
}

# ---------------------------------------------------------------------------
# Preflight
# ---------------------------------------------------------------------------
require curl

if [[ -z "${API_KEY}" ]]; then
  die "CORDUM_API_KEY is required; export it before running the soak test."
fi

# Initialise log files
: > "${HTTP_LOG}"
: > "${METRICS_FILE}"

log "Soak test ${SOAK_ID} starting"
log "Duration: ${DURATION_MINUTES}m | Interval: ${INTERVAL_SECONDS}s"
log "Memory drift limit: ${MEMORY_DRIFT_PCT}% | Log storm limit: ${LOG_STORM_THRESHOLD}"
log "Error rate limit: ${ERROR_RATE_PCT}%"

# ---------------------------------------------------------------------------
# Health check
# ---------------------------------------------------------------------------
log "Checking gateway health..."
health_status=$(api GET /health)
if [[ "${health_status}" != "200" ]]; then
  die "Gateway not healthy (status=${health_status}). Start services first."
fi
log "Gateway healthy"

# ---------------------------------------------------------------------------
# Baseline metrics
# ---------------------------------------------------------------------------
collect_docker_metrics() {
  if command -v docker >/dev/null 2>&1; then
    docker stats --no-stream --format \
      '{"container":"{{.Name}}","cpu":"{{.CPUPerc}}","mem_usage":"{{.MemUsage}}","mem_pct":"{{.MemPerc}}","ts":"'"$(date +%s)"'"}' \
      2>/dev/null || true
  fi
}

collect_log_counts() {
  if command -v docker >/dev/null 2>&1; then
    local since="${1:-60s}"
    docker compose logs --since "${since}" 2>/dev/null | wc -l || echo "0"
  else
    echo "0"
  fi
}

log "Collecting baseline metrics..."
baseline_metrics=$(collect_docker_metrics)
if [[ -n "${baseline_metrics}" ]]; then
  echo "# baseline $(date +%s)" >> "${METRICS_FILE}"
  echo "${baseline_metrics}" >> "${METRICS_FILE}"
fi

# Extract baseline memory percentages for drift comparison.
declare -A BASELINE_MEM
while IFS= read -r line; do
  if [[ -z "${line}" || "${line}" == "#"* ]]; then continue; fi
  container=$(echo "${line}" | grep -o '"container":"[^"]*"' | head -1 | cut -d'"' -f4 || true)
  mem_pct=$(echo "${line}" | grep -o '"mem_pct":"[^"]*"' | head -1 | cut -d'"' -f4 | tr -d '%' || true)
  if [[ -n "${container}" && -n "${mem_pct}" ]]; then
    BASELINE_MEM["${container}"]="${mem_pct}"
  fi
done <<< "${baseline_metrics}"

# ---------------------------------------------------------------------------
# Check for container restarts (baseline)
# ---------------------------------------------------------------------------
get_restart_counts() {
  if command -v docker >/dev/null 2>&1; then
    docker compose ps --format '{{.Name}} {{.Status}}' 2>/dev/null | while read -r name status; do
      restarts=$(echo "${status}" | grep -oP '\(\d+\)' | tr -d '()' || echo "0")
      echo "${name}:${restarts:-0}"
    done
  fi
}

BASELINE_RESTARTS=$(get_restart_counts)

# ---------------------------------------------------------------------------
# Cleanup trap
# ---------------------------------------------------------------------------
SOAK_PIDS=()
cleanup() {
  log "Cleaning up..."
  for pid in "${SOAK_PIDS[@]:-}"; do
    kill "${pid}" 2>/dev/null || true
    wait "${pid}" 2>/dev/null || true
  done
}
trap cleanup EXIT

# ---------------------------------------------------------------------------
# Load phase
# ---------------------------------------------------------------------------
END_TIME=$(( $(date +%s) + DURATION_MINUTES * 60 ))
BATCH=0
LAST_METRICS_TS=0
LAST_LOG_CHECK_TS=0

# Fixed job payloads for determinism
JOB_PAYLOADS=(
  '{"prompt":"soak test echo","topic":"job.default","priority":"normal"}'
  '{"prompt":"","topic":"job.default","priority":"normal"}'
  '{"prompt":"soak test high","topic":"job.default","priority":"high"}'
)

log "Starting load phase (${DURATION_MINUTES} minutes)..."

while [[ $(date +%s) -lt ${END_TIME} ]]; do
  BATCH=$((BATCH + 1))
  NOW=$(date +%s)

  # --- Submit jobs (mix of valid/invalid) ---
  for payload in "${JOB_PAYLOADS[@]}"; do
    status=$(api POST /jobs -d "${payload}")
    record_status "POST:/jobs" "${status}"
  done

  # --- Hit read endpoints ---
  for endpoint in /jobs /approvals /config /dlq /health; do
    status=$(api GET "${endpoint}")
    record_status "GET:${endpoint}" "${status}"
  done

  # --- Approve pending approvals ---
  approvals_body=$(api_body GET /approvals)
  # Extract job IDs that need approval (best-effort parse without jq)
  approval_ids=$(echo "${approvals_body}" | grep -oP '"id"\s*:\s*"[^"]*"' | head -5 | cut -d'"' -f4 || true)
  for jid in ${approval_ids}; do
    status=$(api POST "/approvals/${jid}/approve" -d '{"reason":"soak auto-approve"}')
    record_status "POST:/approvals/approve" "${status}"
  done

  # --- Collect metrics every 60 seconds ---
  if [[ $((NOW - LAST_METRICS_TS)) -ge 60 ]]; then
    LAST_METRICS_TS=${NOW}
    echo "# snapshot ${NOW}" >> "${METRICS_FILE}"
    collect_docker_metrics >> "${METRICS_FILE}"

    elapsed=$(( (NOW - (END_TIME - DURATION_MINUTES * 60)) / 60 ))
    remaining=$(( (END_TIME - NOW) / 60 ))
    log "Batch ${BATCH} | ${elapsed}m elapsed, ${remaining}m remaining"
  fi

  # --- Check log volume every 60 seconds ---
  if [[ $((NOW - LAST_LOG_CHECK_TS)) -ge 60 ]]; then
    LAST_LOG_CHECK_TS=${NOW}
    log_count=$(collect_log_counts "60s")
    echo "# logcount ${NOW} ${log_count}" >> "${METRICS_FILE}"
  fi

  sleep "${INTERVAL_SECONDS}"
done

log "Load phase complete. ${BATCH} batches sent."

# ---------------------------------------------------------------------------
# Analysis phase
# ---------------------------------------------------------------------------
log "Analysing results..."

FAILURES=()
WARNINGS=()

# --- 1. HTTP error rate ---
TOTAL_REQUESTS=$(wc -l < "${HTTP_LOG}" || echo "0")
ERROR_REQUESTS=$(awk '$3 >= 400 || $3 == "000"' "${HTTP_LOG}" | wc -l || echo "0")
if [[ ${TOTAL_REQUESTS} -gt 0 ]]; then
  # Use integer arithmetic (multiply by 100 first for precision)
  ERROR_RATE_X100=$(( ERROR_REQUESTS * 10000 / TOTAL_REQUESTS ))
  THRESHOLD_X100=$(( ERROR_RATE_PCT * 100 ))
  ACTUAL_RATE="$(( ERROR_RATE_X100 / 100 )).$(printf '%02d' $(( ERROR_RATE_X100 % 100 )))"

  if [[ ${ERROR_RATE_X100} -gt ${THRESHOLD_X100} ]]; then
    FAILURES+=("HTTP error rate ${ACTUAL_RATE}% exceeds threshold ${ERROR_RATE_PCT}% (${ERROR_REQUESTS}/${TOTAL_REQUESTS} requests)")
  fi
  log "HTTP error rate: ${ACTUAL_RATE}% (${ERROR_REQUESTS}/${TOTAL_REQUESTS})"
else
  WARNINGS+=("No HTTP requests recorded")
fi

# --- 2. 4xx retry storm detection ---
# Check if any endpoint has >50% of its requests returning 4xx in succession
STORM_ENDPOINTS=$(awk '$3 >= 400 && $3 < 500' "${HTTP_LOG}" | awk '{print $2}' | sort | uniq -c | sort -rn | head -5 || true)
while IFS= read -r line; do
  if [[ -z "${line}" ]]; then continue; fi
  count=$(echo "${line}" | awk '{print $1}')
  endpoint=$(echo "${line}" | awk '{print $2}')
  endpoint_total=$(grep -c "${endpoint}" "${HTTP_LOG}" || echo "0")
  if [[ ${endpoint_total} -gt 10 && ${count} -gt $((endpoint_total / 2)) ]]; then
    FAILURES+=("4xx retry storm on ${endpoint}: ${count}/${endpoint_total} requests returned 4xx")
  fi
done <<< "${STORM_ENDPOINTS}"

# --- 3. Memory drift ---
if command -v docker >/dev/null 2>&1; then
  final_metrics=$(collect_docker_metrics)
  while IFS= read -r line; do
    if [[ -z "${line}" || "${line}" == "#"* ]]; then continue; fi
    container=$(echo "${line}" | grep -o '"container":"[^"]*"' | head -1 | cut -d'"' -f4 || true)
    mem_pct=$(echo "${line}" | grep -o '"mem_pct":"[^"]*"' | head -1 | cut -d'"' -f4 | tr -d '%' || true)
    if [[ -n "${container}" && -n "${mem_pct}" ]]; then
      baseline="${BASELINE_MEM[${container}]:-0}"
      if [[ "${baseline}" != "0" ]]; then
        # Integer comparison: drift = ((final - baseline) * 100) / baseline
        baseline_int=$(echo "${baseline}" | cut -d'.' -f1)
        final_int=$(echo "${mem_pct}" | cut -d'.' -f1)
        if [[ ${baseline_int} -gt 0 ]]; then
          drift=$(( (final_int - baseline_int) * 100 / baseline_int ))
          if [[ ${drift} -gt ${MEMORY_DRIFT_PCT} ]]; then
            FAILURES+=("Memory drift on ${container}: ${baseline}% -> ${mem_pct}% (${drift}% growth, limit ${MEMORY_DRIFT_PCT}%)")
          fi
        fi
      fi
    fi
  done <<< "${final_metrics}"
fi

# --- 4. Log storm detection ---
if command -v docker >/dev/null 2>&1; then
  # Get last 5 minutes of logs and check for repeated lines
  recent_logs=$(docker compose logs --since 300s 2>/dev/null || true)
  if [[ -n "${recent_logs}" ]]; then
    storm_lines=$(echo "${recent_logs}" | \
      sed 's/^[^ ]* //' | \
      sort | uniq -c | sort -rn | head -5)
    while IFS= read -r line; do
      if [[ -z "${line}" ]]; then continue; fi
      count=$(echo "${line}" | awk '{print $1}')
      if [[ ${count} -gt ${LOG_STORM_THRESHOLD} ]]; then
        msg=$(echo "${line}" | sed 's/^[[:space:]]*[0-9]* //' | head -c 120)
        FAILURES+=("Log storm: '${msg}' repeated ${count} times in 5 minutes (limit ${LOG_STORM_THRESHOLD})")
      fi
    done <<< "${storm_lines}"
  fi
fi

# --- 5. Container restart detection ---
if command -v docker >/dev/null 2>&1; then
  FINAL_RESTARTS=$(get_restart_counts)
  while IFS= read -r entry; do
    if [[ -z "${entry}" ]]; then continue; fi
    name=$(echo "${entry}" | cut -d: -f1)
    final_count=$(echo "${entry}" | cut -d: -f2)
    baseline_count=0
    while IFS= read -r bl; do
      bl_name=$(echo "${bl}" | cut -d: -f1)
      if [[ "${bl_name}" == "${name}" ]]; then
        baseline_count=$(echo "${bl}" | cut -d: -f2)
        break
      fi
    done <<< "${BASELINE_RESTARTS}"
    if [[ ${final_count} -gt ${baseline_count} ]]; then
      FAILURES+=("Container ${name} restarted during soak (${baseline_count} -> ${final_count})")
    fi
  done <<< "${FINAL_RESTARTS}"
fi

# ---------------------------------------------------------------------------
# Results
# ---------------------------------------------------------------------------
PASS_COUNT=0
FAIL_COUNT=${#FAILURES[@]}
WARN_COUNT=${#WARNINGS[@]}

# Build JSON results
{
  echo "{"
  echo "  \"soak_id\": \"${SOAK_ID}\","
  echo "  \"duration_minutes\": ${DURATION_MINUTES},"
  echo "  \"batches\": ${BATCH},"
  echo "  \"total_requests\": ${TOTAL_REQUESTS},"
  echo "  \"error_requests\": ${ERROR_REQUESTS},"
  echo "  \"pass\": $(( FAIL_COUNT == 0 ? 1 : 0 )),"
  echo "  \"failures\": ["
  for i in "${!FAILURES[@]}"; do
    comma=""
    if [[ $i -lt $((FAIL_COUNT - 1)) ]]; then comma=","; fi
    # Escape quotes in failure messages
    msg=$(echo "${FAILURES[$i]}" | sed 's/"/\\"/g')
    echo "    \"${msg}\"${comma}"
  done
  echo "  ],"
  echo "  \"warnings\": ["
  for i in "${!WARNINGS[@]}"; do
    comma=""
    if [[ $i -lt $((WARN_COUNT - 1)) ]]; then comma=","; fi
    msg=$(echo "${WARNINGS[$i]}" | sed 's/"/\\"/g')
    echo "    \"${msg}\"${comma}"
  done
  echo "  ],"
  echo "  \"timestamp\": \"$(date -u +%Y-%m-%dT%H:%M:%SZ)\""
  echo "}"
} > "${RESULTS_FILE}"

# ---------------------------------------------------------------------------
# Summary
# ---------------------------------------------------------------------------
echo ""
echo "============================================"
echo "  Soak Test Results: ${SOAK_ID}"
echo "============================================"
echo "  Duration:    ${DURATION_MINUTES} minutes"
echo "  Batches:     ${BATCH}"
echo "  Requests:    ${TOTAL_REQUESTS}"
echo "  Errors:      ${ERROR_REQUESTS}"
echo "  Failures:    ${FAIL_COUNT}"
echo "  Warnings:    ${WARN_COUNT}"
echo ""

if [[ ${FAIL_COUNT} -gt 0 ]]; then
  echo "FAILURES:"
  for f in "${FAILURES[@]}"; do
    echo "  - ${f}"
  done
  echo ""
fi

if [[ ${WARN_COUNT} -gt 0 ]]; then
  echo "WARNINGS:"
  for w in "${WARNINGS[@]}"; do
    echo "  - ${w}"
  done
  echo ""
fi

echo "Results written to: ${RESULTS_FILE}"
echo "Metrics written to: ${METRICS_FILE}"
echo "HTTP log written to: ${HTTP_LOG}"

if [[ ${FAIL_COUNT} -gt 0 ]]; then
  echo ""
  echo "SOAK TEST FAILED"
  exit 1
fi

echo ""
echo "SOAK TEST PASSED"
exit 0

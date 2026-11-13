#!/usr/bin/env bash
# Cordum Soak Test
# Long-running test to detect retry storms, log floods, and resource drift.
#
# Usage:
#   CORDUM_API_KEY=... bash tools/scripts/soak_test.sh
#
# Environment:
#   CORDUM_API_KEY            Required. API key for gateway auth.
#   CORDUM_API_BASE           Gateway base URL (default: http://localhost:8081/api/v1)
#   CORDUM_TLS_CA             Optional CA certificate for HTTPS gateways.
#   CORDUM_TLS_INSECURE       Set to 1/true only for local insecure TLS.
#   SOAK_DURATION_MINUTES     Test duration in minutes (default: 60)
#   SOAK_INTERVAL_SECONDS     Seconds between request batches (default: 5)
#   SOAK_MEMORY_DRIFT_PCT     Max allowed memory growth % (default: 50)
#   SOAK_MEMORY_DRIFT_MIN_MIB Absolute growth floor in MiB; growth below it only
#                             warns even when the % limit is exceeded (default: 32)
#   SOAK_WARMUP_SECONDS       Load time before the memory-drift baseline is
#                             captured; capped at half the run (default: 300)
#   SOAK_MEMORY_WINDOW_SAMPLES  Median window K (default 5, minimum 3); needs
#                             warm-up plus 2*K one-minute snapshots.
#   SOAK_METRICS_SOURCE       docker (default): missing/failed/empty stats FAIL.
#                             none (Helm): drift/restarts not evaluated, not PASS.
#   SOAK_LOG_STORM_THRESHOLD  Max identical log lines per 60s window (default: 100)
#   SOAK_ERROR_RATE_PCT       Max allowed 4xx/5xx rate % (default: 1)
#   SOAK_RESULTS_FILE         Output JSON file (default: soak_results.json)
#   SOAK_METRICS_FILE         Metrics log file (default: soak_metrics.log)
#   SOAK_HTTP_LOG             HTTP status log (default: soak_http.log)
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=tools/scripts/soak_test_lib.sh
source "${SCRIPT_DIR}/soak_test_lib.sh"

# Configuration
API_BASE="${CORDUM_API_BASE:-http://localhost:8081/api/v1}"
API_KEY="${CORDUM_API_KEY:-}"
TENANT_ID="${CORDUM_TENANT_ID:-default}"
DURATION_MINUTES="${SOAK_DURATION_MINUTES:-60}"
INTERVAL_SECONDS="${SOAK_INTERVAL_SECONDS:-5}"
MEMORY_DRIFT_PCT="${SOAK_MEMORY_DRIFT_PCT:-50}"
MEMORY_DRIFT_MIN_MIB="${SOAK_MEMORY_DRIFT_MIN_MIB:-32}"
WARMUP_SECONDS="${SOAK_WARMUP_SECONDS:-300}"
MEMORY_WINDOW_SAMPLES="${SOAK_MEMORY_WINDOW_SAMPLES:-5}"
METRICS_SOURCE="${SOAK_METRICS_SOURCE:-docker}"
LOG_STORM_THRESHOLD="${SOAK_LOG_STORM_THRESHOLD:-100}"
ERROR_RATE_PCT="${SOAK_ERROR_RATE_PCT:-1}"
RESULTS_FILE="${SOAK_RESULTS_FILE:-soak_results.json}"
METRICS_FILE="${SOAK_METRICS_FILE:-soak_metrics.log}"
HTTP_LOG="${SOAK_HTTP_LOG:-soak_http.log}"
TLS_CA="${CORDUM_TLS_CA:-}"
CURL_TLS_OPTS=()

SOAK_ID="soak-$(date +%s)"

# Findings are collected from the first docker stats sample onward.
FAILURES=()
WARNINGS=()
DRIFT_EVALUATED=false
DRIFT_BASELINE_SOURCE="none"

# Helpers
require() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "[soak] missing dependency: $1" >&2
    exit 1
  fi
}

log() { echo "[soak] $(date +%H:%M:%S) $*"; }
die() { echo "[soak] ERROR: $*" >&2; exit 1; }

if [[ "${CORDUM_TLS_INSECURE:-}" =~ ^(1|true|TRUE|yes|YES)$ ]]; then
  CURL_TLS_OPTS=(-k)
elif [[ -n "${TLS_CA}" ]]; then
  [[ -r "${TLS_CA}" ]] || die "CORDUM_TLS_CA is not readable: ${TLS_CA}"
  CURL_TLS_OPTS=(--cacert "${TLS_CA}")
  if [[ "${OS:-}" == "Windows_NT" ]]; then
    CURL_TLS_OPTS+=(--ssl-no-revoke)
  fi
fi

api() {
  local method="$1" path="$2"
  shift 2
  local url="${API_BASE}${path}"
  local status
  status=$(curl -s "${CURL_TLS_OPTS[@]}" -o /dev/null -w "%{http_code}" \
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
  curl -s "${CURL_TLS_OPTS[@]}" -X "$method" "$url" \
    -H "X-API-Key: ${API_KEY}" \
    -H "X-Tenant-ID: ${TENANT_ID}" \
    -H "Content-Type: application/json" \
    "$@" 2>/dev/null || echo "{}"
}

record_status() {
  local endpoint="$1" status="$2" expected="${3:-}"
  if [[ -n "${expected}" ]]; then
    echo "$(date +%s) ${endpoint} ${status} expected=${expected}" >> "${HTTP_LOG}"
    return
  fi
  echo "$(date +%s) ${endpoint} ${status}" >> "${HTTP_LOG}"
}

# Preflight
require curl

if [[ -z "${API_KEY}" ]]; then
  die "CORDUM_API_KEY is required; export it before running the soak test."
fi

# Initialise log files
: > "${HTTP_LOG}"
: > "${METRICS_FILE}"

# Validate before any arithmetic: awk would read a non-numeric limit or floor
# as 0, and Bash reads a leading-zero integer such as 0300 as octal.
for knob in MEMORY_DRIFT_PCT MEMORY_DRIFT_MIN_MIB; do
  if [[ ! "${!knob}" =~ ^[0-9]+(\.[0-9]+)?$ ]]; then
    die "SOAK_${knob} must be a non-negative number (got '${!knob}')"
  fi
done
if [[ ! "${WARMUP_SECONDS}" =~ ^[0-9]{1,9}$ ]]; then
  die "SOAK_WARMUP_SECONDS must be a non-negative integer of at most 9 digits (got '${WARMUP_SECONDS}')"
fi
WARMUP_SECONDS=$((10#${WARMUP_SECONDS}))
# Never spend more than half the run warming up, so short dispatch runs
# (e.g. SOAK_DURATION_MINUTES=1) still measure drift over real load.
MAX_WARMUP_SECONDS=$(( DURATION_MINUTES * 30 ))
if [[ ${WARMUP_SECONDS} -gt ${MAX_WARMUP_SECONDS} ]]; then
  WARMUP_SECONDS=${MAX_WARMUP_SECONDS}
fi
if [[ "${METRICS_SOURCE}" != "docker" && "${METRICS_SOURCE}" != "none" ]]; then
  die "SOAK_METRICS_SOURCE must be docker or none (got '${METRICS_SOURCE}')"
fi
if [[ ! "${MEMORY_WINDOW_SAMPLES}" =~ ^[0-9]{1,6}$ ]] || [[ $((10#${MEMORY_WINDOW_SAMPLES})) -lt 3 ]]; then
  die "SOAK_MEMORY_WINDOW_SAMPLES must be an integer of at least 3 (got '${MEMORY_WINDOW_SAMPLES}')"
fi
MEMORY_WINDOW_SAMPLES=$((10#${MEMORY_WINDOW_SAMPLES}))
if [[ "${METRICS_SOURCE}" == "docker" ]]; then
  require docker
fi

log "Soak test ${SOAK_ID} starting"
log "Duration: ${DURATION_MINUTES}m | Interval: ${INTERVAL_SECONDS}s | Metrics source: ${METRICS_SOURCE}"
log "Memory drift limit: ${MEMORY_DRIFT_PCT}% and ${MEMORY_DRIFT_MIN_MIB}MiB, median of ${MEMORY_WINDOW_SAMPLES} snapshots after ${WARMUP_SECONDS}s warm-up vs the last ${MEMORY_WINDOW_SAMPLES} | Log storm limit: ${LOG_STORM_THRESHOLD}"
log "Error rate limit: ${ERROR_RATE_PCT}%"

# Health check
log "Checking gateway health..."
health_status=$(api GET /health)
if [[ "${health_status}" != "200" ]]; then
  die "Gateway not healthy (status=${health_status}). Start services first."
fi
log "Gateway healthy"

# Baseline metrics
# One docker stats sample on stdout. A docker failure is returned to the caller
# (never swallowed); `timeout` keeps a hung daemon from stalling the soak.
collect_docker_metrics() {
  local fmt cmd=(docker stats --no-stream)
  if [[ "${METRICS_SOURCE}" != "docker" ]]; then return 0; fi
  fmt='{"container":"{{.Name}}","cpu":"{{.CPUPerc}}","mem_usage":"{{.MemUsage}}","mem_pct":"{{.MemPerc}}","ts":"'"$(date +%s)"'"}'
  if command -v timeout >/dev/null 2>&1; then cmd=(timeout 30 "${cmd[@]}"); fi
  "${cmd[@]}" --format "${fmt}"
}

# Record every "FAIL<TAB>msg" / "WARN<TAB>msg" line of $1 in FAILURES / WARNINGS.
add_findings() {
  local kind msg
  while IFS=$'\t' read -r kind msg; do
    case "${kind}" in
      FAIL) FAILURES+=("${msg}") ;;
      WARN) WARNINGS+=("${msg}") ;;
    esac
  done <<< "$1"
}

# Append a "# KIND <ts>" block, or return nonzero and record FAIL (severity fail)
# or WARN (mid-run gap). The window check determines whether enough samples remain.
sample_metrics() {
  local kind="$1" label="$2" severity="$3" ts out rc=0 findings
  if [[ "${METRICS_SOURCE}" != "docker" ]]; then return 0; fi
  ts=$(date +%s)
  out=$(collect_docker_metrics) || rc=$?
  findings=$(docker_collection_findings "${label}" "${rc}" "${out}")
  if [[ -z "${findings}" ]]; then
    { echo "# ${kind} ${ts}"; echo "${out}"; } >> "${METRICS_FILE}"
    return 0
  fi
  if [[ "${severity}" == "fail" ]]; then
    add_findings "${findings}"
  else
    add_findings "${findings//FAIL/WARN}"
  fi
  return 1
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
# The cold pre-load sample is diagnostic only: idle Go services sit at ~15MiB and
# legitimately double under load, so it is never the drift baseline. The real
# baseline window starts at the "# drift-baseline" marker written after warm-up.
sample_metrics baseline "pre-load baseline" fail || true

# Check for container restarts (baseline)
get_restart_counts() {
  if command -v docker >/dev/null 2>&1; then
    docker compose ps --format '{{.Name}} {{.Status}}' 2>/dev/null | while read -r name status; do
      restarts=$(echo "${status}" | grep -oP '\(\d+\)' | tr -d '()' || echo "0")
      echo "${name}:${restarts:-0}"
    done
  fi
}

BASELINE_RESTARTS=""
if [[ "${METRICS_SOURCE}" == "docker" ]]; then
  BASELINE_RESTARTS=$(get_restart_counts || true)
fi

# Cleanup trap
SOAK_PIDS=()
cleanup() {
  log "Cleaning up..."
  for pid in "${SOAK_PIDS[@]:-}"; do
    kill "${pid}" 2>/dev/null || true
    wait "${pid}" 2>/dev/null || true
  done
}
trap cleanup EXIT

# Load phase
LOAD_START=$(date +%s)
END_TIME=$(( LOAD_START + DURATION_MINUTES * 60 ))
BATCH=0
LAST_METRICS_TS=0
LAST_LOG_CHECK_TS=0
DRIFT_MARKER_TRIED=0

# Fixed job payloads for determinism
JOB_PAYLOADS=(
  '{"prompt":"soak test echo","topic":"job.default","priority":"normal"}'
  '{"prompt":"soak test high","topic":"job.default","priority":"high"}'
)
INVALID_JOB_PAYLOAD='{"prompt":"","topic":"job.default","priority":"normal"}'

log "Starting load phase (${DURATION_MINUTES} minutes)..."

while [[ $(date +%s) -lt ${END_TIME} ]]; do
  BATCH=$((BATCH + 1))
  NOW=$(date +%s)

  # --- Submit jobs (mix of valid/invalid) ---
  for payload in "${JOB_PAYLOADS[@]}"; do
    status=$(api POST /jobs -d "${payload}")
    record_status "POST:/jobs" "${status}"
  done
  status=$(api POST /jobs -d "${INVALID_JOB_PAYLOAD}")
  record_status "POST:/jobs:invalid-empty-prompt" "${status}" 400

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

  # After warm-up, attempt one marker capture. Failure means "not evaluated",
  # never a fallback to the cold pre-load sample.
  if [[ "${METRICS_SOURCE}" == "docker" && ${DRIFT_MARKER_TRIED} -eq 0 \
        && $((NOW - LOAD_START)) -ge ${WARMUP_SECONDS} ]]; then
    DRIFT_MARKER_TRIED=1
    if sample_metrics drift-baseline "warm-up baseline" fail; then
      log "Memory drift baseline window opened after ${WARMUP_SECONDS}s of load"
    fi
  fi

  # --- Collect metrics every 60 seconds (a failed sample is a gap, not a skip) ---
  if [[ $((NOW - LAST_METRICS_TS)) -ge 60 ]]; then
    LAST_METRICS_TS=${NOW}
    sample_metrics snapshot "mid-run snapshot" warn || true

    elapsed=$(( (NOW - LOAD_START) / 60 ))
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

# The end-of-run state always belongs to the final window.
sample_metrics snapshot "final" fail || true

# Analysis phase
log "Analysing results..."

# --- 1. HTTP error rate ---
TOTAL_REQUESTS=$(wc -l < "${HTTP_LOG}" || echo "0")
ERROR_REQUESTS=$(count_unexpected_http_responses "${HTTP_LOG}")
if [[ ${TOTAL_REQUESTS} -gt 0 ]]; then
  # Use integer arithmetic (multiply by 100 first for precision)
  ERROR_RATE_X100=$(( ERROR_REQUESTS * 10000 / TOTAL_REQUESTS ))
  THRESHOLD_X100=$(( ERROR_RATE_PCT * 100 ))
  ACTUAL_RATE="$(( ERROR_RATE_X100 / 100 )).$(printf '%02d' $(( ERROR_RATE_X100 % 100 )))"

  if [[ ${ERROR_RATE_X100} -gt ${THRESHOLD_X100} ]]; then
    FAILURES+=("HTTP error rate ${ACTUAL_RATE}% exceeds threshold ${ERROR_RATE_PCT}% (${ERROR_REQUESTS}/${TOTAL_REQUESTS} requests)")
  fi
  log "HTTP error rate: ${ACTUAL_RATE}% (${ERROR_REQUESTS}/${TOTAL_REQUESTS})"
fi
# A run that sent no batches or recorded no requests proves nothing.
add_findings "$(load_findings "${BATCH}" "${TOTAL_REQUESTS}")"

# --- 2. 4xx retry storm detection ---
# Check if any endpoint has >50% of its requests returning 4xx in succession
STORM_ENDPOINTS=$(unexpected_client_error_endpoints "${HTTP_LOG}" |
  LC_ALL=C sort | uniq -c | sort -rn | sed -n '1,5p')
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
# First/last K post-warm-up medians: FAIL above both limits or when not evaluable
# (see memory_drift_analysis in soak_test_lib.sh).
if [[ "${METRICS_SOURCE}" == "docker" ]]; then
  drift_findings=$(memory_drift_analysis "${METRICS_FILE}" "${MEMORY_DRIFT_PCT}" "${MEMORY_DRIFT_MIN_MIB}" "${MEMORY_WINDOW_SAMPLES}")
  add_findings "${drift_findings}"
  if [[ -n "$(memory_drift_baseline_ts "${METRICS_FILE}")" ]]; then DRIFT_BASELINE_SOURCE="post-warm-up"; fi
  if ! grep -q $'^FAIL\tMemory drift not evaluated' <<< "${drift_findings}"; then DRIFT_EVALUATED=true; fi
else
  WARNINGS+=("Memory drift and restart checks not evaluated (SOAK_METRICS_SOURCE=none)")
fi

# --- 4. Log storm detection ---
if command -v docker >/dev/null 2>&1; then
  # Get last 5 minutes of logs and check for repeated lines
  recent_logs=$(docker compose logs --no-color --since 300s 2>/dev/null || true)
  if [[ -n "${recent_logs}" ]]; then
    storm_lines=$(printf '%s\n' "${recent_logs}" | top_repeated_log_lines)
    while IFS= read -r line; do
      if [[ -z "${line}" ]]; then continue; fi
      count=$(echo "${line}" | awk '{print $1}')
      if [[ ${count} -gt ${LOG_STORM_THRESHOLD} ]]; then
        msg=$(echo "${line}" | sed 's/^[[:space:]]*[0-9]* //')
        msg="${msg:0:120}"
        FAILURES+=("Log storm: '${msg}' repeated ${count} times in 5 minutes (limit ${LOG_STORM_THRESHOLD})")
      fi
    done <<< "${storm_lines}"
  fi
fi

# --- 5. Container restart detection ---
# FAIL on restart-count growth or disappearance (restart_findings in the lib).
if [[ "${METRICS_SOURCE}" == "docker" ]]; then
  FINAL_RESTARTS=$(get_restart_counts || true)
  add_findings "$(restart_findings "${BASELINE_RESTARTS}" "${FINAL_RESTARTS}")"
fi

# Results
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
  echo "  \"memory_drift_evaluated\": ${DRIFT_EVALUATED},"
  echo "  \"memory_window_samples\": ${MEMORY_WINDOW_SAMPLES},"
  echo "  \"memory_baseline_source\": \"${DRIFT_BASELINE_SOURCE}\","
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

# Summary
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

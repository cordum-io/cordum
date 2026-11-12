#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LIB="${SCRIPT_DIR}/soak_test_lib.sh"
SOAK="${SCRIPT_DIR}/soak_test.sh"

if [[ ! -f "${LIB}" ]]; then
  echo "FAIL: soak analysis helper library is missing: ${LIB}" >&2
  exit 1
fi

# shellcheck source=tools/scripts/soak_test_lib.sh
source "${LIB}"

PASS=0
FAIL=0

assert_eq() {
  local name="$1" want="$2" got="$3"
  if [[ "${got}" == "${want}" ]]; then
    echo "PASS: ${name}"
    PASS=$((PASS + 1))
  else
    echo "FAIL: ${name}: got '${got}', want '${want}'" >&2
    FAIL=$((FAIL + 1))
  fi
}

tmpdir="$(mktemp -d -t soak-test-lib.XXXXXX)"
trap 'rm -rf "${tmpdir}"' EXIT

cat >"${tmpdir}/http.log" <<'EOF'
1 POST:/jobs 200
2 POST:/jobs:invalid-empty-prompt 400 expected=400
3 GET:/jobs 503
4 POST:/jobs:invalid-empty-prompt 401 expected=400
5 GET:/health 000000
EOF

assert_eq "expected client error is excluded from error rate" "3" \
  "$(count_unexpected_http_responses "${tmpdir}/http.log")"
assert_eq "only unexpected 4xx responses feed storm detection" \
  "POST:/jobs:invalid-empty-prompt" \
  "$(unexpected_client_error_endpoints "${tmpdir}/http.log")"

{
  for i in $(seq 1 7); do
    for _ in $(seq 1 "$((8 - i))"); do
      printf 'service | repeated-%02d\n' "${i}"
    done
  done
} >"${tmpdir}/docker.log"
top_lines="$(top_repeated_log_lines <"${tmpdir}/docker.log")"
assert_eq "log storm analysis returns at most five lines" "5" \
  "$(printf '%s\n' "${top_lines}" | wc -l | tr -d ' ')"
assert_eq "log storm analysis ranks the most repeated line first" "repeated-01" \
  "$(printf '%s\n' "${top_lines}" | sed -n '1p' | awk '{$1=""; sub(/^ /, ""); print}')"

# ---------------------------------------------------------------------------
# Memory drift: windowed medians over synthetic metrics files
# ---------------------------------------------------------------------------
T=$'\t'
RUN="${tmpdir}/metrics.log"

# emit_block KIND TS NAME=MEM...: one "# KIND TS" block with a docker stats
# record per NAME=MEM argument, in the format soak_test.sh appends.
emit_block() {
  local kind="$1" ts="$2" kv
  shift 2
  echo "# ${kind} ${ts}"
  for kv in "$@"; do
    printf '{"container":"%s","cpu":"0.50%%","mem_usage":"%s / 512MiB","mem_pct":"1.00%%","ts":"%s"}\n' \
      "${kv%%=*}" "${kv#*=}" "${ts}"
  done
}

# write_run FILE MEM...: a cold pre-load sample (api=15MiB), the drift-baseline
# marker, then one snapshot per MEM argument holding api=<MEM> and db=100MiB.
# DB_LAST and WORKER_FROM (1-based snapshot numbers) reshape the container set.
DB_LAST=999
WORKER_FROM=999
write_run() {
  local file="$1" i=0 ts=1000 mem recs
  shift
  {
    emit_block baseline 900 api=15MiB db=100MiB
    emit_block drift-baseline "${ts}" api=40MiB db=100MiB
    for mem in "$@"; do
      i=$((i + 1))
      ts=$((ts + 60))
      recs=("api=${mem}")
      if [[ ${i} -le ${DB_LAST} ]]; then recs+=("db=100MiB"); fi
      if [[ ${i} -ge ${WORKER_FROM} ]]; then recs+=("worker=20MiB"); fi
      emit_block snapshot "${ts}" "${recs[@]}"
    done
  } >"${file}"
}

rep() { local i; for ((i = 0; i < $1; i++)); do printf '%s ' "$2"; done; }

LIMIT=50
FLOOR=32
analyse() { memory_drift_analysis "${RUN}" "${LIMIT}" "${FLOOR}" "${1:-5}"; }

# check_run NAME WANT MEM...: write the run, assert the exact analysis output.
check_run() {
  local name="$1" want="$2"
  shift 2
  write_run "${RUN}" "$@"
  assert_eq "${name}" "${want}" "$(analyse)"
}

LEAK="FAIL${T}Memory drift on api: median 100.00MiB -> 200.00MiB (+100.00MiB, 100.0% growth over 5-snapshot windows, limit 50% and 32MiB)"
check_run "stable run passes silently" "" $(rep 10 40MiB)
check_run "bounded growth inside both windows (42 -> 46MiB) passes" "" \
  40MiB 41MiB 42MiB 43MiB 44MiB 44MiB 45MiB 46MiB 47MiB 48MiB
check_run "cold pre-load sample is diagnostic only (15 -> 100MiB steady state passes)" "" $(rep 10 100MiB)
check_run "true leak past both limits fails" "${LEAK}" $(rep 5 100MiB) $(rep 5 200MiB)
check_run "relative-only growth stays a WARN" \
  "WARN${T}Memory growth on api stayed under the absolute floor: median 20.00MiB -> 40.00MiB (+20.00MiB, 100.0% growth over 5-snapshot windows, limit 50% and 32MiB)" \
  $(rep 5 20MiB) $(rep 5 40MiB)
check_run "growth exactly at the absolute floor is not past it" \
  "WARN${T}Memory growth on api stayed under the absolute floor: median 32.00MiB -> 64.00MiB (+32.00MiB, 100.0% growth over 5-snapshot windows, limit 50% and 32MiB)" \
  $(rep 5 32MiB) $(rep 5 64MiB)
check_run "absolute-only growth (1000 -> 1100MiB) is under the relative limit" "" $(rep 5 1000MiB) $(rep 5 1100MiB)
check_run "growth exactly at the relative limit is not past it" "" $(rep 5 100MiB) $(rep 5 150MiB)
LIMIT=5 FLOOR=0 check_run "growth keeps decimals (19.5 -> 20.7MiB is 6.2%, not integer-truncated 5.3%)" \
  "FAIL${T}Memory drift on api: median 19.50MiB -> 20.70MiB (+1.20MiB, 6.2% growth over 5-snapshot windows, limit 5% and 0MiB)" \
  $(rep 5 19.5MiB) $(rep 5 20.7MiB)
check_run "one spike at the start of the baseline window does not flip the verdict" "" \
  400MiB 40MiB 40MiB 40MiB 40MiB $(rep 5 40MiB)
check_run "one spike at the end of the final window does not flip the verdict" "" \
  $(rep 5 40MiB) 40MiB 40MiB 40MiB 40MiB 400MiB
check_run "a spike in the baseline window does not hide a true leak" "${LEAK}" \
  100MiB 100MiB 900MiB 100MiB 100MiB $(rep 5 200MiB)
check_run "sustained growth (3 of 5 final samples) fails" \
  "FAIL${T}Memory drift on api: median 40.00MiB -> 200.00MiB (+160.00MiB, 400.0% growth over 5-snapshot windows, limit 50% and 32MiB)" \
  $(rep 5 40MiB) 40MiB 40MiB 200MiB 200MiB 200MiB
LEAK_K3="FAIL${T}Memory drift on api: median 100.00MiB -> 200.00MiB (+100.00MiB, 100.0% growth over 3-snapshot windows, limit 50% and 32MiB)"
write_run "${RUN}" $(rep 3 100MiB) $(rep 3 200MiB)
assert_eq "K=3 (the minimum) evaluates a 6-snapshot run" "${LEAK_K3}" "$(analyse 3)"

# Window statistics, including even K and the START_TS filter.
write_run "${RUN}" 10MiB 20MiB 30MiB 40MiB 50MiB 60MiB 70MiB 80MiB
assert_eq "even K: first window median is the decimal mean of the middle pair" \
  "api${T}25.00${T}1.00${T}4${T}4
db${T}100.00${T}1.00${T}4${T}4" "$(memory_window_medians "${RUN}" 1000 4 first)"
assert_eq "even K: final window median is the decimal mean of the middle pair" \
  "api${T}65.00${T}1.00${T}4${T}4
db${T}100.00${T}1.00${T}4${T}4" "$(memory_window_medians "${RUN}" 1000 4 last)"
assert_eq "snapshots before START_TS (and the cold sample) are excluded" \
  "api${T}55.00${T}1.00${T}2${T}2
db${T}100.00${T}1.00${T}2${T}2" "$(memory_window_medians "${RUN}" 1300 2 first)"
write_run "${RUN}" 10MiB 20MiB 30MiB 40MiB 50MiB 60MiB 70MiB 80MiB 90MiB 100MiB
assert_eq "odd K: medians are the middle samples of each window" \
  "api${T}30.00${T}1.00${T}5${T}5 api${T}80.00${T}1.00${T}5${T}5" \
  "$(memory_window_medians "${RUN}" 1000 5 first | head -1) $(memory_window_medians "${RUN}" 1000 5 last | head -1)"

{
  emit_block drift-baseline 1000 gib=1.5GiB
  for i in 1 2 3 4 5 6; do
    emit_block snapshot $((1000 + i)) gib=1.5GiB kb=512kB bytes=900B kib=2048KiB mb=3MB tib=0.5TiB my-svc_1.a=40MiB
  done
} >"${RUN}"
assert_eq "GiB/kB/B/KiB/MB/TiB convert to MiB and dotted names parse" \
  "gib${T}1536.00${T}1.00${T}3${T}3
kb${T}0.49${T}1.00${T}3${T}3
bytes${T}0.00${T}1.00${T}3${T}3
kib${T}2.00${T}1.00${T}3${T}3
mb${T}2.86${T}1.00${T}3${T}3
tib${T}524288.00${T}1.00${T}3${T}3
my-svc_1.a${T}40.00${T}1.00${T}3${T}3" "$(memory_window_medians "${RUN}" 1000 3 first)"

# Not evaluated: too few snapshots, missing marker, disappeared/new containers.
NOT_EVALUATED="Memory drift not evaluated"
WINDOWS="for non-overlapping baseline and final windows"
check_run "9 snapshots (fewer than 2K) fail as not evaluated" \
  "FAIL${T}${NOT_EVALUATED}: 9 valid post-warm-up snapshots, need 10 (2 x 5) ${WINDOWS}" $(rep 9 40MiB)
check_run "6 snapshots (windows would overlap) fail as not evaluated" \
  "FAIL${T}${NOT_EVALUATED}: 6 valid post-warm-up snapshots, need 10 (2 x 5) ${WINDOWS}" $(rep 6 40MiB)
check_run "a marker with no snapshots fails as not evaluated" \
  "FAIL${T}${NOT_EVALUATED}: no valid post-warm-up container samples"
write_run "${RUN}" $(rep 10 40MiB)
awk '!/^# drift-baseline/' "${RUN}" >"${RUN}.nomarker"
assert_eq "no drift-baseline marker fails (never falls back to the cold sample)" \
  "FAIL${T}${NOT_EVALUATED}: no drift-baseline marker, so the post-warm-up baseline was never captured" \
  "$(memory_drift_analysis "${RUN}.nomarker" 50 32 5)"
DB_LAST=5 check_run "container in the baseline window but absent at the end fails" \
  "FAIL${T}${NOT_EVALUATED} for db: present in the baseline window but absent from the final window" $(rep 10 40MiB)
DB_LAST=7 check_run "container missing from most of the final window fails" \
  "FAIL${T}${NOT_EVALUATED} for db: 5 baseline-window and 2 final-window samples, need 5 in each" $(rep 10 40MiB)
WORKER_FROM=6 check_run "container first seen at the end has no baseline and fails" \
  "FAIL${T}${NOT_EVALUATED} for worker: first sampled after the baseline window, so it has no baseline" $(rep 10 40MiB)
WORKER_FROM=9 check_run "container first seen in the last snapshots has no baseline and fails" \
  "FAIL${T}${NOT_EVALUATED} for worker: first sampled after the baseline window, so it has no baseline" $(rep 10 40MiB)
assert_eq "locale decimal commas do not change parsing" "${LEAK}" \
  "$(write_run "${RUN}" $(rep 5 100MiB) $(rep 5 200MiB); LC_ALL=de_DE.UTF-8 analyse)"

# Malformed records are a hard FAIL with the offending line number.
write_run "${RUN}.ok" $(rep 10 40MiB)
bad_line=$(($(wc -l <"${RUN}.ok") + 2))
for spec in \
  'not a JSON object|garbage' \
  'not a JSON object|{"container":"api","mem_usage":"40MiB / 1GiB"' \
  'missing or invalid container name|{"cpu":"1%","mem_usage":"40MiB / 1GiB","mem_pct":"1.00%"}' \
  'unparseable mem_usage|{"container":"api","mem_usage":"abc / 1GiB","mem_pct":"1.00%"}' \
  'unparseable mem_usage|{"container":"api","mem_usage":"","mem_pct":"1.00%"}' \
  'unparseable mem_usage|{"container":"api","mem_usage":"12XB / 1GiB","mem_pct":"1.00%"}' \
  'unparseable mem_usage|{"container":"api","mem_usage":"1234567890123456B / 1GiB","mem_pct":"1.00%"}' \
  'unparseable mem_pct|{"container":"api","mem_usage":"40MiB / 1GiB","mem_pct":""}' \
  'unparseable mem_pct|{"container":"api","mem_usage":"40MiB / 1GiB","mem_pct":"n/a"}'; do
  IFS='|' read -r reason record <<<"${spec}"
  { cat "${RUN}.ok"; echo "# snapshot 5000"; printf '%s\n' "${record}"; } >"${RUN}"
  assert_eq "malformed record (${reason}): ${record:0:50}" \
    "FAIL${T}${NOT_EVALUATED}: 1 malformed metric line(s) (first: line ${bad_line}: ${reason})" "$(analyse)"
done
awk '{ printf "%s\r\n", $0 }' "${RUN}.ok" >"${RUN}"
assert_eq "a CRLF metrics file fails clearly instead of mis-parsing" \
  "FAIL${T}${NOT_EVALUATED}: $(wc -l <"${RUN}.ok" | tr -d ' ') malformed metric line(s) (first: line 1: CRLF line ending, metrics files must use LF)
FAIL${T}${NOT_EVALUATED}: no valid post-warm-up container samples" "$(analyse)"
{ echo "# baseline 100"; echo "garbage"; cat "${RUN}.ok"; } >"${RUN}"
assert_eq "a malformed cold pre-load record is diagnostic only" "" "$(analyse)"

# ---------------------------------------------------------------------------
# Load, docker metrics source and restart findings
# ---------------------------------------------------------------------------
assert_eq "load with batches and requests passes" "" "$(load_findings 12 36)"
assert_eq "zero batches fails" "FAIL${T}Load phase sent zero request batches" "$(load_findings 0 36)"
assert_eq "zero HTTP requests fails (not a warning)" "FAIL${T}No HTTP requests were recorded" "$(load_findings 12 0)"
assert_eq "zero batches and zero requests report both" \
  "FAIL${T}Load phase sent zero request batches
FAIL${T}No HTTP requests were recorded" "$(load_findings 0 0)"
assert_eq "malformed load counters fail" \
  "FAIL${T}Load evidence is malformed (batches='x', requests='')" "$(load_findings x '')"

record='{"container":"api","cpu":"0.5%","mem_usage":"40MiB / 512MiB","mem_pct":"1.00%","ts":"1"}'
assert_eq "docker stats records pass" "" "$(docker_collection_findings warm-up 0 "${record}")"
assert_eq "failing docker stats fails even with partial output" \
  "FAIL${T}docker stats failed (exit 1) during the warm-up collection" \
  "$(docker_collection_findings warm-up 1 "${record}")"
assert_eq "unavailable docker (exit 127) fails" \
  "FAIL${T}docker stats failed (exit 127) during the final collection" "$(docker_collection_findings final 127 '')"
assert_eq "empty docker stats output fails" \
  "FAIL${T}docker stats returned no container records during the final collection" \
  "$(docker_collection_findings final 0 '')"
assert_eq "non-record docker output fails" \
  "FAIL${T}docker stats returned no container records during the final collection" \
  "$(docker_collection_findings final 0 'Cannot connect to the Docker daemon')"

assert_eq "unchanged restart counts pass" "" "$(restart_findings $'a:0\nb:1' $'a:0\nb:1')"
assert_eq "a restart count increase fails" "FAIL${T}Container b restarted during soak (0 -> 1)" \
  "$(restart_findings $'a:0\nb:0' $'a:0\nb:1')"
assert_eq "a container absent at the end fails" \
  "FAIL${T}Container b was running at the start of the soak but is absent at the end" \
  "$(restart_findings $'a:0\nb:0' 'a:0')"
assert_eq "an empty restart baseline fails as not evaluated" \
  "FAIL${T}Restart check not evaluated: no containers were listed at the start of the soak" \
  "$(restart_findings '' 'a:0')"
assert_eq "a new healthy container is not a restart" "" "$(restart_findings 'a:0' $'a:0\nc:0')"
assert_eq "a malformed restart count fails" \
  "FAIL${T}Container a reported a malformed restart count 'x'" "$(restart_findings 'a:0' 'a:x')"

# ---------------------------------------------------------------------------
# soak_test.sh wiring and behavior (stubbed curl/docker, zero-minute load)
# ---------------------------------------------------------------------------
assert_eq "soak script loads analysis helpers" "1" \
  "$(grep -cF 'source "${SCRIPT_DIR}/soak_test_lib.sh"' "${SOAK}" || true)"
assert_eq "soak script records the intentional 400 expectation" "1" \
  "$(grep -cF 'record_status "POST:/jobs:invalid-empty-prompt" "${status}" 400' "${SOAK}" || true)"
assert_eq "soak error rate uses semantic response classification" "1" \
  "$(grep -cF 'count_unexpected_http_responses "${HTTP_LOG}"' "${SOAK}" || true)"
assert_eq "soak log ranking avoids an early-closing head pipeline" "1" \
  "$(grep -cF 'top_repeated_log_lines' "${SOAK}" || true)"
assert_eq "soak drift check uses the windowed-median analysis" "1" \
  "$(grep -cF 'memory_drift_analysis "${METRICS_FILE}"' "${SOAK}" || true)"
assert_eq "soak warm-up is normalised to base 10 (0300 is not octal 192)" "1" \
  "$(grep -cF 'WARMUP_SECONDS=$((10#${WARMUP_SECONDS}))' "${SOAK}" || true)"
assert_eq "docker stats failures are not swallowed with || true" "0" \
  "$(sed -n '/^collect_docker_metrics()/,/^}/p' "${SOAK}" | grep -c '|| true' || true)"
assert_eq "the Helm wrapper declares that Kubernetes has no docker stats" "1" \
  "$(grep -cF 'export SOAK_METRICS_SOURCE="${SOAK_METRICS_SOURCE:-none}"' "${SCRIPT_DIR}/soak_test_helm.sh" || true)"

stubs="${tmpdir}/stubs"
mkdir -p "${stubs}"
printf '#!%s\nprintf 200\n' "${BASH}" >"${stubs}/curl"
cat >"${stubs}/docker" <<EOF
#!${BASH}
case "\$1" in
  stats)
    case "\${STUB_DOCKER:-ok}" in
      ok) printf '%s\n' '${record}' ;;
      fail) exit 1 ;;
    esac ;;
  compose) if [[ "\$2" == ps ]]; then echo "api-1 Up 5 seconds"; fi ;;
esac
EOF
chmod +x "${stubs}/curl" "${stubs}/docker"

# run_soak ENV...: run soak_test.sh with SOAK_DURATION_MINUTES=0, so the load
# phase sends no batches and the analysis phase runs immediately.
run_soak() {
  rm -f "${tmpdir}"/soak.*
  (cd "${tmpdir}" && env CORDUM_API_KEY=k SOAK_DURATION_MINUTES=0 SOAK_HTTP_LOG="${tmpdir}/soak.http" \
    SOAK_METRICS_FILE="${tmpdir}/soak.metrics" SOAK_RESULTS_FILE="${tmpdir}/soak.json" \
    PATH="${stubs}:${PATH}" "$@" "${BASH}" "${SOAK}" 2>&1) || true
}
count_in() { grep -cF -- "$2" "$1" || true; }

assert_eq "an unknown SOAK_METRICS_SOURCE is rejected" \
  "[soak] ERROR: SOAK_METRICS_SOURCE must be docker or none (got 'bogus')" \
  "$(run_soak SOAK_METRICS_SOURCE=bogus | grep -F 'ERROR:')"
assert_eq "a window below the minimum of 3 is rejected" \
  "[soak] ERROR: SOAK_MEMORY_WINDOW_SAMPLES must be an integer of at least 3 (got '2')" \
  "$(run_soak SOAK_MEMORY_WINDOW_SAMPLES=2 | grep -F 'ERROR:')"
assert_eq "a non-numeric window is rejected" \
  "[soak] ERROR: SOAK_MEMORY_WINDOW_SAMPLES must be an integer of at least 3 (got 'x')" \
  "$(run_soak SOAK_MEMORY_WINDOW_SAMPLES=x | grep -F 'ERROR:')"
assert_eq "window 08 is normalised to base 10 (it is not octal)" "0" \
  "$(run_soak SOAK_MEMORY_WINDOW_SAMPLES=08 SOAK_METRICS_SOURCE=none | grep -c 'ERROR: SOAK_MEMORY_WINDOW_SAMPLES' || true)"

nodocker="${tmpdir}/nodocker"
mkdir -p "${nodocker}"
for tool in dirname date; do
  printf '#!%s\nexec %q "$@"\n' "${BASH}" "$(command -v "${tool}")" >"${nodocker}/${tool}"
done
cp "${stubs}/curl" "${nodocker}/curl"
chmod +x "${nodocker}"/*
assert_eq "docker is required up front when it is the declared metrics source" \
  "[soak] missing dependency: docker" "$(run_soak PATH="${nodocker}" | grep -F 'missing dependency')"
assert_eq "docker is not required when the source is none" "0" \
  "$(run_soak PATH="${nodocker}" SOAK_METRICS_SOURCE=none | grep -c 'missing dependency: docker' || true)"

run_soak SOAK_METRICS_SOURCE=none >/dev/null
assert_eq "source none: zero batches fail and drift is reported as not evaluated" \
  "1 1 1 1 1" "$(count_in "${tmpdir}/soak.json" 'Load phase sent zero request batches') $(count_in "${tmpdir}/soak.json" 'No HTTP requests were recorded') $(count_in "${tmpdir}/soak.json" '"memory_drift_evaluated": false') $(count_in "${tmpdir}/soak.json" '"memory_baseline_source": "none"') $(count_in "${tmpdir}/soak.json" 'Memory drift and restart checks not evaluated (SOAK_METRICS_SOURCE=none)')"
assert_eq "source none: the run does not pass" "1" "$(count_in "${tmpdir}/soak.json" '"pass": 0')"
run_soak >/dev/null
assert_eq "docker source with no warm-up capture fails as not evaluated" "1 1 1" \
  "$(count_in "${tmpdir}/soak.json" 'Memory drift not evaluated: no drift-baseline marker') $(count_in "${tmpdir}/soak.json" '"memory_drift_evaluated": false') $(count_in "${tmpdir}/soak.json" '"memory_window_samples": 5')"
assert_eq "docker source appends the final snapshot to the metrics file" "1 1" \
  "$(count_in "${tmpdir}/soak.metrics" '# snapshot') $(count_in "${tmpdir}/soak.metrics" '# baseline')"
run_soak STUB_DOCKER=fail >/dev/null
assert_eq "a failing docker stats fails the run at the start and at the end" "1 1" \
  "$(count_in "${tmpdir}/soak.json" 'docker stats failed (exit 1) during the pre-load baseline collection') $(count_in "${tmpdir}/soak.json" 'docker stats failed (exit 1) during the final collection')"
run_soak STUB_DOCKER=empty >/dev/null
assert_eq "an empty docker stats fails the run at the end" "1" \
  "$(count_in "${tmpdir}/soak.json" 'docker stats returned no container records during the final collection')"

echo "SUMMARY: ${PASS} pass, ${FAIL} fail"
[[ "${FAIL}" -eq 0 ]]

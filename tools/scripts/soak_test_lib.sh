#!/usr/bin/env bash

# Count responses that violate their declared expectation. Lines without an
# expectation retain the soak test's default contract: every 4xx/5xx response
# is an error. Malformed curl status output is always unexpected.
count_unexpected_http_responses() {
  local http_log="$1"
  awk '
    {
      status = $3
      expected = ""
      if ($4 ~ /^expected=[0-9][0-9][0-9]$/) {
        expected = substr($4, 10)
      }
      if (status !~ /^[0-9][0-9][0-9]$/) {
        unexpected++
      } else if (expected != "") {
        if (status != expected) unexpected++
      } else if (status >= 400) {
        unexpected++
      }
    }
    END { print unexpected + 0 }
  ' "${http_log}"
}

# Emit endpoints whose unexpected 4xx responses should participate in retry-
# storm detection. Expected negative probes are deliberately excluded.
unexpected_client_error_endpoints() {
  local http_log="$1"
  awk '
    {
      status = $3
      expected = ""
      if ($4 ~ /^expected=[0-9][0-9][0-9]$/) {
        expected = substr($4, 10)
      }
      if (status ~ /^[0-9][0-9][0-9]$/ && status >= 400 && status < 500 &&
          !(expected != "" && status == expected)) {
        print $2
      }
    }
  ' "${http_log}"
}

# Rank repeated Compose log messages without an early-closing `head` stage.
# Under `set -o pipefail`, head caused upstream sort to receive SIGPIPE and
# abort the entire soak analysis before it could write its JSON result.
top_repeated_log_lines() {
  sed -E 's/^[^[:space:]]+[[:space:]]+\|[[:space:]]*//' |
    LC_ALL=C sort |
    uniq -c |
    sort -rn |
    sed -n '1,5p'
}

# ---------------------------------------------------------------------------
# Memory drift analysis
# ---------------------------------------------------------------------------
# All arithmetic below is floating point via awk. docker stats reports
# percentages such as 1.95% and 2.07%; truncating those to integers before
# dividing turned a 6% change into a reported "100% growth" and failed every
# nightly soak. Only POSIX awk features are used so mawk (Ubuntu runners) and
# gawk behave identically.

# Extract one string field from a single-line docker stats JSON record.
docker_metric_field() {
  local line="$1" field="$2"
  printf '%s\n' "${line}" | grep -o "\"${field}\":\"[^\"]*\"" | head -1 | cut -d'"' -f4 || true
}

# Convert a docker stats human-readable size ("18.55MiB", "1.5GiB", "512kB",
# "900B") into MiB with two decimals. Unparseable input prints nothing so
# callers can fall back to percentage-only analysis.
docker_size_to_mib() {
  awk -v raw="${1:-}" '
    BEGIN {
      gsub(/^[[:space:]]+|[[:space:]]+$/, "", raw)
      if (raw !~ /^[0-9]+(\.[0-9]+)?[[:space:]]*[A-Za-z]*$/) { exit 0 }
      num = raw; sub(/[^0-9.].*$/, "", num)
      unit = raw; sub(/^[0-9.]+[[:space:]]*/, "", unit)
      unit = tolower(unit)
      if (unit == "" || unit == "b") mult = 1
      else if (unit == "kib") mult = 1024
      else if (unit == "mib") mult = 1024 * 1024
      else if (unit == "gib") mult = 1024 * 1024 * 1024
      else if (unit == "tib") mult = 1024 * 1024 * 1024 * 1024
      else if (unit == "kb") mult = 1000
      else if (unit == "mb") mult = 1000 * 1000
      else if (unit == "gb") mult = 1000 * 1000 * 1000
      else if (unit == "tb") mult = 1000 * 1000 * 1000 * 1000
      else exit 0
      printf "%.2f\n", (num * mult) / (1024 * 1024)
    }'
}

# Relative growth from $1 to $2 in percent, one decimal. A non-positive
# baseline yields 0.0 rather than a division error.
memory_growth_pct() {
  awk -v base="${1:-0}" -v final="${2:-0}" '
    BEGIN {
      base += 0; final += 0
      if (base <= 0) { print "0.0"; exit }
      printf "%.1f\n", (final - base) * 100 / base
    }'
}

# Decimal-safe comparison: succeeds when $1 > $2.
num_gt() {
  awk -v a="${1:-0}" -v b="${2:-0}" 'BEGIN { exit !((a + 0) > (b + 0)) }'
}

# Decimal-safe subtraction: prints $1 - $2 with two decimals.
num_sub() {
  awk -v a="${1:-0}" -v b="${2:-0}" 'BEGIN { printf "%.2f\n", (a + 0) - (b + 0) }'
}

# Memory baselines keyed by container name, populated by load_memory_baseline
# from a docker stats snapshot (one JSON record per line, "#" comments ignored).
declare -A BASELINE_MEM
declare -A BASELINE_MEM_MIB

load_memory_baseline() {
  local metrics="$1" line container mem_pct mem_usage
  BASELINE_MEM=()
  BASELINE_MEM_MIB=()
  while IFS= read -r line; do
    if [[ -z "${line}" || "${line}" == "#"* ]]; then continue; fi
    container=$(docker_metric_field "${line}" container)
    mem_pct=$(docker_metric_field "${line}" mem_pct | tr -d '%')
    mem_usage=$(docker_metric_field "${line}" mem_usage | cut -d'/' -f1)
    if [[ -n "${container}" && -n "${mem_pct}" ]]; then
      BASELINE_MEM["${container}"]="${mem_pct}"
      BASELINE_MEM_MIB["${container}"]="$(docker_size_to_mib "${mem_usage}")"
    fi
  done <<< "${metrics}"
}

# Compare a final docker stats snapshot against the loaded baseline.
#   $1  final metrics text
#   $2  relative growth limit in percent
#   $3  absolute growth floor in MiB
#   $4  human label for the baseline (for messages)
# Prints one "FAIL<TAB>message" line per container whose growth exceeds both
# the relative limit and the absolute floor, and one "WARN<TAB>message" line
# when only the relative limit is exceeded. A Go service that starts idle at
# ~15MiB legitimately doubles its heap under load; that is warm-up, not a
# leak, so relative growth alone must not fail the soak.
memory_drift_findings() {
  local metrics="$1" limit_pct="$2" min_mib="$3" source_label="${4:-baseline}"
  local line container mem_pct mem_usage baseline drift baseline_mib final_mib delta_mib detail
  while IFS= read -r line; do
    if [[ -z "${line}" || "${line}" == "#"* ]]; then continue; fi
    container=$(docker_metric_field "${line}" container)
    mem_pct=$(docker_metric_field "${line}" mem_pct | tr -d '%')
    mem_usage=$(docker_metric_field "${line}" mem_usage | cut -d'/' -f1)
    if [[ -z "${container}" || -z "${mem_pct}" ]]; then continue; fi
    baseline="${BASELINE_MEM[${container}]:-}"
    if [[ -z "${baseline}" ]] || ! num_gt "${baseline}" 0; then continue; fi
    drift=$(memory_growth_pct "${baseline}" "${mem_pct}")
    if ! num_gt "${drift}" "${limit_pct}"; then continue; fi
    detail="${baseline}% -> ${mem_pct}% (${drift}% growth vs ${source_label} baseline, limit ${limit_pct}%"
    baseline_mib="${BASELINE_MEM_MIB[${container}]:-}"
    final_mib=$(docker_size_to_mib "${mem_usage}")
    if [[ -n "${baseline_mib}" && -n "${final_mib}" ]]; then
      delta_mib=$(num_sub "${final_mib}" "${baseline_mib}")
      detail="${detail}; ${baseline_mib}MiB -> ${final_mib}MiB, +${delta_mib}MiB, floor ${min_mib}MiB)"
      if ! num_gt "${delta_mib}" "${min_mib}"; then
        printf 'WARN\t%s\n' "Memory growth on ${container} stayed under the absolute floor: ${detail}"
        continue
      fi
    else
      detail="${detail})"
    fi
    printf 'FAIL\t%s\n' "Memory drift on ${container}: ${detail}"
  done <<< "${metrics}"
}

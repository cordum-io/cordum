#!/usr/bin/env bash

# Count expectation violations; without an expectation, every 4xx/5xx is an
# error. Malformed curl status output is always unexpected.
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

# Retry-storm endpoints: unexpected 4xx only, excluding expected negative probes.
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

# Rank repeated Compose messages without `head`: its SIGPIPE under pipefail
# aborted analysis before the JSON result could be written.
top_repeated_log_lines() {
  sed -E 's/^[^[:space:]]+[[:space:]]+\|[[:space:]]*//' |
    LC_ALL=C sort |
    uniq -c |
    sort -rn |
    sed -n '1,5p'
}

# Memory drift analysis
# Host-memory percentages and cold heaps distort growth. Compare MiB medians
# over the first/last K non-overlapping post-warm-up snapshots; fail closed.
# POSIX awk (no extensions or {n} intervals) and LC_ALL=C keep mawk/gawk parity.

# size_mib("1.5GiB"): the size in MiB, or -1 when it cannot be parsed. Numbers
# are limited to 15 digits so the arithmetic cannot overflow a double.
_SOAK_AWK_SIZE='
function size_mib(raw,   num, unit, mult) {
  gsub(/^[[:space:]]+|[[:space:]]+$/, "", raw)
  if (raw !~ /^[0-9]+(\.[0-9]+)?[[:space:]]*[A-Za-z]*$/) return -1
  num = raw; sub(/[^0-9.].*$/, "", num)
  unit = raw; sub(/^[0-9.]+[[:space:]]*/, "", unit)
  if (length(num) > 15) return -1
  unit = tolower(unit)
  if (unit == "" || unit == "b") mult = 1
  else if (unit == "kib") mult = 1024
  else if (unit == "mib") mult = 1048576
  else if (unit == "gib") mult = 1073741824
  else if (unit == "tib") mult = 1099511627776
  else if (unit == "kb") mult = 1000
  else if (unit == "mb") mult = 1000000
  else if (unit == "gb") mult = 1000000000
  else if (unit == "tb") mult = 1000000000000
  else return -1
  return (num * mult) / 1048576
}'

# Prefix every stdin line with "TAG<TAB>" so two texts can share one awk input.
_tag() { LC_ALL=C awk -v t="$1" '{ print t "\t" $0 }'; }

# Parse snapshots >= START_TS into tab-separated S/block/container/mib/pct,
# N/valid-snapshot-count, and X/malformed-count/first-error records.
# A snapshot needs a valid record; drift-baseline is validated, not sampled;
# cold baseline is ignored. BINMODE=3 preserves CR on Git Bash gawk so CRLF
# fails visibly (other awks treat BINMODE as an ordinary variable).
_memory_samples() {
  LC_ALL=C awk -v BINMODE=3 -v start="$2" "${_SOAK_AWK_SIZE}"'
    function bad(msg) { nbad++; if (nbad == 1) first = "line " NR ": " msg }
    function fld(s, name,   re) {
      re = "\"" name "\":\"[^\"]*\""
      if (!match(s, re)) return ""
      return substr(s, RSTART + length(name) + 4, RLENGTH - length(name) - 5)
    }
    BEGIN { start += 0 }
    index($0, "\r") { bad("CRLF line ending, metrics files must use LF"); active = 0; next }
    $0 == "" { next }
    /^# (snapshot|drift-baseline)( |$)/ {
      if ($0 !~ /^# (snapshot|drift-baseline) [0-9]+$/) { bad("malformed block header"); active = 0; next }
      split($0, h, " ")
      active = (h[3] + 0 >= start); counted = (h[2] == "snapshot"); seen = 0
      next
    }
    /^#/ { active = 0; next }
    !active { next }
    {
      c = fld($0, "container"); u = fld($0, "mem_usage"); p = fld($0, "mem_pct")
      sub(/[[:space:]]*\/.*$/, "", u); m = size_mib(u); sub(/%$/, "", p)
      if (substr($0, 1, 1) != "{" || substr($0, length($0), 1) != "}") bad("not a JSON object")
      else if (c !~ /^[A-Za-z0-9][A-Za-z0-9_.-]*$/) bad("missing or invalid container name")
      else if (m < 0) bad("unparseable mem_usage")
      else if (p !~ /^[0-9]+(\.[0-9]+)?$/) bad("unparseable mem_pct")
      else if (counted) {
        if (!seen) { seen = 1; blocks++ }
        printf "S\t%d\t%s\t%.4f\t%s\n", blocks, c, m, p
      }
    }
    END { printf "N\t%d\n", blocks + 0; if (nbad) printf "X\t%d\t%s\n", nbad, first }
  ' "$1"
}

# First/last K valid snapshots >= START_TS, without overlap. Output columns:
# container<TAB>median_mib<TAB>median_pct<TAB>samples<TAB>window_snapshots.
# Median = middle value (odd n) or mean of middle pair (even n). samples counts
# container records; last window_snapshots < K exposes runs shorter than 2K.
memory_window_medians() {
  local file="$1" start="$2" k="$3" which="$4"
  _memory_samples "${file}" "${start}" | LC_ALL=C awk -F'\t' -v k="${k}" -v which="${which}" '
    function med(n,   i, j, t) {
      for (i = 2; i <= n; i++) {
        t = v[i]
        for (j = i - 1; j >= 1 && v[j] > t; j--) v[j + 1] = v[j]
        v[j + 1] = t
      }
      return (n % 2) ? v[(n + 1) / 2] : (v[n / 2] + v[n / 2 + 1]) / 2
    }
    $1 == "S" {
      if (!($3 in known)) { known[$3] = 1; order[++nc] = $3 }
      mib[$3, $2] = $4 + 0; pct[$3, $2] = $5 + 0
    }
    $1 == "N" { s = $2 + 0 }
    END {
      k += 0
      if (which == "first") { lo = 1; hi = (s < k ? s : k) }
      else { lo = (s > 2 * k ? s - k + 1 : k + 1); hi = s }
      w = hi - lo + 1; if (w < 0) w = 0
      for (i = 1; i <= nc; i++) {
        c = order[i]; n = 0
        for (b = lo; b <= hi; b++) if ((c, b) in mib) v[++n] = mib[c, b]
        if (n == 0) { printf "%s\t-\t-\t0\t%d\n", c, w; continue }
        m = med(n); n = 0
        for (b = lo; b <= hi; b++) if ((c, b) in pct) v[++n] = pct[c, b]
        printf "%s\t%.2f\t%.2f\t%d\t%d\n", c, m, med(n), n, w
      }
    }'
}

# Compare memory_window_medians outputs: FAIL above BOTH limits, WARN for
# relative-only growth (small heaps can double during warm-up). FAIL if either
# window is short, a container has no baseline, disappears or misses samples.
memory_window_findings() {
  local base="$1" final="$2" limit="$3" floor="$4" k="$5"
  { printf '%s\n' "${base}" | _tag B; printf '%s\n' "${final}" | _tag F; } |
    LC_ALL=C awk -F'\t' -v limit="${limit}" -v floor="${floor}" -v k="${k}" '
      function fail(msg) { printf "FAIL\t%s\n", msg }
      $2 == "" { next }
      {
        if (!($2 in known)) { known[$2] = 1; order[++nc] = $2 }
        mib[$1, $2] = $3 + 0; n[$1, $2] = $5 + 0; w[$1] = $6 + 0
      }
      END {
        ne = "Memory drift not evaluated"
        if (nc == 0) { fail(ne ": no valid post-warm-up container samples"); exit }
        if (w["B"] < k + 0 || w["F"] < k + 0) {
          fail(ne ": " w["B"] + w["F"] " valid post-warm-up snapshots, need " 2 * k " (2 x " k ") for non-overlapping baseline and final windows")
          exit
        }
        for (i = 1; i <= nc; i++) {
          c = order[i]; nb = n["B", c] + 0; nf = n["F", c] + 0
          if (nb == 0 && nf == 0) continue
          if (nb == 0) { fail(ne " for " c ": first sampled after the baseline window, so it has no baseline"); continue }
          if (nf == 0) { fail(ne " for " c ": present in the baseline window but absent from the final window"); continue }
          if (nb < k + 0 || nf < k + 0) { fail(ne " for " c ": " nb " baseline-window and " nf " final-window samples, need " k " in each"); continue }
          b = mib["B", c]; f = mib["F", c]; d = f - b
          if (b > 0) { g = d * 100 / b; if (!(g > limit + 0)) continue; gs = sprintf("%.1f", g) }
          else { if (!(f > 0)) continue; gs = "inf" }
          detail = sprintf("median %.2fMiB -> %.2fMiB (+%.2fMiB, %s%% growth over %s-snapshot windows, limit %s%% and %sMiB)", b, f, d, gs, k, limit, floor)
          if (d > floor + 0) printf "FAIL\tMemory drift on %s: %s\n", c, detail
          else printf "WARN\tMemory growth on %s stayed under the absolute floor: %s\n", c, detail
        }
      }'
}

# Warm-up drift-baseline timestamp, or nothing if the capture never succeeded.
memory_drift_baseline_ts() {
  LC_ALL=C awk -v BINMODE=3 '/^# drift-baseline [0-9]+\r?$/ { print $3 + 0; exit }' "$1"
}

# Args: metrics file, relative limit %, absolute floor MiB, window K.
# Emit FAIL/WARN lines; missing marker, malformed records or short windows
# FAIL as "Memory drift not evaluated". Never fall back to the cold sample.
memory_drift_analysis() {
  local file="$1" limit="$2" floor="$3" k="$4" start base final
  if [[ ! "${k}" =~ ^[0-9]{1,6}$ ]] || [[ $((10#${k})) -lt 3 ]]; then
    printf 'FAIL\t%s\n' "Memory drift not evaluated: the window size must be an integer of at least 3 (got '${k}')"
    return 0
  fi
  k=$((10#${k}))
  start=$(memory_drift_baseline_ts "${file}" 2>/dev/null || true)
  if [[ -z "${start}" ]]; then
    printf 'FAIL\t%s\n' "Memory drift not evaluated: no drift-baseline marker, so the post-warm-up baseline was never captured"
    return 0
  fi
  _memory_samples "${file}" "${start}" | LC_ALL=C awk -F'\t' \
    '$1 == "X" { printf "FAIL\tMemory drift not evaluated: %d malformed metric line(s) (first: %s)\n", $2, $3 }'
  base=$(memory_window_medians "${file}" "${start}" "${k}" first)
  final=$(memory_window_medians "${file}" "${start}" "${k}" last)
  memory_window_findings "${base}" "${final}" "${limit}" "${floor}" "${k}"
}

# Load, metrics source and restart checks

# FAIL on zero batches/requests or non-integer load counters.
load_findings() {
  local batches="${1:-}" requests="${2:-}"
  batches="${batches//[[:space:]]/}"
  requests="${requests//[[:space:]]/}"
  if [[ ! "${batches}" =~ ^[0-9]+$ || ! "${requests}" =~ ^[0-9]+$ ]]; then
    printf 'FAIL\t%s\n' "Load evidence is malformed (batches='${batches}', requests='${requests}')"
    return 0
  fi
  if [[ $((10#${batches})) -eq 0 ]]; then printf 'FAIL\t%s\n' "Load phase sent zero request batches"; fi
  if [[ $((10#${requests})) -eq 0 ]]; then printf 'FAIL\t%s\n' "No HTTP requests were recorded"; fi
}

# Docker source: FAIL on nonzero exit or no records (args: label, status, stdout).
docker_collection_findings() {
  local label="$1" rc="$2" text="$3"
  if [[ "${rc}" != 0 ]]; then
    printf 'FAIL\t%s\n' "docker stats failed (exit ${rc}) during the ${label} collection"
  elif ! grep -q '^{"container":' <<<"${text}"; then
    printf 'FAIL\t%s\n' "docker stats returned no container records during the ${label} collection"
  fi
}

# Compare start/end name:restart-count lists: FAIL on growth, disappearance,
# malformed counts or an empty start list (a vacuous check).
restart_findings() {
  local base="$1" final="$2"
  { printf '%s\n' "${base}" | _tag B; printf '%s\n' "${final}" | _tag F; } |
    LC_ALL=C awk -F'[\t:]' -v q="'" '
      function fail(msg) { printf "FAIL\t%s\n", msg }
      $2 == "" { next }
      {
        cnt[$1, $2] = $3
        if ($1 == "B") border[++nb] = $2; else forder[++nf] = $2
      }
      END {
        if (nb == 0) { fail("Restart check not evaluated: no containers were listed at the start of the soak"); exit }
        for (i = 1; i <= nf; i++) {
          name = forder[i]; f = cnt["F", name]
          if (f !~ /^[0-9]+$/) { fail("Container " name " reported a malformed restart count " q f q); continue }
          b = (("B", name) in cnt) ? cnt["B", name] + 0 : 0
          if (f + 0 > b) fail("Container " name " restarted during soak (" b " -> " f ")")
        }
        for (i = 1; i <= nb; i++)
          if (!(("F", border[i]) in cnt)) fail("Container " border[i] " was running at the start of the soak but is absent at the end")
      }'
}

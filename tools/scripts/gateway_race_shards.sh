#!/usr/bin/env bash
set -euo pipefail

PACKAGE="${PACKAGE:-./core/controlplane/gateway}"
COUNT="${COUNT:-3}"
TIMEOUT="${TIMEOUT:-15m}"
TOTAL_SHARDS="${TOTAL_SHARDS:-4}"
LOG_DIR="${LOG_DIR:-/tmp/cordum-gateway-race-shards-$(date +%Y%m%d-%H%M%S)}"
FOCUSED_BINARY_TEST="TestBinaryIntegrity_MaxBytesErrorTypedCheck"

MODE=""
DRY_RUN=0
SHARD_INDEX=""
SHARD_TOTAL=""
ALL_TESTS=()

usage() {
  cat <<'EOF'
Usage: tools/scripts/gateway_race_shards.sh [options]
Options:
  --all                       Run all deterministic shards (default mode).
  --shard N/M                 Run shard N of M, using sorted Test* names.
  --focused-binary-integrity  Run TestBinaryIntegrity_MaxBytesErrorTypedCheck.
  --list-tests                Print the sorted Test* names and exit.
  --dry-run                   Print selected tests/regexes without go test.
  -h, --help                  Show this help.

Environment overrides:
  PACKAGE        Go package to test (default ./core/controlplane/gateway)
  COUNT          go test -count value (default 3)
  TIMEOUT        go test -timeout value (default 15m)
  TOTAL_SHARDS   shard count for --all (default 4)
  LOG_DIR        output directory for *.jsonl and *.time.txt logs
EOF
}

die() {
  echo "ERROR: $*" >&2
  exit 2
}

set_mode() {
  local next_mode="$1"
  if [[ -n "${MODE}" && "${MODE}" != "${next_mode}" ]]; then
    die "choose exactly one run mode; got ${MODE} and ${next_mode}"
  fi
  MODE="${next_mode}"
}

require_positive_int() {
  local name="$1"
  local value="$2"
  if [[ ! "${value}" =~ ^[1-9][0-9]*$ ]]; then
    die "${name} must be a positive integer; got '${value}'"
  fi
}

parse_shard() {
  local spec="$1"
  if [[ ! "${spec}" =~ ^([1-9][0-9]*)/([1-9][0-9]*)$ ]]; then
    die "invalid --shard '${spec}'; expected N/M with N and M >= 1"
  fi
  SHARD_INDEX="${BASH_REMATCH[1]}"
  SHARD_TOTAL="${BASH_REMATCH[2]}"
  if (( SHARD_INDEX > SHARD_TOTAL )); then
    die "invalid --shard '${spec}'; N must be <= M"
  fi
}

parse_args() {
  while [[ "$#" -gt 0 ]]; do
    case "$1" in
      --all)
        set_mode "all"
        ;;
      --shard)
        [[ "$#" -ge 2 ]] || die "--shard requires N/M"
        set_mode "shard"
        parse_shard "$2"
        shift
        ;;
      --focused-binary-integrity)
        set_mode "focused-binary-integrity"
        ;;
      --list-tests)
        set_mode "list-tests"
        ;;
      --dry-run)
        DRY_RUN=1
        ;;
      -h|--help)
        usage
        exit 0
        ;;
      *)
        die "unknown argument '$1'"
        ;;
    esac
    shift
  done

  [[ -n "${MODE}" ]] || MODE="all"
  require_positive_int "COUNT" "${COUNT}"
  require_positive_int "TOTAL_SHARDS" "${TOTAL_SHARDS}"
}

discover_tests() {
  local output
  if ! output="$(go test "${PACKAGE}" -list '^Test')"; then
    echo "ERROR: failed to discover tests for ${PACKAGE}" >&2
    return 1
  fi
  printf '%s\n' "${output}" | awk '/^Test/ {print $1}' | sort -u
}

load_tests() {
  local discovered
  discovered="$(discover_tests)" || exit 1
  mapfile -t ALL_TESTS <<<"${discovered}"
  if [[ "${#ALL_TESTS[@]}" -eq 1 && -z "${ALL_TESTS[0]}" ]]; then
    ALL_TESTS=()
  fi
  if [[ "${#ALL_TESTS[@]}" -eq 0 ]]; then
    die "no Test* entries discovered for ${PACKAGE}"
  fi
}

join_tests() {
  local IFS=' '
  printf '%s' "$*"
}

regex_escape() {
  printf '%s' "$1" | sed -e 's/[.[\*^$()+?{}|\\]/\\&/g'
}

build_regex() {
  local tests=("$@")
  local escaped=()
  local test
  [[ "${#tests[@]}" -gt 0 ]] || die "cannot build regex for empty test set"

  for test in "${tests[@]}"; do
    escaped+=("$(regex_escape "${test}")")
  done

  local IFS='|'
  printf '^(%s)$' "${escaped[*]}"
}

select_shard_tests() {
  local index="$1"
  local total="$2"
  local i
  for i in "${!ALL_TESTS[@]}"; do
    if (( (i % total) + 1 == index )); then
      printf '%s\n' "${ALL_TESTS[$i]}"
    fi
  done
}

parse_max_rss() {
  local time_log="$1"
  if [[ ! -f "${time_log}" ]]; then
    printf 'unknown'
    return
  fi
  awk -F: '
    /Maximum resident set size/ {
      gsub(/^[ \t]+/, "", $2)
      print $2
      found=1
    }
    END {
      if (!found) {
        print "unknown"
      }
    }
  ' "${time_log}"
}

has_gnu_time() {
  [[ -x /usr/bin/time ]] && /usr/bin/time -v true >/dev/null 2>&1
}

run_go_test() {
  local regex="$1"
  if has_gnu_time; then
    /usr/bin/time -v -o "${TIME_LOG}" \
      go test -race "${PACKAGE}" -run "${regex}" -count="${COUNT}" \
      -timeout="${TIMEOUT}" -json
  else
    : >"${TIME_LOG}"
    go test -race "${PACKAGE}" -run "${regex}" -count="${COUNT}" \
      -timeout="${TIMEOUT}" -json
  fi
}

run_group() {
  local label="$1"
  local safe_name="$2"
  shift 2
  local tests=("$@")
  local test_list
  test_list="$(join_tests "${tests[@]}")"

  if [[ "${#tests[@]}" -eq 0 ]]; then
    echo "SKIP ${label} tests=0"
    return 0
  fi

  local regex
  regex="$(build_regex "${tests[@]}")"
  if [[ "${DRY_RUN}" -eq 1 ]]; then
    echo "DRY-RUN ${label} tests=${#tests[@]} count=${COUNT} timeout=${TIMEOUT} regex=${regex} list=${test_list}"
    return 0
  fi

  mkdir -p "${LOG_DIR}"
  local log_path="${LOG_DIR}/${safe_name}.jsonl"
  TIME_LOG="${LOG_DIR}/${safe_name}.time.txt"

  echo "RUN ${label} tests=${#tests[@]} count=${COUNT} timeout=${TIMEOUT} regex=${regex} log=${log_path}"
  set +e
  run_go_test "${regex}" 2>&1 | tee "${log_path}"
  local rc=${PIPESTATUS[0]}
  set -e

  local max_rss
  max_rss="$(parse_max_rss "${TIME_LOG}")"
  if [[ "${rc}" -eq 0 ]]; then
    echo "PASS ${label} tests=${#tests[@]} exit=0 max_rss_kb=${max_rss} log=${log_path}"
  else
    echo "FAIL ${label} tests=${#tests[@]} exit=${rc} max_rss_kb=${max_rss} log=${log_path}" >&2
  fi
  return "${rc}"
}

run_shard() {
  local index="$1"
  local total="$2"
  local selected=()
  mapfile -t selected < <(select_shard_tests "${index}" "${total}")
  run_group "shard ${index}/${total}" "shard-${index}-of-${total}" "${selected[@]}"
}

run_focused_binary() {
  local test
  for test in "${ALL_TESTS[@]}"; do
    if [[ "${test}" == "${FOCUSED_BINARY_TEST}" ]]; then
      run_group "focused-binary-integrity" "focused-binary-integrity" "${test}"
      return
    fi
  done
  die "${FOCUSED_BINARY_TEST} was not discovered in ${PACKAGE}"
}

run_all() {
  local i
  local dry_labels=()
  for (( i = 1; i <= TOTAL_SHARDS; i++ )); do
    if run_shard "${i}" "${TOTAL_SHARDS}"; then
      dry_labels+=("DRY-RUN shard ${i}/${TOTAL_SHARDS}")
    else
      local rc=$?
      echo "FAIL first_failed_shard=${i}/${TOTAL_SHARDS} exit=${rc} log_dir=${LOG_DIR}" >&2
      return "${rc}"
    fi
  done

  if [[ "${DRY_RUN}" -eq 1 ]]; then
    echo "DRY-RUN all summary: $(join_tests "${dry_labels[@]}")"
  else
    echo "PASS all shards total=${TOTAL_SHARDS} log_dir=${LOG_DIR}"
  fi
}

main() {
  parse_args "$@"
  load_tests

  case "${MODE}" in
    list-tests)
      echo "TESTS: $(join_tests "${ALL_TESTS[@]}")"
      ;;
    shard)
      run_shard "${SHARD_INDEX}" "${SHARD_TOTAL}"
      ;;
    focused-binary-integrity)
      run_focused_binary
      ;;
    all)
      run_all
      ;;
    *)
      die "unhandled mode '${MODE}'"
      ;;
  esac
}

main "$@"

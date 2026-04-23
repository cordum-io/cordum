#!/usr/bin/env bash
# quickstart.sh env-sharing contract regression test.
#
# Default mode drives the real compose stack. Set
# CORDUM_QUICKSTART_ENV_SHARING_MODE=stub for the fast no-Docker unit harness
# used to exercise banner/divergence edge cases without starting containers.
set -euo pipefail

if [[ "${CORDUM_INTEGRATION:-}" != "1" ]]; then
  echo "skip: set CORDUM_INTEGRATION=1 to run quickstart env-sharing tests" >&2
  exit 0
fi

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
MODE="${CORDUM_QUICKSTART_ENV_SHARING_MODE:-live}"

fail() { echo "QUICKSTART ENV SHARING FAIL: $*" >&2; exit 1; }
missing() { echo "QUICKSTART ENV SHARING PREREQ MISSING: $*" >&2; exit 2; }
ok() { echo "  OK  $*"; }
need() { command -v "$1" >/dev/null 2>&1 || missing "required tool not found: $1"; }

run_stub_contract_test() {
  need bash
  need mktemp
  need grep

  local tmp
  tmp="$(mktemp -d)"
  QUICKSTART_ENV_TEST_TMP="${tmp}"
  trap 'rm -rf "${QUICKSTART_ENV_TEST_TMP:-}"' EXIT

  mkdir -p "${tmp}/tools/scripts" "${tmp}/certs/ca" "${tmp}/bin"
  cp "${REPO_ROOT}/tools/scripts/quickstart.sh" "${tmp}/tools/scripts/quickstart.sh"
  cp "${REPO_ROOT}/tools/scripts/platform_smoke.sh" "${tmp}/tools/scripts/platform_smoke.sh"
  chmod +x "${tmp}/tools/scripts/"*.sh
  printf 'services: {}\n' > "${tmp}/docker-compose.yml"
  printf 'test-ca\n' > "${tmp}/certs/ca/ca.crt"

  cat > "${tmp}/bin/docker" <<'STUB'
#!/usr/bin/env bash
set -euo pipefail
state_file=".stub-container-env"
case "${1:-}" in
  info)
    exit 0
    ;;
  compose)
    shift
    if [[ "${1:-}" == "version" ]]; then
      exit 0
    fi
    cmd=""
    while [[ "$#" -gt 0 ]]; do
      case "$1" in
        -f|--file|--profile)
          shift 2
          ;;
        *)
          cmd="$1"
          shift
          break
          ;;
      esac
    done
    case "${cmd}" in
      version)
        exit 0
        ;;
      up)
        cp .env "${state_file}"
        exit 0
        ;;
      down)
        rm -f "${state_file}"
        exit 0
        ;;
      ps)
        if [[ ! -f "${state_file}" ]]; then
          exit 0
        fi
        if [[ " $* " == *" -q "* ]]; then
          echo "stub-api-gateway-container"
        elif [[ " $* " == *" --services "* ]]; then
          echo "api-gateway"
        else
          echo "api-gateway"
        fi
        exit 0
        ;;
      exec)
        if [[ "${MSYS_NO_PATHCONV:-}" != "1" ]]; then
          echo "MSYS_NO_PATHCONV was not set for docker compose exec" >&2
          exit 97
        fi
        if [[ "${1:-}" == "-T" ]]; then
          shift
        fi
        service="${1:-}"
        subcmd="${2:-}"
        var="${3:-}"
        if [[ "${service}" != "api-gateway" || "${subcmd}" != "printenv" || -z "${var}" || ! -f "${state_file}" ]]; then
          exit 1
        fi
        grep -E "^${var}=" "${state_file}" | head -1 | cut -d= -f2- || true
        exit 0
        ;;
      logs)
        exit 0
        ;;
      *)
        exit 0
        ;;
    esac
    ;;
esac
exit 0
STUB
  chmod +x "${tmp}/bin/docker"

  cat > "${tmp}/bin/curl" <<'STUB'
#!/usr/bin/env bash
set -euo pipefail
if [[ "${1:-}" == "--version" ]]; then
  echo "curl 8.0.0 OpenSSL"
  exit 0
fi
args=" $* "
if [[ "${args}" == *"/api/v1/config"* && "${args}" == *"%{http_code}"* ]]; then
  printf '200'
  exit 0
fi
printf '{"nats":{"connected":true},"redis":{"ok":true}}\n'
STUB
  chmod +x "${tmp}/bin/curl"

  cat > "${tmp}/.env" <<'ENV'
CORDUM_API_KEY=seeded-api-key-for-env-sharing-test-000000000000
REDIS_PASSWORD=pa ss"word
ENV

  (
    cd "${tmp}"
    PATH="${tmp}/bin:${PATH}" \
      bash ./tools/scripts/quickstart.sh --skip-build --skip-smoke --skip-doctor --health-timeout 3 \
      > "${tmp}/quickstart.log" 2>&1
  )

  if ! grep -Fq "[quickstart] env.source key=CORDUM_API_KEY source=env-file file=.env" "${tmp}/quickstart.log"; then
    cat "${tmp}/quickstart.log" >&2
    fail "missing CORDUM_API_KEY env-file source banner"
  fi
  ok "seeded .env CORDUM_API_KEY source banner emitted"

  cat > "${tmp}/.env" <<'ENV'
CORDUM_API_KEY=changed-api-key-for-env-sharing-test-111111111111
REDIS_PASSWORD=pa ss"word
ENV

  set +e
  (
    cd "${tmp}"
    PATH="${tmp}/bin:${PATH}" \
      bash ./tools/scripts/quickstart.sh --skip-build --skip-smoke --skip-doctor --health-timeout 3 \
      > "${tmp}/quickstart-divergence.log" 2>&1
  )
  local rc=$?
  set -e
  if [[ "${rc}" -ne 2 ]]; then
    cat "${tmp}/quickstart-divergence.log" >&2
    fail "expected stale container/env divergence to exit 2, got ${rc}"
  fi
  if ! grep -Fq "[quickstart] env.divergence key=CORDUM_API_KEY container=api-gateway action=abort" "${tmp}/quickstart-divergence.log"; then
    cat "${tmp}/quickstart-divergence.log" >&2
    fail "missing CORDUM_API_KEY divergence log"
  fi
  if grep -Fq "changed-api-key-for-env-sharing-test" "${tmp}/quickstart-divergence.log"; then
    cat "${tmp}/quickstart-divergence.log" >&2
    fail "divergence log leaked the new API key value"
  fi
  ok "stale container/env divergence aborts without leaking secret values"

  (
    cd "${tmp}"
    PATH="${tmp}/bin:${PATH}" \
      bash ./tools/scripts/quickstart.sh --clean --skip-build --skip-smoke --skip-doctor --health-timeout 3 \
      > "${tmp}/quickstart-clean.log" 2>&1
  )
  ok "--clean tolerates divergence and refreshes stubbed container env"

  rm -f "${tmp}/.env" "${tmp}/.stub-container-env"
  (
    cd "${tmp}"
    PATH="${tmp}/bin:${PATH}" \
      CORDUM_API_KEY="shell-api-key-for-env-sharing-test-222222222222" \
      REDIS_PASSWORD='shell redis"password' \
      bash ./tools/scripts/quickstart.sh --skip-build --skip-smoke --skip-doctor --health-timeout 3 \
      > "${tmp}/quickstart-shell.log" 2>&1
  )
  if ! grep -Fxq "CORDUM_API_KEY=shell-api-key-for-env-sharing-test-222222222222" "${tmp}/.env"; then
    cat "${tmp}/quickstart-shell.log" >&2
    cat "${tmp}/.env" >&2 || true
    fail "shell-provided CORDUM_API_KEY was not persisted to .env"
  fi
  if ! grep -Fxq 'REDIS_PASSWORD=shell redis"password' "${tmp}/.env"; then
    cat "${tmp}/quickstart-shell.log" >&2
    cat "${tmp}/.env" >&2 || true
    fail "shell-provided REDIS_PASSWORD was not persisted to .env"
  fi
  ok "shell-provided secrets persist to .env for future compose runs"
  rm -rf "${tmp}"
  QUICKSTART_ENV_TEST_TMP=""
  trap - EXIT
}

run_live_contract_test() {
  need bash
  need curl
  need docker
  need jq
  need openssl

  cd "${REPO_ROOT}"

  local start_ts end_ts elapsed
  start_ts="$(date +%s)"

  LIVE_HAD_ENV=0
  LIVE_ENV_BACKUP=""
  if [[ -f .env ]]; then
    LIVE_HAD_ENV=1
    LIVE_ENV_BACKUP="$(mktemp)"
    cp .env "${LIVE_ENV_BACKUP}"
  fi

  cleanup() {
    docker compose down -v >/dev/null 2>&1 || true
    if [[ "${LIVE_HAD_ENV:-0}" == "1" ]]; then
      cp "${LIVE_ENV_BACKUP}" .env
      rm -f "${LIVE_ENV_BACKUP}"
    else
      rm -f .env
    fi
  }
  trap cleanup EXIT

  local seeded_key seeded_redis api_base status_json quickstart_log rc
  seeded_key="test-key-$(date +%s)-$(openssl rand -hex 16)"
  seeded_redis="test-redis-$(openssl rand -hex 16)"
  cat > .env <<ENV
CORDUM_API_KEY=${seeded_key}
REDIS_PASSWORD=${seeded_redis}
ENV

  docker compose down -v >/dev/null 2>&1 || true

  quickstart_log="$(mktemp)"
  set +e
  ./tools/scripts/quickstart.sh --skip-build --skip-smoke --skip-doctor --health-timeout 30 >"${quickstart_log}" 2>&1
  rc=$?
  set -e
  cat "${quickstart_log}"
  if [[ "${rc}" -ne 0 ]]; then
    fail "quickstart exited ${rc}"
  fi
  if grep -Fq "${seeded_key}" "${quickstart_log}" || grep -Fq "${seeded_redis}" "${quickstart_log}"; then
    fail "quickstart output leaked a full seeded secret value"
  fi

  local curl_tls=()
  if [[ -f ./certs/ca/ca.crt ]]; then
    curl_tls=(--cacert ./certs/ca/ca.crt)
    api_base="${CORDUM_API_BASE:-https://localhost:8081}"
  else
    api_base="${CORDUM_API_BASE:-http://localhost:8081}"
  fi

  status_json="$(curl -fsS "${curl_tls[@]}" -H "X-API-Key: ${seeded_key}" -H "X-Tenant-ID: default" "${api_base}/api/v1/status")"
  echo "${status_json}" | jq -e '.nats.connected == true and .redis.ok == true' >/dev/null \
    || fail "status endpoint did not report nats.connected=true and redis.ok=true"
  ok "status endpoint accepted seeded CORDUM_API_KEY"

  local key_count key_value
  key_count="$(grep -c '^CORDUM_API_KEY=' .env || true)"
  if [[ "${key_count}" != "1" ]]; then
    fail ".env contains ${key_count} CORDUM_API_KEY entries, expected 1"
  fi
  key_value="$(grep '^CORDUM_API_KEY=' .env | cut -d= -f2-)"
  if [[ "${key_value}" != "${seeded_key}" ]]; then
    fail ".env CORDUM_API_KEY changed during quickstart"
  fi
  ok ".env retained exactly one seeded CORDUM_API_KEY"

  end_ts="$(date +%s)"
  elapsed=$((end_ts - start_ts))
  if (( elapsed > 90 )); then
    fail "quickstart env-sharing test exceeded 90s budget (${elapsed}s)"
  fi
  echo "PASS: quickstart env-sharing live e2e completed in ${elapsed}s"
}

case "${MODE}" in
  stub)
    run_stub_contract_test
    ;;
  live)
    run_live_contract_test
    ;;
  *)
    missing "unknown CORDUM_QUICKSTART_ENV_SHARING_MODE=${MODE}; use live or stub"
    ;;
esac

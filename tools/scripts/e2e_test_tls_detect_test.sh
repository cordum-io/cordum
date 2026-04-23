#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "${TMP_DIR}"' EXIT

mkdir -p "${TMP_DIR}/certs/ca" "${TMP_DIR}/certs/client"
printf 'test-ca\n' > "${TMP_DIR}/certs/ca/ca.crt"
printf 'test-cert\n' > "${TMP_DIR}/certs/client/tls.crt"
printf 'test-key\n' > "${TMP_DIR}/certs/client/tls.key"

set +e
output="$(
  cd "${TMP_DIR}" &&
  export REDIS_PASSWORD="test-password" &&
  CORDUM_E2E_SOURCE_ONLY=1 source "${ROOT}/tools/scripts/e2e_test.sh" &&
  detect_worker_bus_env &&
  printf 'BUS_SCHEME=%s\n' "${BUS_SCHEME}" &&
  printf 'REDIS_SCHEME=%s\n' "${REDIS_SCHEME}" &&
  printf 'HELLO_NATS_URL=%s\n' "${HELLO_NATS_URL}" &&
  printf 'HELLO_REDIS_URL=%s\n' "${HELLO_REDIS_URL}" &&
  printf 'NATS_TLS_CA=%s\n' "${NATS_TLS_CA}" &&
  printf 'NATS_TLS_CERT=%s\n' "${NATS_TLS_CERT}" &&
  printf 'NATS_TLS_KEY=%s\n' "${NATS_TLS_KEY}" &&
  printf 'REDIS_TLS_CA=%s\n' "${REDIS_TLS_CA}" &&
  printf 'REDIS_TLS_CERT=%s\n' "${REDIS_TLS_CERT}" &&
  printf 'REDIS_TLS_KEY=%s\n' "${REDIS_TLS_KEY}" &&
  if printf '%s\n' '{"items":[{"worker_id":"hello-worker","pool":"hello-pack"}]}' | workers_json_has_pool "hello-pack"; then
    printf 'WORKERS_ITEMS_HAS_HELLO=yes\n'
  else
    printf 'WORKERS_ITEMS_HAS_HELLO=no\n'
  fi &&
  if printf '%s\n' '[{"worker_id":"hello-worker","pool":"hello-pack"}]' | workers_json_has_pool "hello-pack"; then
    printf 'WORKERS_ARRAY_HAS_HELLO=yes\n'
  else
    printf 'WORKERS_ARRAY_HAS_HELLO=no\n'
  fi
)"
status=$?
set -e

if [[ ${status} -ne 0 ]]; then
  echo "e2e TLS detection source test failed with status ${status}" >&2
  printf '%s\n' "${output}" >&2
  exit "${status}"
fi

grep -qx 'BUS_SCHEME=tls' <<<"${output}"
grep -qx 'REDIS_SCHEME=rediss' <<<"${output}"
grep -qx 'HELLO_NATS_URL=tls://localhost:4222' <<<"${output}"
grep -qx 'HELLO_REDIS_URL=rediss://:test-password@localhost:6379/0' <<<"${output}"
grep -qx 'NATS_TLS_CA=./certs/ca/ca.crt' <<<"${output}"
grep -qx 'NATS_TLS_CERT=./certs/client/tls.crt' <<<"${output}"
grep -qx 'NATS_TLS_KEY=./certs/client/tls.key' <<<"${output}"
grep -qx 'REDIS_TLS_CA=./certs/ca/ca.crt' <<<"${output}"
grep -qx 'REDIS_TLS_CERT=./certs/client/tls.crt' <<<"${output}"
grep -qx 'REDIS_TLS_KEY=./certs/client/tls.key' <<<"${output}"
grep -qx 'WORKERS_ITEMS_HAS_HELLO=yes' <<<"${output}"
grep -qx 'WORKERS_ARRAY_HAS_HELLO=yes' <<<"${output}"

echo "e2e TLS detection source test passed"

#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd -- "${SCRIPT_DIR}/.." && pwd)"
SPEC_PATH="${ROOT_DIR}/../../docs/api/openapi/cordum-api.yaml"
CONFIG_PATH="${ROOT_DIR}/openapi-python-client.config.yaml"
OUTPUT_PATH="${ROOT_DIR}/src/cordum_sdk/_generated"

if [[ -x "${ROOT_DIR}/.venv/Scripts/openapi-python-client.exe" ]]; then
  GENERATOR=("${ROOT_DIR}/.venv/Scripts/openapi-python-client.exe")
elif [[ -x "${ROOT_DIR}/.venv/Scripts/python.exe" ]]; then
  GENERATOR=("${ROOT_DIR}/.venv/Scripts/python.exe" -m openapi_python_client)
elif [[ -x "${ROOT_DIR}/.venv/bin/openapi-python-client" ]]; then
  GENERATOR=("${ROOT_DIR}/.venv/bin/openapi-python-client")
elif [[ -x "${ROOT_DIR}/.venv/bin/python" ]]; then
  GENERATOR=("${ROOT_DIR}/.venv/bin/python" -m openapi_python_client)
elif command -v openapi-python-client >/dev/null 2>&1; then
  GENERATOR=(openapi-python-client)
elif command -v python >/dev/null 2>&1; then
  GENERATOR=(python -m openapi_python_client)
elif command -v python.exe >/dev/null 2>&1; then
  GENERATOR=(python.exe -m openapi_python_client)
elif command -v python3 >/dev/null 2>&1; then
  GENERATOR=(python3 -m openapi_python_client)
else
  echo "Unable to find openapi-python-client, python, python3, or python.exe on PATH" >&2
  exit 1
fi

if [[ "${GENERATOR[0]}" == *.exe ]]; then
  if command -v cygpath >/dev/null 2>&1; then
    SPEC_PATH="$(cygpath -w "${SPEC_PATH}")"
    CONFIG_PATH="$(cygpath -w "${CONFIG_PATH}")"
    OUTPUT_PATH="$(cygpath -w "${OUTPUT_PATH}")"
  elif command -v wslpath >/dev/null 2>&1; then
    SPEC_PATH="$(wslpath -w "${SPEC_PATH}")"
    CONFIG_PATH="$(wslpath -w "${CONFIG_PATH}")"
    OUTPUT_PATH="$(wslpath -w "${OUTPUT_PATH}")"
  fi
fi

"${GENERATOR[@]}" generate \
  --meta none \
  --path "${SPEC_PATH}" \
  --config "${CONFIG_PATH}" \
  --overwrite \
  --output-path "${OUTPUT_PATH}"

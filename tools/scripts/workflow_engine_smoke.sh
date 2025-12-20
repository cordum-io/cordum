#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT_DIR}"

echo "[workflow-engine-smoke] building coretex-workflow-engine..."
GOCACHE="${ROOT_DIR}/.cache/go-build" go build ./cmd/coretex-workflow-engine
echo "[workflow-engine-smoke] ok"


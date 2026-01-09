#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT_DIR}"

echo "[workflow-engine-smoke] building cordum-workflow-engine..."
GOCACHE="${ROOT_DIR}/.cache/go-build" go build -o "${ROOT_DIR}/bin/cordum-workflow-engine" ./cmd/cordum-workflow-engine
echo "[workflow-engine-smoke] ok"

#!/usr/bin/env bash
# ws_soak_test.sh — WebSocket connection stability soak test.
#
# Usage:
#   ./tools/scripts/ws_soak_test.sh quick    # 2 min, 5 clients
#   ./tools/scripts/ws_soak_test.sh          # 10 min, 10 clients (default)
#   ./tools/scripts/ws_soak_test.sh full     # 2 hours, 20 clients
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
BIN_DIR="$ROOT_DIR/bin"
SOAK_BIN="$BIN_DIR/ws-soak"

# ---------- configuration ----------

: "${CORDUM_API_KEY:?CORDUM_API_KEY env var is required}"
: "${CORDUM_WS_URL:=wss://localhost:8081/api/v1/stream}"
: "${CORDUM_STATUS_URL:=https://localhost:8081/api/v1/status}"
: "${TLS_SKIP_VERIFY:=true}"

MODE="${1:-default}"

case "$MODE" in
  quick)
    CLIENTS=5
    DURATION="2m"
    ;;
  default)
    CLIENTS=10
    DURATION="10m"
    ;;
  full)
    CLIENTS=20
    DURATION="2h"
    ;;
  *)
    echo "Usage: $0 [quick|default|full]" >&2
    exit 1
    ;;
esac

# ---------- build ----------

echo "Building ws-soak binary..."
mkdir -p "$BIN_DIR"

GO_CMD="${GO_CMD:-}"
if [[ -z "$GO_CMD" ]]; then
  if command -v go >/dev/null 2>&1; then
    GO_CMD="$(command -v go)"
  elif [[ -x "/c/Program Files/Go/bin/go.exe" ]]; then
    GO_CMD="/c/Program Files/Go/bin/go.exe"
  else
    echo "go not found; install Go or set GO_CMD" >&2
    exit 1
  fi
fi

"$GO_CMD" build -o "$SOAK_BIN" "$ROOT_DIR/tools/ws-soak/"

# ---------- cleanup ----------

cleanup() {
  rm -f "$SOAK_BIN" 2>/dev/null || true
}
trap cleanup EXIT

# ---------- run ----------

echo ""
echo "Starting WebSocket soak test: mode=$MODE clients=$CLIENTS duration=$DURATION"
echo ""

"$SOAK_BIN" \
  -url "$CORDUM_WS_URL" \
  -api-key "$CORDUM_API_KEY" \
  -clients "$CLIENTS" \
  -duration "$DURATION" \
  -status-url "$CORDUM_STATUS_URL" \
  -tls-skip-verify="$TLS_SKIP_VERIFY"

EXIT_CODE=$?

if [[ $EXIT_CODE -eq 0 ]]; then
  echo ""
  echo "WebSocket soak test PASSED ($MODE mode)"
else
  echo ""
  echo "WebSocket soak test FAILED ($MODE mode) — exit code $EXIT_CODE"
fi

exit $EXIT_CODE

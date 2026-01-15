#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
OUT_DIR="${1:-$ROOT_DIR}"
COVERAGE_OUT="$OUT_DIR/coverage.out"
COVERAGE_HTML="$OUT_DIR/coverage.html"

cd "$ROOT_DIR"

go test ./... -coverprofile="$COVERAGE_OUT"

go tool cover -func="$COVERAGE_OUT"
if command -v go >/dev/null 2>&1; then
	go tool cover -html="$COVERAGE_OUT" -o "$COVERAGE_HTML"
	echo "wrote $COVERAGE_HTML"
fi

#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
MIN_COVERAGE="${MIN_COVERAGE:-80}"
COVERAGE_PROFILE="${COVERAGE_PROFILE:-$ROOT_DIR/coverage.core.out}"

cd "$ROOT_DIR"

go test ./core/... -coverprofile="$COVERAGE_PROFILE"

total=$(go tool cover -func="$COVERAGE_PROFILE" | awk '/^total:/ {print $3}' | tr -d '%')
if [[ -z "$total" ]]; then
	echo "coverage total not found" >&2
	exit 1
fi

awk -v total="$total" -v min="$MIN_COVERAGE" 'BEGIN { exit !(total + 0 >= min) }'

echo "core coverage ${total}% (min ${MIN_COVERAGE}%)"

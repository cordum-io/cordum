#!/usr/bin/env bash
# Orchestrates the conformance suite end-to-end for local + CI use.
# Idempotent: re-runs clean if invoked twice.
#
# Usage:
#   ./run.sh                 # build sim + run all three harnesses + aggregate
#   ./run.sh --go-only       # Go harness only (useful during harness dev)
#   ./run.sh --python-only
#   ./run.sh --typescript-only

set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$HERE"

TARGETS=(go python typescript)
if [[ $# -gt 0 ]]; then
  case "$1" in
    --go-only) TARGETS=(go) ;;
    --python-only) TARGETS=(python) ;;
    --typescript-only) TARGETS=(typescript) ;;
    *) echo "unknown flag: $1" >&2; exit 2 ;;
  esac
fi

echo "[run] building simulator"
make sim >/dev/null

for t in "${TARGETS[@]}"; do
  echo "[run] conformance-$t"
  if ! make "conformance-$t"; then
    echo "[run] $t harness reported failures — continuing to next harness" >&2
  fi
done

echo "[run] aggregating"
make aggregate

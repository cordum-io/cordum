#!/usr/bin/env bash
# parity/run.sh — the cross-harness diff-parity gate.
#
# Invokes each harness's diff-cli on the shared scenarios.json and
# asserts (a) every harness produced the same pass/fail verdict per
# scenario, and (b) every verdict matched the scenario's want_pass.
#
# Run this BEFORE the per-SDK fixture grading in CI so a latent diff-
# engine divergence fails the build with a clear "diff implementations
# disagreed" message instead of masquerading as a fixture bug.

set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$HERE/.." && pwd)"
OUT="$HERE/_tmp"
mkdir -p "$OUT"
SCENARIOS="$HERE/scenarios.json"

echo "[parity] Go"
(cd "$ROOT/harness/go" && go run ./cmd/diff-cli) <"$SCENARIOS" >"$OUT/verdicts-go.json"

echo "[parity] Python"
(cd "$ROOT/harness/python" && python -m conformance.diff_cli) <"$SCENARIOS" >"$OUT/verdicts-python.json"

echo "[parity] TypeScript"
(cd "$ROOT/harness/typescript" && node src/diff-cli.mjs) <"$SCENARIOS" >"$OUT/verdicts-typescript.json"

echo "[parity] aggregating"
node "$HERE/compare_verdicts.mjs" \
  --go "$OUT/verdicts-go.json" \
  --python "$OUT/verdicts-python.json" \
  --typescript "$OUT/verdicts-typescript.json"

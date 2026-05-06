#!/usr/bin/env bash
# Regression tests for the static lint guard. These tests intentionally create
# temporary bad Go files under cmd/ so lint_no_secret_log.sh must fail, then
# delete them before the script exits.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
TMP_DIR="$(mktemp -d "$REPO_ROOT/cmd/lint-shell-exec-repro.XXXXXX")"
OUT_FILE="$TMP_DIR/lint.out"

cleanup() {
  case "$TMP_DIR" in
    "$REPO_ROOT"/cmd/lint-shell-exec-repro.*)
      rm -rf "$TMP_DIR"
      ;;
  esac
}
trap cleanup EXIT

run_repro() {
  local name="$1"
  local file="$TMP_DIR/${name}.go"
  cat >"$file"

  if bash "$REPO_ROOT/tools/scripts/lint_no_secret_log.sh" >"$OUT_FILE" 2>&1; then
    echo "FAIL: lint passed for shell-exec repro $name"
    cat "$OUT_FILE"
    exit 1
  fi
  if ! grep -q 'may spawn a shell interpreter via exec.Command' "$OUT_FILE"; then
    echo "FAIL: lint failed for $name but did not report the shell-exec guard"
    cat "$OUT_FILE"
    exit 1
  fi
  rm -f "$file"
}

run_repro one_line <<'GO'
package lintrepro

import (
	"context"
	"os/exec"
)

func bad() {
	_ = exec.CommandContext(context.Background(), "sh", "-c", "echo bad")
}
GO

run_repro multi_line <<'GO'
package lintrepro

import (
	"context"
	"os/exec"
)

func bad() {
	_ = exec.CommandContext(
		context.Background(),
		"sh",
		"-c",
		"echo bad",
	)
}
GO

echo "OK: lint_no_secret_log shell-exec guard catches one-line and multi-line patterns"

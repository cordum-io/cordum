#!/usr/bin/env bash
# Configure the Cordum stack for the LLM-governance demo.
#
# This merges the gateway-side settings (config/gateway.demo.env) into the
# stack's top-level .env and restarts the gateway so POST /api/v1/edge/llm/events
# is exposed with PII+secret+keyword redaction enabled.
#
# Usage:
#   ./setup.sh            # print the steps (safe, no changes)
#   ./setup.sh --apply    # append to ../../.env and restart api-gateway
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$HERE/../.." && pwd)"
ENV_FILE="$REPO_ROOT/.env"
DEMO_ENV="$HERE/config/gateway.demo.env"

echo "Repo root: $REPO_ROOT"
echo "Gateway demo env: $DEMO_ENV"
echo

if [[ "${1:-}" != "--apply" ]]; then
  cat <<EOF
DRY RUN. To enable the demo on a running stack:

  1) Append the gateway settings to your stack .env:
       cat "$DEMO_ENV" >> "$ENV_FILE"

  2) Restart the gateway so it picks them up:
       (cd "$REPO_ROOT" && docker compose up -d api-gateway)

  3) Run the demo:
       ./run_demo.sh

Re-run with --apply to do steps 1-2 automatically.
EOF
  exit 0
fi

echo ">> Appending demo gateway settings to $ENV_FILE"
{ echo ""; echo "# --- llm-governance demo (added by setup.sh) ---"; cat "$DEMO_ENV"; } >> "$ENV_FILE"

echo ">> Restarting api-gateway"
( cd "$REPO_ROOT" && docker compose up -d api-gateway )

echo ">> Done. Now run: ./run_demo.sh"

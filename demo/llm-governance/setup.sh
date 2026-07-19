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

MARKER="# --- llm-governance demo (added by setup.sh) ---"
if grep -qF "$MARKER" "$ENV_FILE" 2>/dev/null; then
  echo ">> Demo gateway settings already present in $ENV_FILE; skipping append"
else
  echo ">> Appending demo gateway settings to $ENV_FILE"
  { echo ""; echo "$MARKER"; cat "$DEMO_ENV"; } >> "$ENV_FILE"
fi

echo ">> Restarting api-gateway"
echo "   NOTE: the gateway must receive the CORDUM_EDGE_LLM_* + CORDUM_API_KEYS"
echo "   vars in its CONTAINER env. If your compose file does not map them from"
echo "   .env, add them to the api-gateway 'environment:' block (or a compose"
echo "   override) before restarting — see config/gateway.demo.env."
( cd "$REPO_ROOT" && docker compose up -d api-gateway )

# When RBAC is entitled, the llm_proxy role must exist with edge.llm.ingest
# (built-in roles do not include it). Idempotent; harmless in non-RBAC mode.
ADMIN_KEY="$(grep -E '^CORDUM_API_KEY=' "$ENV_FILE" | head -1 | cut -d= -f2- | tr -d '"\r' || true)"
if [[ -n "$ADMIN_KEY" ]]; then
  echo ">> Ensuring llm_proxy RBAC role (edge.llm.ingest)"
  curl -ksS -X PUT "https://localhost:8081/api/v1/auth/roles/llm_proxy" \
    -H "X-API-Key: $ADMIN_KEY" -H "X-Tenant-ID: default" -H "Content-Type: application/json" \
    -d '{"description":"LLM proxy ingest","permissions":["edge.llm.ingest"]}' >/dev/null 2>&1 || true
fi

echo ">> Done. Now run: ./run_demo.sh"

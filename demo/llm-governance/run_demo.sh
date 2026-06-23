#!/usr/bin/env bash
# Run the LLM-governance demo end to end:
#   1) start the Cordum OpenAI governance proxy
#   2) run the meeting-assistant agent through it
#   3) show the recorded, redacted turn in the Edge audit trail
#
# Prereqs: Cordum stack up + ./setup.sh applied; python3 with the proxy and
# agent requirements installed. Defaults target the local dev stack.
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$HERE/../.." && pwd)"

export CORDUM_GATEWAY="${CORDUM_GATEWAY:-https://localhost:8081}"
export CORDUM_TENANT="${CORDUM_TENANT:-default}"
export CORDUM_API_KEY="${CORDUM_API_KEY:-demo-llm-proxy-key}"
export CORDUM_PROXY_PRINCIPAL="${CORDUM_PROXY_PRINCIPAL:-llm-proxy-1}"
export CORDUM_CA_CERT="${CORDUM_CA_CERT:-$REPO_ROOT/certs/ca/ca.crt}"
export UPSTREAM="${UPSTREAM:-mock}"
PROXY_PORT="${PROXY_PORT:-8088}"
export CORDUM_PROXY_URL="http://localhost:${PROXY_PORT}/v1"

CURL_CA=( --cacert "$CORDUM_CA_CERT" )
[[ -f "$CORDUM_CA_CERT" ]] || CURL_CA=( -k )

echo ">> Starting governance proxy on :${PROXY_PORT} (upstream=${UPSTREAM})"
( cd "$HERE/proxy" && uvicorn cordum_openai_proxy:app --port "$PROXY_PORT" --log-level warning ) &
PROXY_PID=$!
trap 'kill "$PROXY_PID" 2>/dev/null || true' EXIT

for _ in $(seq 1 30); do
  if curl -fsS "http://localhost:${PROXY_PORT}/healthz" >/dev/null 2>&1; then break; fi
  sleep 0.5
done
SESSION_ID="$(curl -fsS "http://localhost:${PROXY_PORT}/healthz" | sed -n 's/.*"session_id":"\([^"]*\)".*/\1/p')"
echo ">> Proxy ready. edge session: ${SESSION_ID:-<unknown>}"
echo

echo ">> Running meeting-assistant agent through the proxy"
( cd "$HERE/agent" && python3 meeting_agent.py "$HERE/fixtures/meeting_transcript.txt" )
echo

if [[ -n "${SESSION_ID:-}" ]]; then
  echo ">> Recorded, redacted turns in the Edge audit trail:"
  curl -fsS "${CURL_CA[@]}" \
    -H "X-API-Key: $CORDUM_API_KEY" -H "X-Tenant-ID: $CORDUM_TENANT" \
    "${CORDUM_GATEWAY}/api/v1/edge/sessions/${SESSION_ID}/events" || true
  echo
  echo "(Note llm.finding.* labels + redacted input — no raw PII/secrets stored.)"
fi

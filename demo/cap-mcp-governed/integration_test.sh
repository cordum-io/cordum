#!/usr/bin/env bash
# Real-stack proof: policy deny, self-approval refusal, operator execution once,
# and final invariants (four PASS lines, one CAP-MCP GOVERNED DEMO OK marker).
# Requires a fresh compose demo stack with docker-compose.ci.yml and principal-
# bearing CORDUM_API_KEYS, generated certs, and prebuilt cordumctl/worker binaries.
# Local setup and credential/principal table: README.md, Governed end-to-end test.
# Inputs: CORDUM_INTEGRATION=1, CORDUM_API_KEY, CAP_MCP_WORKER_API_KEY,
# CAP_MCP_OPERATOR_API_KEY, REDIS_PASSWORD, CORDUM_TLS_CA, CORDUMCTL_BIN,
# CAP_MCP_WORKER_BIN; optional API_BASE, CERTS_DIR, ARTIFACT_DIR and WAIT_* below.
# Linux/WSL only; Git-Bash is not a supported worker host. Never enable xtrace.
# Exit 0: pass (or opt-out); 1: assertion failure; 2: missing prerequisite.
# jq filters intentionally use single-quoted jq variables, not shell expansion.
# shellcheck disable=SC2016
set -euo pipefail
if [[ "${CORDUM_INTEGRATION:-0}" != 1 ]]; then
  echo 'demo/cap-mcp-governed/integration_test: CORDUM_INTEGRATION=1 required (no-op).'
  exit 0
fi
fail() { echo "FAIL $phase: $*" >&2; exit 1; }
prereq() { echo "PREREQUISITE: $*" >&2; exit 2; }
phase=P0
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
CORDUM_API_BASE="${CORDUM_API_BASE:-https://localhost:8081}"
CORDUM_CERTS_DIR="${CORDUM_CERTS_DIR:-$ROOT/certs}"
WAIT_GATEWAY_SEC="${WAIT_GATEWAY_SEC:-180}"
WAIT_WORKER_SEC="${WAIT_WORKER_SEC:-60}"
WAIT_APPROVAL_SEC="${WAIT_APPROVAL_SEC:-60}"
WAIT_JOB_SEC="${WAIT_JOB_SEC:-90}"

check_inputs() {
  local i j
  local names=(CORDUM_API_KEY CAP_MCP_WORKER_API_KEY CAP_MCP_OPERATOR_API_KEY REDIS_PASSWORD)
  local values=("${CORDUM_API_KEY:-}" "${CAP_MCP_WORKER_API_KEY:-}" "${CAP_MCP_OPERATOR_API_KEY:-}" "${REDIS_PASSWORD:-}")
  for ((i=0; i<4; i++)); do
    [[ -n "${values[i]}" ]] || prereq "${names[i]} is required"
    # Generated hex only: prevents curl-config injection and Redis URL ambiguity.
    [[ "${values[i]}" =~ ^[0-9a-fA-F]{64}$ ]] || prereq "${names[i]} must be 32-byte hex"
    for ((j=0; j<i; j++)); do
      [[ "${values[i]}" != "${values[j]}" ]] || prereq "${names[i]} must be distinct"
    done
  done
  [[ "$CORDUM_API_BASE" == https://* ]] || prereq 'CORDUM_API_BASE must use https'
  [[ -r "${CORDUM_TLS_CA:-}" ]] || prereq 'CORDUM_TLS_CA must be readable'
  [[ -r "$CORDUM_CERTS_DIR/client/tls.crt" && -r "$CORDUM_CERTS_DIR/client/tls.key" ]] || prereq 'client TLS cert/key required'
  [[ -x "${CORDUMCTL_BIN:-}" && -x "${CAP_MCP_WORKER_BIN:-}" ]] || prereq 'CORDUMCTL_BIN and CAP_MCP_WORKER_BIN must be executable'
  if ! command -v jq >/dev/null || ! command -v curl >/dev/null; then prereq 'jq and curl required'; fi
  for i in "$WAIT_GATEWAY_SEC" "$WAIT_WORKER_SEC" "$WAIT_APPROVAL_SEC" "$WAIT_JOB_SEC"; do
    if [[ ! "$i" =~ ^[1-9][0-9]{0,2}$ ]] || ((i>600)); then prereq 'WAIT_* must be integers from 1 to 600'; fi
  done
}
check_inputs
artifacts="${CAP_MCP_ARTIFACT_DIR:-$(mktemp -d)}"
mkdir -p "$artifacts"
tmp="$(mktemp -d)"
worker_pid=
cleanup() {
  local deadline=$((SECONDS+10))
  if [[ -n "$worker_pid" ]]; then
    kill "$worker_pid" 2>/dev/null || true
    while kill -0 "$worker_pid" 2>/dev/null && ((SECONDS<deadline)); do sleep 1; done
    kill -KILL "$worker_pid" 2>/dev/null || true
    wait "$worker_pid" 2>/dev/null || true
  fi
  rm -rf -- "$tmp"
  echo "Artifacts: $artifacts"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

api() {
  local role=$1
  local args=(-sS --cacert "$CORDUM_TLS_CA" --connect-timeout 2 --max-time 10
    -o "$tmp/response.json" -w '%{http_code}' -X "$2"
    -H 'Accept: application/json' -H 'X-Tenant-ID: default' --config -)
  [[ $# == 3 ]] || args+=(-H 'Content-Type: application/json' --data-binary "@$4")
  HTTP_BODY_FILE="$tmp/response.json"
  case "$role" in
    admin) HTTP_STATUS=$(curl "${args[@]}" "$CORDUM_API_BASE$3" <<<"header = \"X-API-Key: $CORDUM_API_KEY\"") || HTTP_STATUS=000 ;;
    worker) HTTP_STATUS=$(curl "${args[@]}" "$CORDUM_API_BASE$3" <<<"header = \"X-API-Key: $CAP_MCP_WORKER_API_KEY\"") || HTTP_STATUS=000 ;;
    operator) HTTP_STATUS=$(curl "${args[@]}" "$CORDUM_API_BASE$3" <<<"header = \"X-API-Key: $CAP_MCP_OPERATOR_API_KEY\"") || HTTP_STATUS=000 ;;
    *) fail 'invalid API role' ;;
  esac
}
status() {
  local code
  [[ "$HTTP_STATUS" != "$1" ]] || return 0
  code=$(jq -r '.code // "unspecified"' "$HTTP_BODY_FILE" 2>/dev/null) || code=invalid_json
  [[ "$code" =~ ^[A-Za-z_]{1,48}$ ]] || code=invalid_code
  fail "HTTP $HTTP_STATUS, want $1 (code=$code)"
}
json() { jq -e "$@" "$HTTP_BODY_FILE" || fail "invalid JSON/assertion (HTTP $HTTP_STATUS)"; }
get() { api admin GET "$1"; status 200; }
identifier() { json -r "$1 | select(type == \"string\" and test(\"^[A-Za-z0-9_-]+$\"))"; }
workflow_count() { get /api/v1/workflows; json 'if type=="array" then length else error("not array") end'; }
matches() {
  local cursor=0 page
  : > "$tmp/approvals.jsonl"
  for ((page=0; page<20; page++)); do
    get "/api/v1/mcp/approvals?limit=200&cursor=$cursor"
    json -c --arg job "cap-mcp-governed:$1" '[.items[] | select(.args.idempotency_key==$job)]' >> "$tmp/approvals.jsonl"
    cursor=$(json -r '.next_cursor | tostring | select(test("^[0-9]+$"))')
    [[ "$cursor" != 0 ]] || break
  done
  [[ "$cursor" == 0 ]] || fail 'approval pagination exceeds 20 pages'
  jq -s 'add | unique_by(.id)' "$tmp/approvals.jsonl" > "$tmp/matches.json"
  match_count=$(jq length "$tmp/matches.json")
}
job_state() {
  get "/api/v1/jobs/$J1"
  cp "$HTTP_BODY_FILE" "$artifacts/job-approved.json"
  state=$(json -r '.state | select(type=="string")')
  case "$state" in
    FAILED|DENIED|TIMEOUT|CANCELLED|OUTPUT_QUARANTINED)
      fail "job $state ($(json -r '(.error_message // "unknown") | split(":")[0] | select(test("^[a-z_]+$"))'))" ;;
  esac
}
leak_check() {
  local rc
  if grep -rFl -f - "$artifacts"; then fail "$1 leaked"; else rc=$?; fi
  [[ "$rc" == 1 ]] || fail "$1 leak scan failed"
}
deadline=$((SECONDS+WAIT_GATEWAY_SEC))
while :; do
  api admin GET /api/v1/status
  [[ "$HTTP_STATUS" != 200 ]] || break
  ((SECONDS<deadline)) || fail "gateway readiness HTTP $HTTP_STATUS"
  sleep 1
done
phase=P1
echo '{"mcp":{"enabled":true,"transport":"http","policy_gate_enabled":false}}' > "$tmp/body.json"
api admin PUT /api/v1/config "$tmp/body.json"; status 204
deadline=$((SECONDS+30))
while :; do
  get /mcp/status
  json 'type=="object"' >/dev/null
  if jq -e '.running==true and .transport=="http" and .enabled_tools>0' "$HTTP_BODY_FILE" >/dev/null; then break; fi
  ((SECONDS<deadline)) || fail 'MCP readiness timeout'
  sleep 1
done
phase=P2
export CORDUM_GATEWAY="$CORDUM_API_BASE" CORDUM_TENANT_ID=default CORDUM_TLS_CA
"$CORDUMCTL_BIN" pack install "$ROOT/demo/cap-mcp-governed/pack" --upgrade
topic=job.cap-mcp-governed.create-workflow
jq -n --arg topic "$topic" '{topic:$topic,tenant:"default",meta:{risk_tags:[]}}' > "$tmp/allow.json"
jq '.meta.risk_tags=["cap-mcp-governed-denied"]' "$tmp/allow.json" > "$tmp/deny.json"
deadline=$((SECONDS+30))
while :; do
  api admin POST /api/v1/policy/evaluate "$tmp/allow.json"; status 200
  json 'type=="object"' >/dev/null
  allow=0
  if jq -e '.decision=="DECISION_TYPE_ALLOW" and (.ruleId | contains("cap-mcp-governed-allow"))' "$HTTP_BODY_FILE" >/dev/null; then allow=1; fi
  api admin POST /api/v1/policy/evaluate "$tmp/deny.json"; status 200
  json 'type=="object"' >/dev/null
  if [[ "$allow" == 1 ]] && jq -e '.decision=="DECISION_TYPE_DENY" and (.ruleId | contains("cap-mcp-governed-denied"))' "$HTTP_BODY_FILE" >/dev/null; then break; fi
  ((SECONDS<deadline)) || fail 'pack policy readiness timeout'
  sleep 1
done
phase=P3
cat > "$tmp/body.json" <<'JSON'
{"name":"cap-mcp-governed demo worker","owner":"cap-mcp-governed-operator","agent_id":"cap-mcp-governed","risk_tier":"high","allowed_tools":["cordum_status","cordum_create_workflow"],"allowed_resources":["cordum://jobs/*"],"preapproved_mutating_tools":[]}
JSON
api admin POST /api/v1/agents "$tmp/body.json"
[[ "$HTTP_STATUS" != 409 ]] || prereq 'identity already linked; use a fresh stack (docker compose ... down -v)'
status 201
identity=$(identifier '.id')
phase=P4
(
  unset CAP_MCP_OPERATOR_API_KEY CORDUM_API_KEYS CORDUM_LICENSE_TOKEN CORDUM_LICENSE_PUBLIC_KEY
  # Deliberately replace only the child worker's key, never the parent's.
  # shellcheck disable=SC2030
  export CORDUM_API_KEY="$CAP_MCP_WORKER_API_KEY"
  unset CAP_MCP_WORKER_API_KEY
  export CORDUM_GATEWAY_URL="$CORDUM_API_BASE" CAP_MCP_JOB_TIMEOUT=3m CAP_MCP_POLL_INTERVAL=500ms
  export NATS_URL=tls://localhost:4222 NATS_TLS_SERVER_NAME=nats NATS_TLS_CA="$CORDUM_TLS_CA"
  export NATS_TLS_CERT="$CORDUM_CERTS_DIR/client/tls.crt" NATS_TLS_KEY="$CORDUM_CERTS_DIR/client/tls.key"
  export REDIS_URL="rediss://:$REDIS_PASSWORD@localhost:6379" REDIS_TLS_CA="$CORDUM_TLS_CA" REDIS_TLS_SERVER_NAME=redis
  exec "$CAP_MCP_WORKER_BIN" > "$artifacts/worker.log" 2>&1
) &
worker_pid=$!
deadline=$((SECONDS+WAIT_WORKER_SEC))
while :; do
  # Do not echo unredacted worker errors; failure artifacts are redacted by CI.
  kill -0 "$worker_pid" 2>/dev/null || fail 'worker exited; see worker.log'
  get /api/v1/workers
  json '.items | type=="array"' >/dev/null
  if jq -e '.items | any(.worker_id=="cap-mcp-governed" and (.heartbeat_age_seconds|type)=="number" and .heartbeat_age_seconds<=10)' "$HTTP_BODY_FILE" >/dev/null; then break; fi
  ((SECONDS<deadline)) || fail 'worker heartbeat timeout'
  sleep 1
done
phase=P5
W0=$(workflow_count)
((W0<=98)) || prereq 'workflow list is capped at 100; use a fresh stack'
run_tag="$(date -u +%Y%m%d%H%M%S)-$$"
name="cap-mcp-governed-$run_tag"
jq -n --arg topic "$topic" --arg name "$name" '{topic:$topic,prompt:"Create the governed demo workflow",context:{workflow:{name:$name,description:"governed demo",steps:{greet:{name:"Greet",type:"transform",input:{message:"governed demo"}}}}}}' > "$tmp/job.json"
phase=P6
jq '.risk_tags=["cap-mcp-governed-denied"] | .context.workflow.name += "-denied"' "$tmp/job.json" > "$tmp/denied-job.json"
api admin POST /api/v1/jobs "$tmp/denied-job.json"; status 403
J2=$(identifier '.job_id')
get "/api/v1/jobs/$J2"
json '.state=="DENIED" and (.safety_rule_id|contains("cap-mcp-governed-denied"))' >/dev/null
cp "$HTTP_BODY_FILE" "$artifacts/job-denied.json"
[[ "$(workflow_count)" == "$W0" ]] || fail 'denied job changed workflow count'
matches "$J2"; [[ "$match_count" == 0 ]] || fail 'denied job created approval'
echo "PASS policy_denied_no_approval_no_workflow W0=$W0 W=$W0 approvals=0"
phase=P7
api admin POST /api/v1/jobs "$tmp/job.json"; status 200
J1=$(identifier '.job_id')
deadline=$((SECONDS+WAIT_APPROVAL_SEC))
while :; do
  matches "$J1"
  ((match_count<=1)) || fail 'multiple approvals for one job'
  job_state
  [[ "$state" != SUCCEEDED ]] || fail 'job succeeded before operator approval'
  [[ "$match_count" != 1 ]] || break
  ((SECONDS<deadline)) || fail 'approval timeout'
  sleep 1
done
jq -e --arg identity "$identity" '.[0] | .tool_name=="cordum_create_workflow" and .status=="pending" and .agent_id==$identity and (.principal==null or .principal=="cap-mcp-governed")' "$tmp/matches.json" >/dev/null || fail 'approval identity/tool/status mismatch'
approval=$(jq -er '.[0].id | select(test("^[A-Za-z0-9_-]+$"))' "$tmp/matches.json")
phase=P8
api worker POST "/api/v1/mcp/approvals/$approval/approve"; status 403
json '.code=="self_approval_denied"' >/dev/null
get "/api/v1/mcp/approvals/$approval"
json '.status=="pending"' >/dev/null
[[ "$(workflow_count)" == "$W0" ]] || fail 'self-approval changed workflow count'
echo "PASS self_approval_denied W0=$W0 W=$W0"
phase=P9
echo '{"reason":"approved by the governed demo operator"}' > "$tmp/body.json"
api operator POST "/api/v1/mcp/approvals/$approval/approve" "$tmp/body.json"; status 200
json '.status=="approved" and .resolved_by=="cap-mcp-governed-operator"' >/dev/null
deadline=$((SECONDS+WAIT_JOB_SEC))
while :; do
  job_state
  [[ "$state" != SUCCEEDED ]] || break
  ((SECONDS<deadline)) || fail 'job completion timeout'
  sleep 1
done
json --arg approval "$approval" '.result.approval_id==$approval' >/dev/null
workflow=$(identifier '.result.workflow_id')
[[ "$(workflow_count)" == "$((W0+1))" ]] || fail 'operator workflow delta is not one'
get "/api/v1/workflows/$workflow"
json --arg name "$name" --arg id "$workflow" '.name==$name and .id==$id' >/dev/null
cp "$HTTP_BODY_FILE" "$artifacts/workflow.json"
get "/api/v1/mcp/approvals/$approval"
json '.status=="approved" and .consumed_at>0' >/dev/null
cp "$HTTP_BODY_FILE" "$artifacts/approval.json"
matches "$J1"; [[ "$match_count" == 1 ]] || fail 'approved job approval count changed'
echo "PASS operator_approval_executes_once W0=$W0 W=$((W0+1))"
phase=P10
matches "$J2"; [[ "$match_count" == 0 ]] || fail 'late approval for denied job'
[[ "$(workflow_count)" == "$((W0+1))" ]] || fail 'late workflow count change'
! grep -Fq -- "$J2" "$artifacts/worker.log" || fail 'denied job reached worker log'
# Patterns go over stdin, never argv or a file. Do not print matching lines.
# Parent retains the bootstrap key; the child-only replacement is intentional.
# shellcheck disable=SC2031
leak_check CORDUM_API_KEY <<<"$CORDUM_API_KEY"
leak_check CAP_MCP_WORKER_API_KEY <<<"$CAP_MCP_WORKER_API_KEY"
leak_check CAP_MCP_OPERATOR_API_KEY <<<"$CAP_MCP_OPERATOR_API_KEY"
leak_check REDIS_PASSWORD <<<"$REDIS_PASSWORD"
echo 'PASS final_invariants'
echo 'CAP-MCP GOVERNED DEMO OK'

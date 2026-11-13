#!/usr/bin/env bash
# =============================================================================
# integration_test.sh — governed A2A end-to-end test for demo/a2a-governed.
#
# Proves that A2A turns sent through cap-a2a-bridge are governed by Cordum. On
# success it prints these 4 lines, then `A2A GOVERNED DEMO OK` exactly once:
#   PASS allowed_turn_input_required_cordum_allow  turn 1 is INPUT_REQUIRED and
#        its job SUCCEEDED with an ALLOW decision by rule a2a-governed-allow
#   PASS resumed_turn_completed_cordum_allow       the resumed task is COMPLETED
#        with one `itinerary` artifact; its job has the same ALLOW decision
#   PASS denied_topic_rejected_cordum_denied       the denied topic is REJECTED;
#        its job is DENIED by a2a-governed-denied and never reaches the worker
#   PASS final_invariants                          processes alive, the task
#        still COMPLETED, and no credential in any artifact
#
# Preconditions (not bootstrapped here):
#   1. Certificates from `cordumctl generate-certs --dir ./certs`.
#   2. The stack from `docker compose -f docker-compose.yml up -d --build`, with
#      no profile and the same CORDUM_API_KEY and REDIS_PASSWORD.
#   3. A prebuilt cordumctl, plus cap-a2a-bridge and cap-a2a-worker built from
#      the CAP SHA pinned in .github/workflows/demo-a2a-governed-e2e.yml.
#
# Inputs (env):
#   CORDUM_INTEGRATION=1    required; anything else is a no-op
#   CORDUM_API_KEY, CAP_A2A_CLIENT_API_KEY, REDIS_PASSWORD
#                           required, non-empty and pairwise distinct
#   CORDUM_TLS_CA           required: the gateway's CA file
#   CORDUM_API_BASE         default https://localhost:8081; https only
#   CORDUM_CERTS_DIR        default <repo>/certs
#   CORDUMCTL_BIN, CAP_A2A_BRIDGE_BIN, CAP_A2A_WORKER_BIN   default <repo>/bin/*
#   A2A_ARTIFACT_DIR        default: a new `mktemp -d` directory
#   WAIT_{GATEWAY,POLICY,WORKER,BRIDGE,TURN,JOB}_SEC   180/30/60/30/90/30
#   Ports 8443 (turns bridge) and 8444 (denied bridge) must be free.
#
# Exit codes: 0 pass, 1 assertion failed, 2 prerequisite missing.
# Local recipe: README.md, "Run it locally". A rerun on the same stack is safe:
# message ids are new on every run and the pack install is an --upgrade.
# Hosts: Linux and WSL only, not Git-Bash.
# Compat limitation: the bridge publishes raw, unsigned sys.job.submit packets,
# which Cordum admits only on its compat profile (CORDUM_SDK_HANDSHAKE off or
# warn). See README.md, "Limitations".
# =============================================================================
set -euo pipefail

if [ "${CORDUM_INTEGRATION:-0}" != "1" ]; then
  echo "demo/a2a-governed/integration_test: CORDUM_INTEGRATION=1 required (no-op)."
  exit 0
fi

# Every FAIL and PREREQ line names the phase it happened in.
PHASE=inputs
prereq() { printf 'PREREQ [%s]: %s\n' "$PHASE" "$*" >&2; exit 2; }
fail() { printf 'FAIL [%s]: %s\n' "$PHASE" "$*" >&2; exit 1; }
say() { printf '[a2a-governed] %s\n' "$*"; }
phase() { PHASE="$1"; say "$*"; }

# --- inputs: failures name the variable, never its value ---------------------
for name in CORDUM_API_KEY CAP_A2A_CLIENT_API_KEY REDIS_PASSWORD; do
  [ -n "${!name:-}" ] || prereq "${name} must be set and non-empty"
done
if [ "$CORDUM_API_KEY" = "$CAP_A2A_CLIENT_API_KEY" ] || [ "$CORDUM_API_KEY" = "$REDIS_PASSWORD" ] \
  || [ "$CAP_A2A_CLIENT_API_KEY" = "$REDIS_PASSWORD" ]; then
  prereq "CORDUM_API_KEY, CAP_A2A_CLIENT_API_KEY and REDIS_PASSWORD must be pairwise distinct"
fi
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
api_base="${CORDUM_API_BASE:-https://localhost:8081}"
case "$api_base" in https://*) ;; *) prereq "CORDUM_API_BASE must be an https URL" ;; esac
if [ ! -f "${CORDUM_TLS_CA:-}" ] || [ ! -r "${CORDUM_TLS_CA:-}" ]; then prereq "CORDUM_TLS_CA must name a readable file"; fi
certs_dir="${CORDUM_CERTS_DIR:-$repo_root/certs}"
for rel in ca/ca.crt client/tls.crt client/tls.key server/tls.crt server/tls.key; do
  [ -r "$certs_dir/$rel" ] || prereq "CORDUM_CERTS_DIR has no readable $rel"
done
: "${CORDUMCTL_BIN:=$repo_root/bin/cordumctl}" "${CAP_A2A_BRIDGE_BIN:=$repo_root/bin/cap-a2a-bridge}"
: "${CAP_A2A_WORKER_BIN:=$repo_root/bin/cap-a2a-worker}"
for name in CORDUMCTL_BIN CAP_A2A_BRIDGE_BIN CAP_A2A_WORKER_BIN; do
  if [ ! -f "${!name}" ] || [ ! -x "${!name}" ]; then prereq "${name} must name an executable file"; fi
done
: "${WAIT_GATEWAY_SEC:=180}" "${WAIT_POLICY_SEC:=30}" "${WAIT_WORKER_SEC:=60}"
: "${WAIT_BRIDGE_SEC:=30}" "${WAIT_TURN_SEC:=90}" "${WAIT_JOB_SEC:=30}"
for name in WAIT_GATEWAY_SEC WAIT_POLICY_SEC WAIT_WORKER_SEC WAIT_BRIDGE_SEC WAIT_TURN_SEC WAIT_JOB_SEC; do
  case "${!name}" in '' | *[!0-9]*) prereq "${name} must be a whole number of seconds" ;; esac
done
for tool in jq curl openssl sha256sum; do command -v "$tool" >/dev/null 2>&1 || prereq "$tool is required"; done
py="$(command -v python3 || command -v python || true)"
[ -n "$py" ] || prereq "python3 is required"
port_turns=8443 port_denied=8444
for port in "$port_turns" "$port_denied"; do
  if (exec 3<>"/dev/tcp/127.0.0.1/$port") 2>/dev/null; then prereq "port $port already accepts connections"; fi
done

# --- process and file lifecycle ----------------------------------------------
pids=""
alive_any() {
  local pid
  for pid in $pids; do if kill -0 "$pid" 2>/dev/null; then return 0; fi; done
  return 1
}
cleanup() {
  local pid n=0
  for pid in $pids; do kill -TERM "$pid" 2>/dev/null || true; done
  while [ "$n" -lt 20 ] && alive_any; do sleep 0.5; n=$((n + 1)); done
  for pid in $pids; do kill -KILL "$pid" 2>/dev/null || true; done
  rm -rf "${tmp:-}"
  say "artifacts at ${artifacts:-none}"
}
umask 077
tmp="$(mktemp -d)"
artifacts="${A2A_ARTIFACT_DIR:-$(mktemp -d)}"
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
mkdir -p "$artifacts"
HTTP_BODY_FILE="$artifacts/http-last.json" HTTP_STATUS=000

# --- helpers -------------------------------------------------------------------
# poll SECONDS PAUSE CMD...: rerun CMD until it succeeds; status 1 on timeout.
poll() {
  local deadline pause="$2"
  deadline=$(($(date +%s) + $1))
  shift 2
  until "$@"; do
    [ "$(date +%s)" -lt "$deadline" ] || return 1
    sleep "$pause"
  done
}
# api METHOD PATH [BODY_FILE]: sets HTTP_STATUS (000 on transport failure) and
# HTTP_BODY_FILE. The API key reaches curl only on stdin, never in argv.
api() {
  local -a args=(-sS --cacert "$CORDUM_TLS_CA" --connect-timeout 2 --max-time 10 -o "$HTTP_BODY_FILE"
    -w '%{http_code}' -X "$1" -H 'Accept: application/json' -H 'X-Tenant-ID: default')
  if [ -n "${3:-}" ]; then args+=(-H 'Content-Type: application/json' --data-binary "@$3"); fi
  : >"$HTTP_BODY_FILE"
  HTTP_STATUS="$(curl "${args[@]}" --config - "${api_base}$2" 2>>"$artifacts/curl.err" \
    <<<"header = \"X-API-Key: ${CORDUM_API_KEY}\"")" || HTTP_STATUS=000
}
# a2a PORT BODY_FILE OUT_FILE: one JSON-RPC call to the URL in that bridge's
# card. The bearer reaches curl only on stdin; errors show code and message.
a2a() {
  local url="$rpc_turns" status
  all_alive
  [ "$1" = "$port_turns" ] || url="$rpc_denied"
  status="$(curl -sS --cacert "$certs_dir/ca/ca.crt" --connect-timeout 2 --max-time 40 -o "$3" \
    -w '%{http_code}' -X POST -H 'Content-Type: application/json' -H "A2A-Version: ${A2A_VERSION}" \
    --data-binary "@$2" --config - "$url" 2>>"$artifacts/curl.err" \
    <<<"header = \"Authorization: Bearer ${CAP_A2A_CLIENT_API_KEY}\"")" || status=000
  if [ "$status" != 200 ] || ! jq -e 'type == "object" and .error == null' "$3" >/dev/null 2>&1; then
    fail "A2A $(jq -r '.method' "$2") on port $1: HTTP $status, error $(jq -c '[.error.code, .error.message]' "$3" 2>/dev/null || echo unreadable)"
  fi
}
# job_id TENANT PRINCIPAL TASK_REF MESSAGE_ID: the bridge's job id (spec §3.1).
job_id() {
  local id
  id="$("$py" -c 'import hashlib, struct, sys
h = hashlib.sha256(b"cap-a2a-turn-v1")
for field in sys.argv[1:]:
    b = field.encode("utf-8")
    h.update(struct.pack(">I", len(b)) + b)
print("a2a-" + h.hexdigest())' "$@")" || fail "job_id: python failed"
  [[ "$id" =~ ^a2a-[0-9a-f]{64}$ ]] || fail "job_id: malformed id"
  printf '%s\n' "$id"
}
# task_is PORT TASK WANT OUT: one GetTask, the task into OUT. 0 at WANT; fails
# at once on another terminal or interrupted state; 1 while still running.
task_is() {
  jq -n --arg id "$2" '{jsonrpc: "2.0", id: "get-task", method: "GetTask", params: {id: $id}}' \
    >"$artifacts/get-task.request.json"
  a2a "$1" "$artifacts/get-task.request.json" "$artifacts/get-task.json"
  jq '.result.task // .result' "$artifacts/get-task.json" >"$4"
  LAST_STATE="$(jq -r '.status.state // empty' "$4")"
  [ "$LAST_STATE" != "$3" ] || return 0
  case "$LAST_STATE" in
    TASK_STATE_COMPLETED | TASK_STATE_FAILED | TASK_STATE_CANCELED | TASK_STATE_REJECTED | \
      TASK_STATE_INPUT_REQUIRED | TASK_STATE_AUTH_REQUIRED) fail "task $2 is $LAST_STATE, want $3" ;;
  esac
  return 1
}
wait_task() {
  LAST_STATE=""
  poll "$WAIT_TURN_SEC" 0.5 task_is "$@" || fail "task $2 is not $3 after ${WAIT_TURN_SEC}s (last ${LAST_STATE:-none})"
}
# job_is ID WANT: 0 once the Cordum job is WANT; fails at once on another
# terminal state; 1 while it is missing or running.
job_is() {
  all_alive
  api GET "/api/v1/jobs/$1"
  [ "$HTTP_STATUS" = 200 ] || return 1
  cp "$HTTP_BODY_FILE" "$artifacts/job-$1.json"
  LAST_STATE="$(jq -r '.state // empty' "$HTTP_BODY_FILE")"
  [ "$LAST_STATE" != "$2" ] || return 0
  case "$LAST_STATE" in
    SUCCEEDED | FAILED | CANCELLED | TIMEOUT | DENIED | OUTPUT_QUARANTINED) fail "job $1 is $LAST_STATE, want $2" ;;
  esac
  return 1
}
wait_job() {
  LAST_STATE=""
  poll "$WAIT_JOB_SEC" 0.5 job_is "$1" "$2" \
    || fail "job $1 is not $2 after ${WAIT_JOB_SEC}s (last ${LAST_STATE:-none}, HTTP $HTTP_STATUS)"
}
# check_job ID TOPIC RULE DECISION [WORKFLOW_ID]: Cordum's record of the job.
check_job() {
  local f="$artifacts/job-$1.json"
  jq -e --arg t "$2" --arg w "${5:-}" '.topic == $t and .tenant == "default" and ($w == "" or .workflow_id == $w)' \
    "$f" >/dev/null || fail "job $1 is $(jq -c '{topic, tenant, workflow_id}' "$f"), want topic $2, tenant default ${5:+and workflow_id $5}"
  jq -e --arg r "$3" '.safety_rule_id == $r' "$f" >/dev/null \
    || fail "job $1 has safety_rule_id $(jq -r '.safety_rule_id // "none"' "$f"), want $3"
  api GET "/api/v1/jobs/$1/decisions"
  cp "$HTTP_BODY_FILE" "$artifacts/decisions-$1.json"
  [ "$HTTP_STATUS" = 200 ] || fail "GET /api/v1/jobs/$1/decisions returned HTTP $HTTP_STATUS"
  jq -e --arg d "$4" --arg r "$3" 'any(.[]?; .decision == $d and .rule_id == $r)' "$HTTP_BODY_FILE" >/dev/null \
    || fail "job $1 has no $4 decision with rule_id $3"
}
# send PORT N TEXT [TASK CONTEXT]: SendMessage; sets MSG, writes send-N.json and
# task-N.json. settle PORT N WANT: task-N.json ends in WANT, via GetTask if needed.
send() {
  MSG="m-$run_tag-$2"
  jq -n --arg rid "$run_tag-$2" --arg m "$MSG" --arg text "$3" --arg t "${4:-}" --arg c "${5:-}" \
    '{jsonrpc: "2.0", id: $rid, method: "SendMessage", params: {message: ({messageId: $m, role: "ROLE_USER",
      parts: [{text: $text}]} + (if $t == "" then {} else {taskId: $t, contextId: $c} end))}}' \
    >"$artifacts/send-$2.request.json"
  a2a "$1" "$artifacts/send-$2.request.json" "$artifacts/send-$2.json"
  jq '.result.task // .result' "$artifacts/send-$2.json" >"$artifacts/task-$2.json"
}
settle() {
  local f="$artifacts/task-$2.json"
  [ "$(jq -r '.status.state // empty' "$f")" = "$3" ] || wait_task "$1" "$(jq -r '.id' "$f")" "$3" "$f"
}
gateway_ready() { api GET /api/v1/status; [ "$HTTP_STATUS" = 200 ]; }
# policy_is TOPIC DECISION RULE NAME: policy/evaluate decides TOPIC that way.
policy_is() {
  jq -n --arg t "$1" '{topic: $t, tenant: "default"}' >"$artifacts/policy-$4.request.json"
  api POST /api/v1/policy/evaluate "$artifacts/policy-$4.request.json"
  cp "$HTTP_BODY_FILE" "$artifacts/policy-$4.json"
  [ "$HTTP_STATUS" = 200 ] && jq -e --arg d "$2" --arg r "$3" '.decision == $d and .ruleId == $r' \
    "$HTTP_BODY_FILE" >/dev/null
}
policy_ready() {
  policy_is job.a2a-governed.turns DECISION_TYPE_ALLOW a2a-governed-allow turns \
    && policy_is job.a2a-governed.denied DECISION_TYPE_DENY a2a-governed-denied denied
}
# alive PID LOG WHAT: fails with the log's last 20 lines once PID has exited.
alive() {
  kill -0 "$1" 2>/dev/null && return 0
  tail -n 20 "$2" >&2 || true
  fail "$3 exited"
}
# all_alive: once P4 has started them, the worker and both bridges still run.
all_alive() {
  alive "$worker_pid" "$artifacts/worker.log" "the worker"
  alive "$turns_pid" "$artifacts/bridge-turns.log" "the turns bridge"
  alive "$denied_pid" "$artifacts/bridge-denied.log" "the denied bridge"
}
worker_ready() {
  alive "$worker_pid" "$artifacts/worker.log" "the worker"
  api GET /api/v1/workers
  cp "$HTTP_BODY_FILE" "$artifacts/workers.json"
  [ "$HTTP_STATUS" = 200 ] && jq -e 'any(.items[]?; .worker_id == "a2a-demo-worker" and .pool == "a2a-governed"
    and (.heartbeat_age_seconds // 999) <= 10)' "$HTTP_BODY_FILE" >/dev/null
}
# start ROLE [PORT TOPIC]: start the worker, or a bridge, in a subshell that
# drops the Cordum and A2A credentials. REDIS_PASSWORD reaches the process only
# inside CAP_REDIS_URL, and is then unset.
start() {
  (
    unset CORDUM_API_KEY CAP_A2A_CLIENT_API_KEY CAP_A2A_DEV_INSECURE CAP_A2A_RESULT_KEYS_DIR \
      NATS_TLS_INSECURE REDIS_TLS_INSECURE
    export CAP_NATS_URL=tls://localhost:4222 NATS_TLS_CA="$certs_dir/ca/ca.crt" NATS_TLS_SERVER_NAME=nats \
      NATS_TLS_CERT="$certs_dir/client/tls.crt" NATS_TLS_KEY="$certs_dir/client/tls.key"
    export CAP_REDIS_URL="rediss://:${REDIS_PASSWORD}@localhost:6379" REDIS_TLS_CA="$certs_dir/ca/ca.crt" \
      REDIS_TLS_SERVER_NAME=redis CAP_A2A_DEV_UNSIGNED=1
    unset REDIS_PASSWORD
    if [ "$1" = worker ]; then
      unset CAP_SCHEDULER_PUBLIC_KEY_FILE CAP_WORKER_SIGNING_KEY_FILE
      export CAP_A2A_TOPIC=job.a2a-governed.turns CAP_WORKER_ID=a2a-demo-worker CAP_WORKER_POOL=a2a-governed \
        CAP_WORKER_HEARTBEAT_INTERVAL=2s CORDUM_SDK_HANDSHAKE=off
      exec "$CAP_A2A_WORKER_BIN" >"$artifacts/worker.log" 2>&1
    fi
    export CAP_A2A_LISTEN="127.0.0.1:$2" CAP_A2A_PUBLIC_URL="https://localhost:$2" CAP_A2A_TOPIC="$3" \
      CAP_A2A_TLS_CERT_FILE="$certs_dir/server/tls.crt" CAP_A2A_TLS_KEY_FILE="$certs_dir/server/tls.key" \
      CAP_A2A_API_KEYS_FILE="$tmp/a2a-clients.json" CAP_A2A_SIGNING_KEY_FILE="$tmp/bridge-signing.pem" \
      CAP_A2A_MAX_BLOCKING_WAIT=20s
    exec "$CAP_A2A_BRIDGE_BIN" >"$artifacts/bridge-$1.log" 2>&1
  ) &
  pids="$pids $!"
}
# card_ready PORT PID NAME: the agent card is served over verified TLS.
# card_field NAME PORT FIELD: FIELD of its JSONRPC interface at its public URL.
card_ready() {
  local status
  alive "$2" "$artifacts/bridge-$3.log" "the $3 bridge"
  status="$(curl -sS --cacert "$certs_dir/ca/ca.crt" --connect-timeout 2 --max-time 5 -o "$artifacts/card-$3.json" \
    -w '%{http_code}' "https://localhost:$1/.well-known/agent-card.json" 2>>"$artifacts/curl.err")" || status=000
  [ "$status" = 200 ]
}
card_field() {
  jq -r --arg u "https://localhost:$2" --arg f "$3" 'first(.supportedInterfaces[]?
    | select(.protocolBinding == "JSONRPC" and ((.url // "") | rtrimstr("/")) == $u)) | .[$f] // empty' \
    "$artifacts/card-$1.json"
}

# --- P0-P2: gateway, pack and policy, bridge material ------------------------
phase P0 "waiting for ${api_base}/api/v1/status (up to ${WAIT_GATEWAY_SEC}s)"
poll "$WAIT_GATEWAY_SEC" 2 gateway_ready || prereq "the gateway status is HTTP $HTTP_STATUS after ${WAIT_GATEWAY_SEC}s"
phase P1 "installing demo/a2a-governed/pack"
if ! (
  export CORDUM_GATEWAY="$api_base" CORDUM_TENANT_ID=default CORDUM_TLS_CA
  exec "$CORDUMCTL_BIN" pack install "$repo_root/demo/a2a-governed/pack" --upgrade
) >"$artifacts/pack-install.log" 2>&1; then
  tail -n 20 "$artifacts/pack-install.log" >&2 || true
  fail "cordumctl pack install failed"
fi
poll "$WAIT_POLICY_SEC" 1 policy_ready || fail "policy/evaluate lacks the pack's allow and deny rules after ${WAIT_POLICY_SEC}s"
phase P2 "writing the client digest and a signing key to a private temp dir"
digest="$(printf '%s' "$CAP_A2A_CLIENT_API_KEY" | sha256sum | cut -d' ' -f1)"
[[ "$digest" =~ ^[0-9a-f]{64}$ ]] || prereq "sha256sum did not produce a digest"
jq -n --arg d "$digest" '[{sha256: $d, tenant: "default", principal: "a2a-demo-client"}]' >"$tmp/a2a-clients.json"
openssl ecparam -name prime256v1 -genkey -noout | openssl pkcs8 -topk8 -nocrypt >"$tmp/bridge-signing.pem"

# --- P3-P4: worker and bridges -----------------------------------------------
phase P3 "starting the worker in pool a2a-governed"
start worker
worker_pid=$!
poll "$WAIT_WORKER_SEC" 1 worker_ready || fail "a2a-demo-worker has no fresh heartbeat in pool a2a-governed after ${WAIT_WORKER_SEC}s"
phase P4 "starting the turns bridge on :$port_turns and the denied bridge on :$port_denied"
start turns "$port_turns" job.a2a-governed.turns
turns_pid=$!
start denied "$port_denied" job.a2a-governed.denied
denied_pid=$!
poll "$WAIT_BRIDGE_SEC" 1 card_ready "$port_turns" "$turns_pid" turns || fail "no agent card on :$port_turns after ${WAIT_BRIDGE_SEC}s"
poll "$WAIT_BRIDGE_SEC" 1 card_ready "$port_denied" "$denied_pid" denied || fail "no agent card on :$port_denied after ${WAIT_BRIDGE_SEC}s"
rpc_turns="$(card_field turns "$port_turns" url)"
rpc_denied="$(card_field denied "$port_denied" url)"
A2A_VERSION="$(card_field turns "$port_turns" protocolVersion)"
if [ -z "$rpc_turns" ] || [ -z "$rpc_denied" ] || [ -z "$A2A_VERSION" ]; then
  fail "an agent card has no JSONRPC interface with a protocolVersion at its public URL"
fi

# --- P5: allowed turn 1 -------------------------------------------------------
run_tag="$(date -u +%Y%m%d%H%M%S)-$$"
phase P5 "allowed turn 1 (run $run_tag)"
send "$port_turns" 1 "Plan a trip"
m1="$MSG"
task_id="$(jq -r '.id // empty' "$artifacts/task-1.json")"
context_id="$(jq -r '.contextId // empty' "$artifacts/task-1.json")"
[[ "$task_id" =~ ^a2a-task-[0-9a-f]{32}$ ]] || fail "turn 1 returned a malformed task id"
[ -n "$context_id" ] || fail "turn 1 returned no contextId"
settle "$port_turns" 1 TASK_STATE_INPUT_REQUIRED
jq -e '(.status.message.parts // []) | length >= 1' "$artifacts/task-1.json" >/dev/null \
  || fail "the INPUT_REQUIRED status of task $task_id has no message parts"
job1="$(job_id default a2a-demo-client "" "$m1")"
wait_job "$job1" SUCCEEDED
check_job "$job1" job.a2a-governed.turns a2a-governed-allow ALLOW "$task_id"
echo "PASS allowed_turn_input_required_cordum_allow"

# --- P6: resume ---------------------------------------------------------------
phase P6 "resuming task $task_id"
send "$port_turns" 2 "Lisbon" "$task_id" "$context_id"
m2="$MSG"
[ "$(jq -r '.id' "$artifacts/task-2.json")" = "$task_id" ] || fail "the resumed turn answered another task"
settle "$port_turns" 2 TASK_STATE_COMPLETED
jq -e '(.artifacts // []) | length == 1 and .[0].artifactId == "itinerary"' "$artifacts/task-2.json" >/dev/null \
  || fail "the COMPLETED task $task_id does not hold exactly one itinerary artifact"
job2="$(job_id default a2a-demo-client "$task_id" "$m2")"
[ "$job2" != "$job1" ] || fail "the resumed turn has the same job id as turn 1"
wait_job "$job2" SUCCEEDED
check_job "$job2" job.a2a-governed.turns a2a-governed-allow ALLOW "$task_id"
echo "PASS resumed_turn_completed_cordum_allow"

# --- P7: denied topic ---------------------------------------------------------
phase P7 "sending to the denied topic"
send "$port_denied" 3 "Plan a trip"
m3="$MSG"
settle "$port_denied" 3 TASK_STATE_REJECTED
job3="$(job_id default a2a-demo-client "" "$m3")"
wait_job "$job3" DENIED
check_job "$job3" job.a2a-governed.denied a2a-governed-denied DENY
api GET "/api/v1/workers/a2a-demo-worker/jobs?limit=100"
cp "$HTTP_BODY_FILE" "$artifacts/worker-jobs.json"
[ "$HTTP_STATUS" = 200 ] || fail "GET /api/v1/workers/a2a-demo-worker/jobs returned HTTP $HTTP_STATUS"
jq -e --arg a "$job1" --arg b "$job2" --arg c "$job3" \
  'any(.items[]?; .id == $a) and any(.items[]?; .id == $b) and all(.items[]?; .id != $c)' \
  "$artifacts/worker-jobs.json" >/dev/null || fail "the worker's jobs must hold $job1 and $job2 and not $job3"
if grep -qF "$job1" "$artifacts/worker.log" && grep -qF "$job3" "$artifacts/worker.log"; then
  fail "the worker log names the denied job $job3"
fi
echo "PASS denied_topic_rejected_cordum_denied"

# --- P8: final invariants -----------------------------------------------------
phase P8 "checking the final invariants"
all_alive
wait_task "$port_turns" "$task_id" TASK_STATE_COMPLETED "$artifacts/task-final.json"
for name in CORDUM_API_KEY CAP_A2A_CLIENT_API_KEY REDIS_PASSWORD; do
  if grep -rFq -f - "$artifacts" <<<"${!name}"; then
    fail "${name} appears in: $(grep -rlF -f - "$artifacts" <<<"${!name}" | tr '\n' ' ')"
  fi
done
echo "PASS final_invariants"
echo "A2A GOVERNED DEMO OK"

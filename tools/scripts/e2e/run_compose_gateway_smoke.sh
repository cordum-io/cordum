#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
cd "$ROOT"

require_cmd() {
  local cmd="$1"
  if ! command -v "$cmd" >/dev/null 2>&1; then
    echo "[compose-smoke] missing required command: $cmd" >&2
    exit 1
  fi
}

require_cmd docker
require_cmd curl
require_cmd jq
require_cmd python3

API_BASE="${CORETEX_API_BASE:-http://localhost:8081}"
API_KEY="${CORETEX_API_KEY:-super-secret-key}"

pick_free_port() {
  python3 - <<'PY' 2>/dev/null
import socket
s = socket.socket()
s.bind(("127.0.0.1", 0))
print(s.getsockname()[1])
s.close()
PY
}

wait_for_http() {
  local url="$1"
  local timeout_s="${2:-30}"

  local start
  start="$(date +%s)"
  while true; do
    if curl -fsS "$url" >/dev/null 2>&1; then
      return 0
    fi
    if [ $(( $(date +%s) - start )) -ge "$timeout_s" ]; then
      return 1
    fi
    sleep 0.25
  done
}

RUN_ID="$(date +%s)-$$"
CACHE_DIR="$ROOT/.cache/e2e/compose-smoke/$RUN_ID"
mkdir -p "$CACHE_DIR"

MOCK_OLLAMA_PORT="${MOCK_OLLAMA_PORT:-$(pick_free_port)}"
if [ -z "$MOCK_OLLAMA_PORT" ]; then
  echo "[compose-smoke] unable to allocate a free port for mock ollama" >&2
  exit 1
fi
export MOCK_OLLAMA_PORT

MOCK_PID=""

cleanup() {
  set +e
  if [ -n "${MOCK_PID:-}" ]; then
    kill "$MOCK_PID" >/dev/null 2>&1 || true
    wait "$MOCK_PID" >/dev/null 2>&1 || true
  fi
  if [ "${E2E_KEEP_STACK:-0}" != "1" ]; then
    docker compose -f docker-compose.yml -f tools/scripts/e2e/docker-compose.mock-ollama.yml down >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

echo "[compose-smoke] starting mock ollama on 0.0.0.0:${MOCK_OLLAMA_PORT}"
MOCK_OLLAMA_PORT="$MOCK_OLLAMA_PORT" python3 - <<'PY' >"$CACHE_DIR/mock-ollama.log" 2>&1 &
import json
import os
from http.server import BaseHTTPRequestHandler, HTTPServer

class Handler(BaseHTTPRequestHandler):
  def log_message(self, fmt, *args):
    return

  def do_GET(self):
    if self.path == "/api/version":
      self.send_response(200)
      self.send_header("Content-Type", "application/json")
      self.end_headers()
      self.wfile.write(b'{"version":"mock"}')
      return
    self.send_response(404)
    self.end_headers()

  def do_POST(self):
    if self.path != "/api/generate":
      self.send_response(404)
      self.end_headers()
      return
    length = int(self.headers.get("Content-Length", "0") or "0")
    body = self.rfile.read(length) if length > 0 else b"{}"
    try:
      req = json.loads(body.decode("utf-8"))
    except Exception:
      req = {}
    prompt = str(req.get("prompt") or "")
    resp = {"response": "[mock] " + prompt}
    data = json.dumps(resp).encode("utf-8")
    self.send_response(200)
    self.send_header("Content-Type", "application/json")
    self.send_header("Content-Length", str(len(data)))
    self.end_headers()
    self.wfile.write(data)

addr = ("0.0.0.0", int(os.environ["MOCK_OLLAMA_PORT"]))
srv = HTTPServer(addr, Handler)
srv.serve_forever()
PY
MOCK_PID="$!"

wait_for_http "http://127.0.0.1:${MOCK_OLLAMA_PORT}/api/version" 10 || {
  echo "[compose-smoke] mock ollama did not become ready" >&2
  tail -n 200 "$CACHE_DIR/mock-ollama.log" >&2 || true
  exit 1
}

echo "[compose-smoke] starting compose stack (mock ollama override)"
docker compose -f docker-compose.yml -f tools/scripts/e2e/docker-compose.mock-ollama.yml up -d --build \
  nats redis \
  coretex-context-engine \
  coretex-safety-kernel coretex-scheduler coretex-api-gateway coretex-workflow-engine \
  coretex-worker-echo coretex-worker-chat coretex-worker-chat-advanced >/dev/null

wait_for_http "${API_BASE}/health" 30 || {
  echo "[compose-smoke] gateway not healthy at ${API_BASE}/health" >&2
  docker compose -f docker-compose.yml -f tools/scripts/e2e/docker-compose.mock-ollama.yml logs --tail=200 coretex-api-gateway >&2 || true
  exit 1
}

echo "[compose-smoke] waiting for workers to register"
start_workers="$(date +%s)"
while true; do
  workers_json="$(curl -fsS "${API_BASE}/api/v1/workers" -H "X-API-Key: ${API_KEY}" || true)"
  ok_echo=0
  ok_chat=0
  ok_adv=0
  if echo "$workers_json" | jq -e '.[] | select(.worker_id == "worker-echo-1")' >/dev/null 2>&1; then ok_echo=1; fi
  if echo "$workers_json" | jq -e '.[] | select(.worker_id == "worker-chat-1")' >/dev/null 2>&1; then ok_chat=1; fi
  if echo "$workers_json" | jq -e '.[] | select(.worker_id == "worker-chat-advanced-1")' >/dev/null 2>&1; then ok_adv=1; fi
  if [ "$ok_echo" -eq 1 ] && [ "$ok_chat" -eq 1 ] && [ "$ok_adv" -eq 1 ]; then
    break
  fi
  if [ $(( $(date +%s) - start_workers )) -ge 30 ]; then
    echo "[compose-smoke] timed out waiting for workers; /api/v1/workers=${workers_json}" >&2
    docker compose -f docker-compose.yml -f tools/scripts/e2e/docker-compose.mock-ollama.yml logs --tail=200 coretex-scheduler >&2 || true
    docker compose -f docker-compose.yml -f tools/scripts/e2e/docker-compose.mock-ollama.yml logs --tail=200 coretex-worker-echo coretex-worker-chat coretex-worker-chat-advanced >&2 || true
    exit 1
  fi
  sleep 0.25
done

submit_job() {
  local topic="$1"
  local prompt="$2"
  local memory_id="$3"
  local wf_id="$4"
  local run_id="$5"
  local node_id="$6"

  local body
  body="$(jq -nc \
    --arg topic "$topic" \
    --arg prompt "$prompt" \
    --arg memory_id "$memory_id" \
    --arg wf_id "$wf_id" \
    --arg run_id "$run_id" \
    --arg node_id "$node_id" \
    '{topic:$topic,prompt:$prompt,priority:"interactive",memory_id:$memory_id,labels:{workflow_id:$wf_id,run_id:$run_id,node_id:$node_id}}')"

  curl -fsS -X POST "${API_BASE}/api/v1/jobs" \
    -H 'Content-Type: application/json' \
    -H "X-API-Key: ${API_KEY}" \
    -d "$body"
}

wait_job_succeeded() {
  local job_id="$1"
  local timeout_s="${2:-25}"

  local start
  start="$(date +%s)"
  while true; do
    local details state
    details="$(curl -fsS "${API_BASE}/api/v1/jobs/${job_id}" -H "X-API-Key: ${API_KEY}" || true)"
    state="$(echo "$details" | jq -r '.state // empty')"
    if [ "$state" = "SUCCEEDED" ]; then
      echo "$details"
      return 0
    fi
    if [ "$state" = "FAILED" ] || [ "$state" = "TIMEOUT" ] || [ "$state" = "DENIED" ] || [ "$state" = "CANCELLED" ]; then
      echo "[compose-smoke] job terminal state=$state job_id=$job_id details=$details" >&2
      return 1
    fi
    if [ $(( $(date +%s) - start )) -ge "$timeout_s" ]; then
      echo "[compose-smoke] timed out waiting for job_id=$job_id state=$state" >&2
      return 1
    fi
    sleep 0.25
  done
}

fetch_ptr() {
  local ptr="$1"
  local encoded
  encoded="$(jq -rn --arg v "$ptr" '$v|@uri')"
  curl -fsS "${API_BASE}/api/v1/memory?ptr=${encoded}" -H "X-API-Key: ${API_KEY}"
}

echo "[compose-smoke] smoke: scheduler ignores studio labels (node_id)"
LABEL_WF="wf-smoke"
LABEL_RUN="run-smoke-${RUN_ID}"

echo_job_json="$(submit_job "job.echo" "hello from compose smoke" "" "$LABEL_WF" "$LABEL_RUN" "node-echo-1")"
echo_job_id="$(echo "$echo_job_json" | jq -r '.job_id')"
if [ -z "$echo_job_id" ] || [ "$echo_job_id" = "null" ]; then
  echo "[compose-smoke] failed to submit echo job: $echo_job_json" >&2
  exit 1
fi

echo_details="$(wait_job_succeeded "$echo_job_id" 30)" || exit 1
ctx_ptr="$(echo "$echo_details" | jq -r '.context_ptr // empty')"
res_ptr="$(echo "$echo_details" | jq -r '.result_ptr // empty')"
if [ -z "$ctx_ptr" ] || [ -z "$res_ptr" ]; then
  echo "[compose-smoke] missing ctx/res pointers details=$echo_details" >&2
  exit 1
fi

ctx_mem="$(fetch_ptr "$ctx_ptr")"
if [ "$(echo "$ctx_mem" | jq -r '.kind // empty')" != "context" ]; then
  echo "[compose-smoke] expected kind=context got=$ctx_mem" >&2
  exit 1
fi
res_mem="$(fetch_ptr "$res_ptr")"
if [ "$(echo "$res_mem" | jq -r '.kind // empty')" != "result" ]; then
  echo "[compose-smoke] expected kind=result got=$res_mem" >&2
  exit 1
fi

echo "[compose-smoke] smoke: chat-advanced uses memory_id and mem:* is readable via /api/v1/memory"
CHAT_MEMORY_ID="smoke-chat-${RUN_ID}"
prev_len=0
for i in 1 2 3; do
  prompt="message ${i} from compose smoke"
  job_json="$(submit_job "job.chat.advanced" "$prompt" "$CHAT_MEMORY_ID" "$LABEL_WF" "$LABEL_RUN" "node-chat-adv-${i}")"
  job_id="$(echo "$job_json" | jq -r '.job_id')"
  if [ -z "$job_id" ] || [ "$job_id" = "null" ]; then
    echo "[compose-smoke] failed to submit chat-advanced job: $job_json" >&2
    exit 1
  fi
  details="$(wait_job_succeeded "$job_id" 40)" || exit 1
  resp="$(echo "$details" | jq -r '.result.response // empty')"
  if [ -z "$resp" ] || [ "$resp" = "null" ]; then
    echo "[compose-smoke] missing response for chat-advanced job_id=$job_id details=$details" >&2
    exit 1
  fi

  mem_ptr="redis://mem:${CHAT_MEMORY_ID}:events"
  mem_json="$(fetch_ptr "$mem_ptr")"
  if [ "$(echo "$mem_json" | jq -r '.kind // empty')" != "memory" ]; then
    echo "[compose-smoke] expected kind=memory got=$mem_json" >&2
    exit 1
  fi
  new_len="$(echo "$mem_json" | jq -r '.json.length // 0')"
  if ! [[ "$new_len" =~ ^[0-9]+$ ]]; then
    echo "[compose-smoke] invalid mem length: $new_len mem=$mem_json" >&2
    exit 1
  fi
  if [ "$new_len" -le "$prev_len" ]; then
    echo "[compose-smoke] expected mem length to increase prev=$prev_len got=$new_len mem=$mem_json" >&2
    exit 1
  fi
  prev_len="$new_len"
done

echo "[compose-smoke] smoke: workflow engine run (draft→refine→summarize via job.chat.advanced)"
WF_ID="wf-chat-adv-${RUN_ID}"
create_wf_body="$(jq -nc --arg id "$WF_ID" '{
  id:$id,
  name:"Chat → Refine → Summarize (Advanced) [smoke]",
  steps:{
    draft:{name:"Draft",type:"worker",topic:"job.chat.advanced",input:{prompt:"${input.prompt}"}},
    refine:{name:"Refine",type:"worker",topic:"job.chat.advanced",depends_on:["draft"],input:{prompt:"Refine: ${steps.draft.output.response}"}},
    summarize:{name:"Summarize",type:"worker",topic:"job.chat.advanced",depends_on:["refine"],input:{prompt:"Summarize: ${steps.refine.output.response}"}}
  }
}')"
curl -fsS -X POST "${API_BASE}/api/v1/workflows" \
  -H 'Content-Type: application/json' \
  -H "X-API-Key: ${API_KEY}" \
  -d "$create_wf_body" >/dev/null

WF_RUN_MEMORY_ID="smoke-wf-${RUN_ID}"
run_json="$(curl -fsS -X POST "${API_BASE}/api/v1/workflows/${WF_ID}/runs" \
  -H 'Content-Type: application/json' \
  -H "X-API-Key: ${API_KEY}" \
  -d "$(jq -nc --arg prompt 'hello workflow' --arg memory_id "$WF_RUN_MEMORY_ID" '{prompt:$prompt, memory_id:$memory_id, priority:"interactive"}')")"
run_id="$(echo "$run_json" | jq -r '.run_id')"
if [ -z "$run_id" ] || [ "$run_id" = "null" ]; then
  echo "[compose-smoke] failed to start run: $run_json" >&2
  exit 1
fi

start_run="$(date +%s)"
while true; do
  run_details="$(curl -fsS "${API_BASE}/api/v1/workflow-runs/${run_id}" -H "X-API-Key: ${API_KEY}" || true)"
  status="$(echo "$run_details" | jq -r '.status // empty')"
  if [ "$status" = "succeeded" ]; then
    break
  fi
  if [ "$status" = "failed" ] || [ "$status" = "timed_out" ] || [ "$status" = "cancelled" ]; then
    echo "[compose-smoke] workflow run failed status=$status details=$run_details" >&2
    exit 1
  fi
  if [ $(( $(date +%s) - start_run )) -ge 60 ]; then
    echo "[compose-smoke] timed out waiting for workflow run status=$status details=$run_details" >&2
    exit 1
  fi
  sleep 0.5
done

for step in draft refine summarize; do
  st="$(echo "$run_details" | jq -r --arg s "$step" '.steps[$s].status // empty')"
  if [ "$st" != "succeeded" ]; then
    echo "[compose-smoke] expected step=$step succeeded got=$st run=$run_details" >&2
    exit 1
  fi
done

echo "[compose-smoke] ✅ compose smoke succeeded"

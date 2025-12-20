#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
cd "$ROOT"

require_cmd() {
  local cmd="$1"
  if ! command -v "$cmd" >/dev/null 2>&1; then
    echo "[e2e] missing required command: $cmd" >&2
    exit 1
  fi
}

require_cmd docker
require_cmd go
require_cmd curl
require_cmd jq
require_cmd nc
require_cmd python3
require_cmd node

pick_free_port() {
  python3 - <<'PY' 2>/dev/null
import socket
s = socket.socket()
s.bind(("127.0.0.1", 0))
print(s.getsockname()[1])
s.close()
PY
}

pick_unique_port() {
  local used_csv="$1"
  while true; do
    local p
    p="$(pick_free_port)" || {
      echo "[e2e] unable to allocate a free local port (socket operation not permitted)" >&2
      exit 1
    }
    if [ -z "$p" ]; then
      echo "[e2e] unable to allocate a free local port (empty result)" >&2
      exit 1
    fi
    if [[ ",${used_csv}," != *",${p},"* ]]; then
      echo "$p"
      return 0
    fi
  done
}

wait_for_tcp() {
  local host="$1"
  local port="$2"
  local timeout_s="${3:-10}"

  local start
  start="$(date +%s)"
  while true; do
    if nc -z "$host" "$port" >/dev/null 2>&1; then
      return 0
    fi
    if [ $(( $(date +%s) - start )) -ge "$timeout_s" ]; then
      return 1
    fi
    sleep 0.1
  done
}

wait_for_http() {
  local url="$1"
  local timeout_s="${2:-15}"

  local start
  start="$(date +%s)"
  while true; do
    if curl -fsS "$url" >/dev/null 2>&1; then
      return 0
    fi
    if [ $(( $(date +%s) - start )) -ge "$timeout_s" ]; then
      return 1
    fi
    sleep 0.2
  done
}

RUN_ID="$(date +%s)-$$"
CACHE_DIR="$ROOT/.cache/e2e/$RUN_ID"
mkdir -p "$CACHE_DIR"

used_ports=""
NATS_PORT="$(pick_unique_port "$used_ports")"; used_ports="${used_ports},${NATS_PORT}"
REDIS_PORT="$(pick_unique_port "$used_ports")"; used_ports="${used_ports},${REDIS_PORT}"
SAFETY_PORT="$(pick_unique_port "$used_ports")"; used_ports="${used_ports},${SAFETY_PORT}"
CTX_PORT="$(pick_unique_port "$used_ports")"; used_ports="${used_ports},${CTX_PORT}"
OLLAMA_PORT="$(pick_unique_port "$used_ports")"; used_ports="${used_ports},${OLLAMA_PORT}"
WFENGINE_PORT="$(pick_unique_port "$used_ports")"; used_ports="${used_ports},${WFENGINE_PORT}"
GW_HTTP_PORT="$(pick_unique_port "$used_ports")"; used_ports="${used_ports},${GW_HTTP_PORT}"
GW_GRPC_PORT="$(pick_unique_port "$used_ports")"; used_ports="${used_ports},${GW_GRPC_PORT}"
GW_METRICS_PORT="$(pick_unique_port "$used_ports")"; used_ports="${used_ports},${GW_METRICS_PORT}"

NATS_CONTAINER="coretex-e2e-nats-$RUN_ID"
REDIS_CONTAINER="coretex-e2e-redis-$RUN_ID"

PIDS=()

cleanup() {
  set +e
  for pid in "${PIDS[@]:-}"; do
    kill "$pid" >/dev/null 2>&1 || true
  done
  for pid in "${PIDS[@]:-}"; do
    wait "$pid" >/dev/null 2>&1 || true
  done
  docker rm -f "$NATS_CONTAINER" >/dev/null 2>&1 || true
  docker rm -f "$REDIS_CONTAINER" >/dev/null 2>&1 || true
}
trap cleanup EXIT

export GOCACHE="$ROOT/.cache/go-build"
export GOMODCACHE="$ROOT/.gomodcache"
export GOPATH="$ROOT/.gopath"

export NATS_URL="nats://127.0.0.1:${NATS_PORT}"
export REDIS_URL="redis://127.0.0.1:${REDIS_PORT}"
export SAFETY_KERNEL_ADDR="127.0.0.1:${SAFETY_PORT}"
export CONTEXT_ENGINE_ADDR="127.0.0.1:${CTX_PORT}"
export SAFETY_POLICY_PATH="$ROOT/config/safety.yaml"
export POOL_CONFIG_PATH="$ROOT/config/pools.yaml"
export TIMEOUT_CONFIG_PATH="$ROOT/config/timeouts.yaml"
export TENANT_ID="default"

export OLLAMA_URL="http://127.0.0.1:${OLLAMA_PORT}"
export OLLAMA_MODEL="mock-model"

export CORETEX_API_KEY="super-secret-key"
export CORETEX_SUPER_SECRET_API_TOKEN="$CORETEX_API_KEY"
export WORKFLOW_ENGINE_HTTP_ADDR="127.0.0.1:${WFENGINE_PORT}"
export WORKFLOW_ENGINE_SCAN_INTERVAL="1s"
export WORKFLOW_ENGINE_RUN_SCAN_LIMIT="200"
export GATEWAY_HTTP_ADDR="127.0.0.1:${GW_HTTP_PORT}"
export GATEWAY_GRPC_ADDR="127.0.0.1:${GW_GRPC_PORT}"
export GATEWAY_METRICS_ADDR="127.0.0.1:${GW_METRICS_PORT}"

echo "[e2e] starting docker containers (nats, redis)"
docker run -d --name "$NATS_CONTAINER" -p "${NATS_PORT}:4222" nats:2.10-alpine >/dev/null
docker run -d --name "$REDIS_CONTAINER" -p "${REDIS_PORT}:6379" redis:7-alpine >/dev/null

if ! wait_for_tcp 127.0.0.1 "$NATS_PORT" 10; then
  echo "[e2e] nats did not become ready on port $NATS_PORT" >&2
  docker logs "$NATS_CONTAINER" | tail -n 200 >&2 || true
  exit 1
fi
if ! wait_for_tcp 127.0.0.1 "$REDIS_PORT" 10; then
  echo "[e2e] redis did not become ready on port $REDIS_PORT" >&2
  docker logs "$REDIS_CONTAINER" | tail -n 200 >&2 || true
  exit 1
fi

echo "[e2e] starting mock ollama server"
OLLAMA_PORT="$OLLAMA_PORT" python3 - <<'PY' >"$CACHE_DIR/ollama.log" 2>&1 &
import json
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

addr = ("127.0.0.1", int(__import__("os").environ["OLLAMA_PORT"]))
srv = HTTPServer(addr, Handler)
srv.serve_forever()
PY
PIDS+=("$!")

wait_for_tcp 127.0.0.1 "$OLLAMA_PORT" 10 || {
  echo "[e2e] mock ollama did not become ready on port $OLLAMA_PORT" >&2
  tail -n 200 "$CACHE_DIR/ollama.log" >&2 || true
  exit 1
}

echo "[e2e] starting coretex processes"
go run ./cmd/coretex-safety-kernel >"$CACHE_DIR/safety-kernel.log" 2>&1 &
PIDS+=("$!")
if ! wait_for_tcp 127.0.0.1 "$SAFETY_PORT" 10; then
  echo "[e2e] safety kernel did not become ready on port $SAFETY_PORT" >&2
  tail -n 200 "$CACHE_DIR/safety-kernel.log" >&2 || true
  exit 1
fi

go run ./cmd/coretex-context-engine >"$CACHE_DIR/context-engine.log" 2>&1 &
PIDS+=("$!")
if ! wait_for_tcp 127.0.0.1 "$CTX_PORT" 10; then
  echo "[e2e] context engine did not become ready on port $CTX_PORT" >&2
  tail -n 200 "$CACHE_DIR/context-engine.log" >&2 || true
  exit 1
fi

go run ./cmd/coretex-scheduler >"$CACHE_DIR/scheduler.log" 2>&1 &
PIDS+=("$!")

go run ./cmd/coretex-worker-echo >"$CACHE_DIR/worker-echo.log" 2>&1 &
PIDS+=("$!")
go run ./cmd/coretex-worker-chat >"$CACHE_DIR/worker-chat.log" 2>&1 &
PIDS+=("$!")
go run ./cmd/coretex-worker-chat-advanced >"$CACHE_DIR/worker-chat-advanced.log" 2>&1 &
PIDS+=("$!")
WORKER_ID="worker-code-llm-1" go run ./cmd/coretex-worker-code-llm >"$CACHE_DIR/worker-code-llm.log" 2>&1 &
PIDS+=("$!")

go run ./cmd/coretex-api-gateway >"$CACHE_DIR/api-gateway.log" 2>&1 &
PIDS+=("$!")
if ! wait_for_http "http://127.0.0.1:${GW_HTTP_PORT}/health" 20; then
  echo "[e2e] api gateway did not become ready on port $GW_HTTP_PORT" >&2
  tail -n 200 "$CACHE_DIR/api-gateway.log" >&2 || true
  exit 1
fi

go run ./cmd/coretex-workflow-engine >"$CACHE_DIR/workflow-engine.log" 2>&1 &
PIDS+=("$!")
if ! wait_for_http "http://127.0.0.1:${WFENGINE_PORT}/health" 20; then
  echo "[e2e] workflow engine did not become ready on port $WFENGINE_PORT" >&2
  tail -n 200 "$CACHE_DIR/workflow-engine.log" >&2 || true
  exit 1
fi

echo "[e2e] waiting for worker heartbeats"
start_workers="$(date +%s)"
while true; do
  WORKERS_JSON="$(curl -fsS "http://127.0.0.1:${GW_HTTP_PORT}/api/v1/workers" -H "X-API-Key: $CORETEX_API_KEY")" || true
  ok_echo=0
  ok_chat=0
  ok_adv=0
  ok_code=0
  if echo "$WORKERS_JSON" | jq -e '.[] | select(.worker_id == "worker-echo-1")' >/dev/null 2>&1; then ok_echo=1; fi
  if echo "$WORKERS_JSON" | jq -e '.[] | select(.worker_id == "worker-chat-1")' >/dev/null 2>&1; then ok_chat=1; fi
  if echo "$WORKERS_JSON" | jq -e '.[] | select(.worker_id == "worker-chat-advanced-1")' >/dev/null 2>&1; then ok_adv=1; fi
  if echo "$WORKERS_JSON" | jq -e '.[] | select(.worker_id == "worker-code-llm-1")' >/dev/null 2>&1; then ok_code=1; fi

  if [ "$ok_echo" -eq 1 ] && [ "$ok_chat" -eq 1 ] && [ "$ok_adv" -eq 1 ] && [ "$ok_code" -eq 1 ]; then
    echo "[e2e] all workers registered"
    break
  fi
  if [ $(( $(date +%s) - start_workers )) -ge 20 ]; then
    echo "[e2e] ❌ timed out waiting for workers" >&2
    echo "[e2e] workers response: $WORKERS_JSON" >&2
    tail -n 200 "$CACHE_DIR/scheduler.log" >&2 || true
    tail -n 200 "$CACHE_DIR/worker-echo.log" >&2 || true
    tail -n 200 "$CACHE_DIR/worker-chat.log" >&2 || true
    tail -n 200 "$CACHE_DIR/worker-chat-advanced.log" >&2 || true
    tail -n 200 "$CACHE_DIR/worker-code-llm.log" >&2 || true
    exit 1
  fi
  sleep 0.2
done

echo "[e2e] starting ws stream capture"
WS_URL="ws://127.0.0.1:${GW_HTTP_PORT}/api/v1/stream?api_key=${CORETEX_API_KEY}"
WS_LOG="$CACHE_DIR/ws.jsonl"
WS_ERR="$CACHE_DIR/ws.err.log"
WS_URL="$WS_URL" node - <<'NODE' >"$WS_LOG" 2>"$WS_ERR" &
const url = process.env.WS_URL;
if (!url) {
  console.error("missing WS_URL");
  process.exit(2);
}

let connected = false;
const ws = new WebSocket(url);
ws.onopen = () => {
  connected = true;
  console.error("[ws] connected");
};
ws.onerror = (e) => {
  console.error("[ws] error", e);
};
ws.onclose = () => {
  console.error("[ws] closed");
  if (!connected) {
    process.exit(1);
  }
};
ws.onmessage = (evt) => {
  if (typeof evt.data === "string") {
    process.stdout.write(evt.data + "\n");
  } else {
    process.stdout.write(String(evt.data) + "\n");
  }
};
setInterval(() => {}, 1000);
NODE
PIDS+=("$!")

echo "[e2e] waiting for ws connection"
start_ws_conn="$(date +%s)"
while true; do
  if [ -f "$WS_ERR" ] && grep -q "\\[ws\\] connected" "$WS_ERR" 2>/dev/null; then
    break
  fi
  if [ $(( $(date +%s) - start_ws_conn )) -ge 8 ]; then
    echo "[e2e] ws did not connect in time" >&2
    tail -n 200 "$WS_ERR" >&2 || true
    exit 1
  fi
  sleep 0.1
done

submit_job() {
  local topic="$1"
  local prompt="$2"
  local out_json
  out_json="$(curl -fsS -X POST "http://127.0.0.1:${GW_HTTP_PORT}/api/v1/jobs" \
    -H 'Content-Type: application/json' \
    -H "X-API-Key: $CORETEX_API_KEY" \
    -d "$(jq -nc --arg topic "$topic" --arg prompt "$prompt" '{topic:$topic,prompt:$prompt,priority:"interactive"}')")"
  local job_id trace_id
  job_id="$(echo "$out_json" | jq -r '.job_id')"
  trace_id="$(echo "$out_json" | jq -r '.trace_id')"
  if [ -z "$job_id" ] || [ "$job_id" = "null" ]; then
    echo "[e2e] failed to parse job_id from response: $out_json" >&2
    exit 1
  fi
  if [ -z "$trace_id" ] || [ "$trace_id" = "null" ]; then
    echo "[e2e] failed to parse trace_id from response: $out_json" >&2
    exit 1
  fi
  echo "$job_id $trace_id"
}

wait_succeeded() {
  local job_id="$1"
  local timeout_s="${2:-25}"
  local start
  start="$(date +%s)"
  while true; do
    local details state
    details="$(curl -fsS "http://127.0.0.1:${GW_HTTP_PORT}/api/v1/jobs/${job_id}" -H "X-API-Key: $CORETEX_API_KEY")" || true
    state="$(echo "$details" | jq -r '.state // empty')"
    if [ "$state" = "SUCCEEDED" ]; then
      echo "$details"
      return 0
    fi
    if [ "$state" = "FAILED" ] || [ "$state" = "TIMEOUT" ] || [ "$state" = "DENIED" ] || [ "$state" = "CANCELLED" ]; then
      echo "[e2e] ❌ terminal state=$state job_id=$job_id details=$details" >&2
      return 1
    fi
    if [ $(( $(date +%s) - start )) -ge "$timeout_s" ]; then
      echo "[e2e] ❌ timed out waiting for job completion job_id=$job_id state=$state" >&2
      return 1
    fi
    sleep 0.2
  done
}

echo "[e2e] submitting jobs"
read -r ECHO_JOB ECHO_TRACE < <(submit_job "job.echo" "hello from e2e")
read -r CHAT_JOB CHAT_TRACE < <(submit_job "job.chat.simple" "hello from chat e2e")
read -r ADV_JOB ADV_TRACE < <(submit_job "job.chat.advanced" "hello from chat-advanced e2e")
read -r CODE_JOB CODE_TRACE < <(submit_job "job.code.llm" "Generate a unified diff that adds logging to func main() {}")

echo "[e2e] waiting for echo completion"
ECHO_DETAILS="$(wait_succeeded "$ECHO_JOB" 25)" || {
  tail -n 200 "$CACHE_DIR/scheduler.log" >&2 || true
  tail -n 200 "$CACHE_DIR/api-gateway.log" >&2 || true
  exit 1
}
if [ "$(echo "$ECHO_DETAILS" | jq -r '.trace_id // empty')" != "$ECHO_TRACE" ]; then
  echo "[e2e] echo trace mismatch details=$ECHO_DETAILS" >&2
  exit 1
fi
if [ "$(echo "$ECHO_DETAILS" | jq -r '.context_ptr // empty')" != "redis://ctx:${ECHO_JOB}" ]; then
  echo "[e2e] echo context_ptr mismatch details=$ECHO_DETAILS" >&2
  exit 1
fi
if [ "$(echo "$ECHO_DETAILS" | jq -r '.result_ptr // empty')" != "redis://res:${ECHO_JOB}" ]; then
  echo "[e2e] echo result_ptr mismatch details=$ECHO_DETAILS" >&2
  exit 1
fi
if [ "$(echo "$ECHO_DETAILS" | jq -r '.result.processed_by // empty')" != "worker-echo-1" ]; then
  echo "[e2e] echo processed_by mismatch details=$ECHO_DETAILS" >&2
  exit 1
fi

echo "[e2e] validating /api/v1/memory pointer fetch"
ECHO_CTX_PTR="$(echo "$ECHO_DETAILS" | jq -r '.context_ptr // empty')"
ENCODED_CTX_PTR="$(jq -rn --arg v "$ECHO_CTX_PTR" '$v|@uri')"
MEM_CTX="$(curl -fsS "http://127.0.0.1:${GW_HTTP_PORT}/api/v1/memory?ptr=${ENCODED_CTX_PTR}" -H "X-API-Key: $CORETEX_API_KEY")"
if [ "$(echo "$MEM_CTX" | jq -r '.kind // empty')" != "context" ]; then
  echo "[e2e] expected memory.kind=context got=$(echo "$MEM_CTX" | jq -r '.kind // empty') mem=$MEM_CTX" >&2
  exit 1
fi
if [ "$(echo "$MEM_CTX" | jq -r '.json.prompt // empty')" != "hello from e2e" ]; then
  echo "[e2e] expected memory.json.prompt='hello from e2e' got=$(echo "$MEM_CTX" | jq -r '.json.prompt // empty') mem=$MEM_CTX" >&2
  exit 1
fi
ECHO_RES_PTR="$(echo "$ECHO_DETAILS" | jq -r '.result_ptr // empty')"
ENCODED_RES_PTR="$(jq -rn --arg v "$ECHO_RES_PTR" '$v|@uri')"
MEM_RES="$(curl -fsS "http://127.0.0.1:${GW_HTTP_PORT}/api/v1/memory?ptr=${ENCODED_RES_PTR}" -H "X-API-Key: $CORETEX_API_KEY")"
if [ "$(echo "$MEM_RES" | jq -r '.kind // empty')" != "result" ]; then
  echo "[e2e] expected memory.kind=result got=$(echo "$MEM_RES" | jq -r '.kind // empty') mem=$MEM_RES" >&2
  exit 1
fi
if [ "$(echo "$MEM_RES" | jq -r '.json.processed_by // empty')" != "worker-echo-1" ]; then
  echo "[e2e] expected memory.json.processed_by=worker-echo-1 got=$(echo "$MEM_RES" | jq -r '.json.processed_by // empty') mem=$MEM_RES" >&2
  exit 1
fi

echo "[e2e] waiting for chat-simple completion"
CHAT_DETAILS="$(wait_succeeded "$CHAT_JOB" 25)" || exit 1
if [ "$(echo "$CHAT_DETAILS" | jq -r '.trace_id // empty')" != "$CHAT_TRACE" ]; then
  echo "[e2e] chat trace mismatch details=$CHAT_DETAILS" >&2
  exit 1
fi
if [ "$(echo "$CHAT_DETAILS" | jq -r '.context_ptr // empty')" != "redis://ctx:${CHAT_JOB}" ]; then
  echo "[e2e] chat context_ptr mismatch details=$CHAT_DETAILS" >&2
  exit 1
fi
if [ "$(echo "$CHAT_DETAILS" | jq -r '.result_ptr // empty')" != "redis://res:${CHAT_JOB}" ]; then
  echo "[e2e] chat result_ptr mismatch details=$CHAT_DETAILS" >&2
  exit 1
fi
if [ "$(echo "$CHAT_DETAILS" | jq -r '.context.prompt // empty')" != "hello from chat e2e" ]; then
  echo "[e2e] chat context.prompt mismatch details=$CHAT_DETAILS" >&2
  exit 1
fi
if [ "$(echo "$CHAT_DETAILS" | jq -r '.result.processed_by // empty')" != "worker-chat-1" ]; then
  echo "[e2e] chat processed_by mismatch details=$CHAT_DETAILS" >&2
  exit 1
fi
if [ "$(echo "$CHAT_DETAILS" | jq -r '.result.response // empty')" != "[mock] hello from chat e2e" ]; then
  echo "[e2e] chat response mismatch details=$CHAT_DETAILS" >&2
  exit 1
fi

echo "[e2e] waiting for chat-advanced completion"
ADV_DETAILS="$(wait_succeeded "$ADV_JOB" 25)" || exit 1
if [ "$(echo "$ADV_DETAILS" | jq -r '.trace_id // empty')" != "$ADV_TRACE" ]; then
  echo "[e2e] chat-advanced trace mismatch details=$ADV_DETAILS" >&2
  exit 1
fi
if [ "$(echo "$ADV_DETAILS" | jq -r '.context_ptr // empty')" != "redis://ctx:${ADV_JOB}" ]; then
  echo "[e2e] chat-advanced context_ptr mismatch details=$ADV_DETAILS" >&2
  exit 1
fi
if [ "$(echo "$ADV_DETAILS" | jq -r '.result_ptr // empty')" != "redis://res:${ADV_JOB}" ]; then
  echo "[e2e] chat-advanced result_ptr mismatch details=$ADV_DETAILS" >&2
  exit 1
fi
if [ "$(echo "$ADV_DETAILS" | jq -r '.context.prompt // empty')" != "hello from chat-advanced e2e" ]; then
  echo "[e2e] chat-advanced context.prompt mismatch details=$ADV_DETAILS" >&2
  exit 1
fi
if [ "$(echo "$ADV_DETAILS" | jq -r '.result.processed_by // empty')" != "worker-chat-advanced-1" ]; then
  echo "[e2e] chat-advanced processed_by mismatch details=$ADV_DETAILS" >&2
  exit 1
fi
ADV_RESP="$(echo "$ADV_DETAILS" | jq -r '.result.response // empty')"
if [[ "$ADV_RESP" != "[mock] "* ]]; then
  echo "[e2e] chat-advanced response mismatch details=$ADV_DETAILS" >&2
  exit 1
fi

echo "[e2e] waiting for code-llm completion"
CODE_DETAILS="$(wait_succeeded "$CODE_JOB" 30)" || exit 1
if [ "$(echo "$CODE_DETAILS" | jq -r '.trace_id // empty')" != "$CODE_TRACE" ]; then
  echo "[e2e] code-llm trace mismatch details=$CODE_DETAILS" >&2
  exit 1
fi
if [ "$(echo "$CODE_DETAILS" | jq -r '.context_ptr // empty')" != "redis://ctx:${CODE_JOB}" ]; then
  echo "[e2e] code-llm context_ptr mismatch details=$CODE_DETAILS" >&2
  exit 1
fi
if [ "$(echo "$CODE_DETAILS" | jq -r '.result_ptr // empty')" != "redis://res:${CODE_JOB}" ]; then
  echo "[e2e] code-llm result_ptr mismatch details=$CODE_DETAILS" >&2
  exit 1
fi
if [ "$(echo "$CODE_DETAILS" | jq -r '.context.prompt // empty')" != "Generate a unified diff that adds logging to func main() {}" ]; then
  echo "[e2e] code-llm context.prompt mismatch details=$CODE_DETAILS" >&2
  exit 1
fi
if [ "$(echo "$CODE_DETAILS" | jq -r '.result.processed_by // empty')" != "worker-code-llm-1" ]; then
  echo "[e2e] code-llm processed_by mismatch details=$CODE_DETAILS" >&2
  exit 1
fi
CODE_PATCH="$(echo "$CODE_DETAILS" | jq -r '.result.patch.content // empty')"
if [[ "$CODE_PATCH" != *"adds logging"* ]] && [[ "$CODE_PATCH" != *"logging"* ]]; then
  echo "[e2e] code-llm patch did not include input prompt details=$CODE_DETAILS" >&2
  exit 1
fi

echo "[e2e] creating approval workflow and running it"
WF_ID="e2e-approval-$RUN_ID"
WF_DEF="$(jq -nc --arg id "$WF_ID" '{
  id: $id,
  org_id: "default",
  name: "E2E Approval Workflow",
  version: "v1",
  timeout_sec: 60,
  created_by: "e2e",
  steps: {
    approve: {
      name: "Approval gate",
      type: "approval"
    },
    echo: {
      name: "Echo",
      type: "worker",
      topic: "job.echo",
      depends_on: ["approve"],
      input: {prompt: "${input.prompt}"},
      output_path: "output.echo"
    }
  }
}')"

curl -fsS -X POST "http://127.0.0.1:${GW_HTTP_PORT}/api/v1/workflows" \
  -H 'Content-Type: application/json' \
  -H "X-API-Key: $CORETEX_API_KEY" \
  -d "$WF_DEF" >/dev/null

WF_RUN_JSON="$(curl -fsS -X POST "http://127.0.0.1:${GW_HTTP_PORT}/api/v1/workflows/${WF_ID}/runs" \
  -H 'Content-Type: application/json' \
  -H "X-API-Key: $CORETEX_API_KEY" \
  -d '{"prompt":"hello from workflow e2e"}')"
WF_RUN_ID="$(echo "$WF_RUN_JSON" | jq -r '.run_id')"
if [ -z "$WF_RUN_ID" ] || [ "$WF_RUN_ID" = "null" ]; then
  echo "[e2e] failed to parse run_id: $WF_RUN_JSON" >&2
  exit 1
fi
WF_JOB="${WF_RUN_ID}:echo@1"

echo "[e2e] waiting for workflow run to enter waiting state"
start_wf_wait="$(date +%s)"
while true; do
  WF_RUN_DETAILS="$(curl -fsS "http://127.0.0.1:${GW_HTTP_PORT}/api/v1/workflow-runs/${WF_RUN_ID}" -H "X-API-Key: $CORETEX_API_KEY")" || true
  WF_STATUS="$(echo "$WF_RUN_DETAILS" | jq -r '.status // empty')"
  if [ "$WF_STATUS" = "waiting" ]; then
    APPROVE_STATUS="$(echo "$WF_RUN_DETAILS" | jq -r '.steps.approve.status // empty')"
    if [ "$APPROVE_STATUS" != "waiting" ]; then
      echo "[e2e] expected approve step waiting, got $APPROVE_STATUS run=$WF_RUN_DETAILS" >&2
      exit 1
    fi
    break
  fi
  if [ $(( $(date +%s) - start_wf_wait )) -ge 10 ]; then
    echo "[e2e] ❌ timed out waiting for workflow run waiting (status=$WF_STATUS) details=$WF_RUN_DETAILS" >&2
    tail -n 200 "$CACHE_DIR/workflow-engine.log" >&2 || true
    exit 1
  fi
  sleep 0.2
done

echo "[e2e] approving workflow step"
start_approve="$(date +%s)"
while true; do
  code="$(curl -sS -o /dev/null -w '%{http_code}' -X POST "http://127.0.0.1:${GW_HTTP_PORT}/api/v1/workflows/${WF_ID}/runs/${WF_RUN_ID}/steps/approve/approve" \
    -H 'Content-Type: application/json' \
    -H "X-API-Key: $CORETEX_API_KEY" \
    -d '{"approved":true}' || true)"
  if [ "$code" = "204" ]; then
    break
  fi
  if [ "$code" != "409" ] && [ "$code" != "000" ]; then
    echo "[e2e] approve failed http=$code" >&2
    exit 1
  fi
  if [ $(( $(date +%s) - start_approve )) -ge 10 ]; then
    echo "[e2e] ❌ timed out waiting to approve step (last http=$code)" >&2
    exit 1
  fi
  sleep 0.2
done

echo "[e2e] waiting for workflow run to succeed"
start_wf_done="$(date +%s)"
while true; do
  WF_RUN_DETAILS="$(curl -fsS "http://127.0.0.1:${GW_HTTP_PORT}/api/v1/workflow-runs/${WF_RUN_ID}" -H "X-API-Key: $CORETEX_API_KEY")" || true
  WF_STATUS="$(echo "$WF_RUN_DETAILS" | jq -r '.status // empty')"
  if [ "$WF_STATUS" = "succeeded" ]; then
    ECHO_STEP_STATUS="$(echo "$WF_RUN_DETAILS" | jq -r '.steps.echo.status // empty')"
    ECHO_STEP_JOB="$(echo "$WF_RUN_DETAILS" | jq -r '.steps.echo.job_id // empty')"
    if [ "$ECHO_STEP_STATUS" != "succeeded" ] || [ "$ECHO_STEP_JOB" != "$WF_JOB" ]; then
      echo "[e2e] unexpected echo step state status=$ECHO_STEP_STATUS job_id=$ECHO_STEP_JOB details=$WF_RUN_DETAILS" >&2
      exit 1
    fi
    WF_OUT_JOB="$(echo "$WF_RUN_DETAILS" | jq -r '.context.output.echo.job_id // empty')"
    if [ "$WF_OUT_JOB" != "$WF_JOB" ]; then
      echo "[e2e] expected context.output.echo.job_id=$WF_JOB got=$WF_OUT_JOB" >&2
      exit 1
    fi
    break
  fi
  if [ "$WF_STATUS" = "failed" ] || [ "$WF_STATUS" = "cancelled" ] || [ "$WF_STATUS" = "timed_out" ]; then
    echo "[e2e] ❌ workflow run terminal status=$WF_STATUS details=$WF_RUN_DETAILS" >&2
    tail -n 200 "$CACHE_DIR/workflow-engine.log" >&2 || true
    exit 1
  fi
  if [ $(( $(date +%s) - start_wf_done )) -ge 30 ]; then
    echo "[e2e] ❌ timed out waiting for workflow run completion (status=$WF_STATUS)" >&2
    tail -n 200 "$CACHE_DIR/workflow-engine.log" >&2 || true
    exit 1
  fi
  sleep 0.2
done

echo "[e2e] validating workflow step job detail"
WF_JOB_DETAILS="$(wait_succeeded "$WF_JOB" 25)" || {
  tail -n 200 "$CACHE_DIR/workflow-engine.log" >&2 || true
  exit 1
}
if [ "$(echo "$WF_JOB_DETAILS" | jq -r '.trace_id // empty')" != "$WF_RUN_ID" ]; then
  echo "[e2e] expected workflow job trace_id=$WF_RUN_ID got=$(echo "$WF_JOB_DETAILS" | jq -r '.trace_id // empty')" >&2
  exit 1
fi
if [ "$(echo "$WF_JOB_DETAILS" | jq -r '.result.processed_by // empty')" != "worker-echo-1" ]; then
  echo "[e2e] expected workflow job processed_by worker-echo-1 got=$(echo "$WF_JOB_DETAILS" | jq -r '.result.processed_by // empty')" >&2
  exit 1
fi

echo "[e2e] creating a denied run (approval=false)"
WF_RUN2_JSON="$(curl -fsS -X POST "http://127.0.0.1:${GW_HTTP_PORT}/api/v1/workflows/${WF_ID}/runs" \
  -H 'Content-Type: application/json' \
  -H "X-API-Key: $CORETEX_API_KEY" \
  -d '{"prompt":"deny me"}')"
WF_RUN2_ID="$(echo "$WF_RUN2_JSON" | jq -r '.run_id')"
if [ -z "$WF_RUN2_ID" ] || [ "$WF_RUN2_ID" = "null" ]; then
  echo "[e2e] failed to parse run_id: $WF_RUN2_JSON" >&2
  exit 1
fi
echo "[e2e] waiting for denied run to enter waiting state"
start_wf_wait2="$(date +%s)"
while true; do
  WF_RUN2_DETAILS="$(curl -fsS "http://127.0.0.1:${GW_HTTP_PORT}/api/v1/workflow-runs/${WF_RUN2_ID}" -H "X-API-Key: $CORETEX_API_KEY")" || true
  WF2_STATUS="$(echo "$WF_RUN2_DETAILS" | jq -r '.status // empty')"
  if [ "$WF2_STATUS" = "waiting" ]; then
    break
  fi
  if [ $(( $(date +%s) - start_wf_wait2 )) -ge 10 ]; then
    echo "[e2e] ❌ timed out waiting for denied run waiting (status=$WF2_STATUS) details=$WF_RUN2_DETAILS" >&2
    exit 1
  fi
  sleep 0.2
done

echo "[e2e] denying workflow step"
code="$(curl -sS -o /dev/null -w '%{http_code}' -X POST "http://127.0.0.1:${GW_HTTP_PORT}/api/v1/workflows/${WF_ID}/runs/${WF_RUN2_ID}/steps/approve/approve" \
  -H 'Content-Type: application/json' \
  -H "X-API-Key: $CORETEX_API_KEY" \
  -d '{"approved":false}' || true)"
if [ "$code" != "204" ]; then
  echo "[e2e] deny failed http=$code" >&2
  exit 1
fi

echo "[e2e] waiting for denied run to fail"
start_wf_done2="$(date +%s)"
while true; do
  WF_RUN2_DETAILS="$(curl -fsS "http://127.0.0.1:${GW_HTTP_PORT}/api/v1/workflow-runs/${WF_RUN2_ID}" -H "X-API-Key: $CORETEX_API_KEY")" || true
  WF2_STATUS="$(echo "$WF_RUN2_DETAILS" | jq -r '.status // empty')"
  if [ "$WF2_STATUS" = "failed" ]; then
    APPROVE2_STATUS="$(echo "$WF_RUN2_DETAILS" | jq -r '.steps.approve.status // empty')"
    if [ "$APPROVE2_STATUS" != "failed" ]; then
      echo "[e2e] expected approve step failed, got $APPROVE2_STATUS run=$WF_RUN2_DETAILS" >&2
      exit 1
    fi
    break
  fi
  if [ $(( $(date +%s) - start_wf_done2 )) -ge 10 ]; then
    echo "[e2e] ❌ timed out waiting for denied run to fail (status=$WF2_STATUS) details=$WF_RUN2_DETAILS" >&2
    exit 1
  fi
  sleep 0.2
done

echo "[e2e] validating /api/v1/jobs list includes trace_id"
LIST_JSON="$(curl -fsS "http://127.0.0.1:${GW_HTTP_PORT}/api/v1/jobs?limit=200" -H "X-API-Key: $CORETEX_API_KEY")"
for pair in "$ECHO_JOB:$ECHO_TRACE" "$CHAT_JOB:$CHAT_TRACE" "$ADV_JOB:$ADV_TRACE" "$CODE_JOB:$CODE_TRACE" "$WF_JOB:$WF_RUN_ID"; do
  job_id="${pair%:*}"
  trace_id="${pair##*:}"
  got_trace="$(echo "$LIST_JSON" | jq -r --arg job "$job_id" '.items[] | select(.id == $job) | .trace_id // empty')"
  if [ "$got_trace" != "$trace_id" ]; then
    echo "[e2e] expected list trace_id=$trace_id got=$got_trace job_id=$job_id" >&2
    exit 1
  fi
done

echo "[e2e] validating trace endpoints list each job"
for pair in "$ECHO_JOB:$ECHO_TRACE" "$CHAT_JOB:$CHAT_TRACE" "$ADV_JOB:$ADV_TRACE" "$CODE_JOB:$CODE_TRACE" "$WF_JOB:$WF_RUN_ID"; do
  job_id="${pair%:*}"
  trace_id="${pair##*:}"
  TRACE_JSON="$(curl -fsS "http://127.0.0.1:${GW_HTTP_PORT}/api/v1/traces/${trace_id}" -H "X-API-Key: $CORETEX_API_KEY")"
  if ! echo "$TRACE_JSON" | jq -e --arg job "$job_id" '.[] | select(.id == $job)' >/dev/null 2>&1; then
    echo "[e2e] trace endpoint missing job_id=$job_id trace_id=$trace_id resp=$TRACE_JSON" >&2
    exit 1
  fi
done

echo "[e2e] validating ws stream saw heartbeats + job results"
echo "[e2e] waiting for ws heartbeat (up to 8s)"
start_ws="$(date +%s)"
while true; do
  HEARTBEATS="$(jq -s '[.[] | select(.heartbeat != null)] | length' "$WS_LOG" 2>/dev/null || echo 0)"
  if [ "$HEARTBEATS" -ge 1 ]; then
    break
  fi
  if [ $(( $(date +%s) - start_ws )) -ge 8 ]; then
    echo "[e2e] expected at least 1 heartbeat event in ws log" >&2
    tail -n 50 "$WS_LOG" >&2 || true
    tail -n 200 "$WS_ERR" >&2 || true
    exit 1
  fi
  sleep 0.5
done

for job_id in "$ECHO_JOB" "$CHAT_JOB" "$ADV_JOB" "$CODE_JOB" "$WF_JOB"; do
  JOB_RES_COUNT="$(jq -s --arg job "$job_id" '[.[] | select(.jobResult != null and .jobResult.jobId == $job)] | length' "$WS_LOG" 2>/dev/null || echo 0)"
  if [ "$JOB_RES_COUNT" -lt 1 ]; then
    echo "[e2e] expected ws jobResult for job_id=$job_id" >&2
    tail -n 50 "$WS_LOG" >&2 || true
    tail -n 200 "$WS_ERR" >&2 || true
    exit 1
  fi
done

echo "[e2e] ✅ full gateway e2e succeeded"

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
export SAFETY_POLICY_PATH="$ROOT/config/safety.yaml"
export POOL_CONFIG_PATH="$ROOT/config/pools.yaml"
export TIMEOUT_CONFIG_PATH="$ROOT/config/timeouts.yaml"
export TENANT_ID="default"

export CORETEX_API_KEY="super-secret-key"
export CORETEX_SUPER_SECRET_API_TOKEN="$CORETEX_API_KEY"
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

echo "[e2e] starting coretex processes"
go run ./cmd/coretex-safety-kernel >"$CACHE_DIR/safety-kernel.log" 2>&1 &
PIDS+=("$!")
if ! wait_for_tcp 127.0.0.1 "$SAFETY_PORT" 10; then
  echo "[e2e] safety kernel did not become ready on port $SAFETY_PORT" >&2
  tail -n 200 "$CACHE_DIR/safety-kernel.log" >&2 || true
  exit 1
fi

go run ./cmd/coretex-scheduler >"$CACHE_DIR/scheduler.log" 2>&1 &
PIDS+=("$!")

go run ./cmd/coretex-worker-echo >"$CACHE_DIR/worker-echo.log" 2>&1 &
PIDS+=("$!")

go run ./cmd/coretex-api-gateway >"$CACHE_DIR/api-gateway.log" 2>&1 &
PIDS+=("$!")
if ! wait_for_http "http://127.0.0.1:${GW_HTTP_PORT}/health" 20; then
  echo "[e2e] api gateway did not become ready on port $GW_HTTP_PORT" >&2
  tail -n 200 "$CACHE_DIR/api-gateway.log" >&2 || true
  exit 1
fi

echo "[e2e] waiting for echo worker heartbeat"
start_workers="$(date +%s)"
while true; do
  WORKERS_JSON="$(curl -fsS "http://127.0.0.1:${GW_HTTP_PORT}/api/v1/workers" -H "X-API-Key: $CORETEX_API_KEY")" || true
  if echo "$WORKERS_JSON" | jq -e '.[] | select(.worker_id == "worker-echo-1")' >/dev/null 2>&1; then
    echo "[e2e] echo worker registered"
    break
  fi
  if [ $(( $(date +%s) - start_workers )) -ge 15 ]; then
    echo "[e2e] ❌ timed out waiting for echo worker heartbeat" >&2
    echo "[e2e] workers response: $WORKERS_JSON" >&2
    tail -n 200 "$CACHE_DIR/worker-echo.log" >&2 || true
    tail -n 200 "$CACHE_DIR/scheduler.log" >&2 || true
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

echo "[e2e] submitting job via gateway"
JOB_JSON="$(curl -fsS -X POST "http://127.0.0.1:${GW_HTTP_PORT}/api/v1/jobs" \
  -H 'Content-Type: application/json' \
  -H "X-API-Key: $CORETEX_API_KEY" \
  -d '{"topic":"job.echo","prompt":"hello from e2e","priority":"interactive","adapter_id":"echo-adapter"}')"
JOB_ID="$(echo "$JOB_JSON" | jq -r '.job_id')"
TRACE_ID="$(echo "$JOB_JSON" | jq -r '.trace_id')"
if [ -z "$JOB_ID" ] || [ "$JOB_ID" = "null" ]; then
  echo "[e2e] failed to parse job_id from response: $JOB_JSON" >&2
  exit 1
fi
if [ -z "$TRACE_ID" ] || [ "$TRACE_ID" = "null" ]; then
  echo "[e2e] failed to parse trace_id from response: $JOB_JSON" >&2
  exit 1
fi
echo "[e2e] job_id=$JOB_ID"
echo "[e2e] trace_id=$TRACE_ID"

echo "[e2e] waiting for completion"
start="$(date +%s)"
while true; do
  DETAILS="$(curl -fsS "http://127.0.0.1:${GW_HTTP_PORT}/api/v1/jobs/${JOB_ID}" -H "X-API-Key: $CORETEX_API_KEY")" || true
  STATE="$(echo "$DETAILS" | jq -r '.state // empty')"
  if [ "$STATE" = "SUCCEEDED" ]; then
    PROCESSED_BY="$(echo "$DETAILS" | jq -r '.result.processed_by // empty')"
    if [ "$PROCESSED_BY" != "worker-echo-1" ]; then
      echo "[e2e] unexpected processed_by=$PROCESSED_BY details=$DETAILS" >&2
      exit 1
    fi

    echo "[e2e] validating job detail fields"
    DETAIL_TRACE="$(echo "$DETAILS" | jq -r '.trace_id // empty')"
    if [ "$DETAIL_TRACE" != "$TRACE_ID" ]; then
      echo "[e2e] expected job.trace_id=$TRACE_ID got=$DETAIL_TRACE details=$DETAILS" >&2
      exit 1
    fi
    CTX_PTR="$(echo "$DETAILS" | jq -r '.context_ptr // empty')"
    if [ "$CTX_PTR" != "redis://ctx:${JOB_ID}" ]; then
      echo "[e2e] expected context_ptr=redis://ctx:${JOB_ID} got=$CTX_PTR" >&2
      exit 1
    fi
	    CTX_PROMPT="$(echo "$DETAILS" | jq -r '.context.prompt // empty')"
	    if [ "$CTX_PROMPT" != "hello from e2e" ]; then
	      echo "[e2e] expected context.prompt='hello from e2e' got='$CTX_PROMPT'" >&2
	      exit 1
	    fi

	    RESULT_PTR="$(echo "$DETAILS" | jq -r '.result_ptr // empty')"
	    if [ "$RESULT_PTR" != "redis://res:${JOB_ID}" ]; then
	      echo "[e2e] expected result_ptr=redis://res:${JOB_ID} got=$RESULT_PTR" >&2
	      exit 1
	    fi

	    echo "[e2e] validating /api/v1/memory pointer fetch"
	    ENCODED_CTX_PTR="$(jq -rn --arg v "$CTX_PTR" '$v|@uri')"
	    MEM_CTX="$(curl -fsS "http://127.0.0.1:${GW_HTTP_PORT}/api/v1/memory?ptr=${ENCODED_CTX_PTR}" -H "X-API-Key: $CORETEX_API_KEY")"
	    if [ "$(echo "$MEM_CTX" | jq -r '.kind // empty')" != "context" ]; then
	      echo "[e2e] expected memory.kind=context got=$(echo "$MEM_CTX" | jq -r '.kind // empty') mem=$MEM_CTX" >&2
	      exit 1
	    fi
	    if [ "$(echo "$MEM_CTX" | jq -r '.json.prompt // empty')" != "hello from e2e" ]; then
	      echo "[e2e] expected memory.json.prompt='hello from e2e' got=$(echo "$MEM_CTX" | jq -r '.json.prompt // empty') mem=$MEM_CTX" >&2
	      exit 1
	    fi
	    ENCODED_RES_PTR="$(jq -rn --arg v "$RESULT_PTR" '$v|@uri')"
	    MEM_RES="$(curl -fsS "http://127.0.0.1:${GW_HTTP_PORT}/api/v1/memory?ptr=${ENCODED_RES_PTR}" -H "X-API-Key: $CORETEX_API_KEY")"
	    if [ "$(echo "$MEM_RES" | jq -r '.kind // empty')" != "result" ]; then
	      echo "[e2e] expected memory.kind=result got=$(echo "$MEM_RES" | jq -r '.kind // empty') mem=$MEM_RES" >&2
	      exit 1
	    fi
	    if [ "$(echo "$MEM_RES" | jq -r '.json.processed_by // empty')" != "worker-echo-1" ]; then
	      echo "[e2e] expected memory.json.processed_by=worker-echo-1 got=$(echo "$MEM_RES" | jq -r '.json.processed_by // empty') mem=$MEM_RES" >&2
	      exit 1
	    fi

	    echo "[e2e] validating /api/v1/jobs list includes trace_id"
	    LIST_JSON="$(curl -fsS "http://127.0.0.1:${GW_HTTP_PORT}/api/v1/jobs?limit=200" -H "X-API-Key: $CORETEX_API_KEY")"
	    LIST_TRACE="$(echo "$LIST_JSON" | jq -r --arg job "$JOB_ID" '.items[] | select(.id == $job) | .trace_id // empty')"
    if [ "$LIST_TRACE" != "$TRACE_ID" ]; then
      echo "[e2e] expected list trace_id=$TRACE_ID got=$LIST_TRACE list=$LIST_JSON" >&2
      exit 1
    fi

    echo "[e2e] validating trace endpoint lists job"
    TRACE_JSON="$(curl -fsS "http://127.0.0.1:${GW_HTTP_PORT}/api/v1/traces/${TRACE_ID}" -H "X-API-Key: $CORETEX_API_KEY")"
    if ! echo "$TRACE_JSON" | jq -e --arg job "$JOB_ID" '.[] | select(.id == $job)' >/dev/null 2>&1; then
      echo "[e2e] trace endpoint missing job_id=$JOB_ID trace=$TRACE_ID resp=$TRACE_JSON" >&2
      exit 1
    fi

    echo "[e2e] validating ws stream saw heartbeat + job result"
    if [ ! -s "$WS_LOG" ]; then
      echo "[e2e] ws log is empty (stderr follows)" >&2
      tail -n 200 "$WS_ERR" >&2 || true
      exit 1
    fi
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
    JOB_RES_COUNT="$(jq -s --arg job "$JOB_ID" '[.[] | select(.jobResult != null and .jobResult.jobId == $job)] | length' "$WS_LOG" 2>/dev/null || echo 0)"
    if [ "$JOB_RES_COUNT" -lt 1 ]; then
      echo "[e2e] expected ws jobResult for job_id=$JOB_ID" >&2
      tail -n 50 "$WS_LOG" >&2 || true
      tail -n 200 "$WS_ERR" >&2 || true
      exit 1
    fi
    echo "[e2e] ✅ succeeded processed_by=$PROCESSED_BY"
    exit 0
  fi
  if [ "$STATE" = "FAILED" ] || [ "$STATE" = "TIMEOUT" ] || [ "$STATE" = "DENIED" ] || [ "$STATE" = "CANCELLED" ]; then
    echo "[e2e] ❌ terminal state=$STATE details=$DETAILS" >&2
    tail -n 200 "$CACHE_DIR/scheduler.log" >&2 || true
    tail -n 200 "$CACHE_DIR/worker-echo.log" >&2 || true
    exit 1
  fi
  if [ $(( $(date +%s) - start )) -ge 20 ]; then
    echo "[e2e] ❌ timed out waiting for job completion (state=$STATE)" >&2
    tail -n 200 "$CACHE_DIR/scheduler.log" >&2 || true
    tail -n 200 "$CACHE_DIR/worker-echo.log" >&2 || true
    tail -n 200 "$CACHE_DIR/api-gateway.log" >&2 || true
    exit 1
  fi
  sleep 0.2
done

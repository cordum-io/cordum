#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
SANDBOX="$(mktemp -d -t platform-smoke-test.XXXXXX)"
trap 'rm -rf "${SANDBOX}"' EXIT

FAKE_BIN="${SANDBOX}/bin"
CURL_LOG="${SANDBOX}/curl.log"
STATE_FILE="${SANDBOX}/approved.state"
mkdir -p "${FAKE_BIN}"

cat >"${FAKE_BIN}/curl" <<'FAKECURL'
#!/usr/bin/env bash
set -euo pipefail

method="GET"
out_file=""
write_out=""
data=""
url=""
fail_on_http=0

while [[ "$#" -gt 0 ]]; do
  case "$1" in
    -X)
      method="${2:-GET}"
      shift 2
      ;;
    -o)
      out_file="${2:-}"
      shift 2
      ;;
    -w)
      write_out="${2:-}"
      shift 2
      ;;
    -d|--data|--data-raw|--data-binary)
      data="${2:-}"
      shift 2
      ;;
    -H|--cacert|--connect-timeout|--max-time)
      shift 2
      ;;
    -f|-fsS)
      fail_on_http=1
      shift
      ;;
    -s|-S|-sS|--ssl-no-revoke)
      shift
      ;;
    http://*|https://*)
      url="$1"
      shift
      ;;
    *)
      shift
      ;;
  esac
done

path="/${url#*://*/}"
path="${path%%#*}"
status=200
body='{}'

case "${method} ${path}" in
  "POST /api/v1/workflows")
    body='{"id":"wf-test"}'
    ;;
  "POST /api/v1/workflows/wf-test/runs")
    body='{"run_id":"run-test"}'
    ;;
  "GET /api/v1/workflow-runs/run-test")
    if [[ -f "${STATE_FILE}" ]]; then
      body='{"status":"succeeded","steps":{"approve":{"job_id":"job-test"}}}'
    else
      body='{"status":"waiting","steps":{"approve":{"job_id":"job-test"}}}'
    fi
    ;;
  "POST /api/v1/approvals/job-test/approve")
    printf 'approval data=%s\n' "${data}" >>"${CURL_LOG}"
    if [[ "${data}" == *'"approved"'* || "${data}" != *'"reason"'* ]]; then
      status=400
      body='{"code":"invalid_body","error":"invalid body","status":400}'
    else
      : >"${STATE_FILE}"
      body='{"job_id":"job-test","state":"approved"}'
    fi
    ;;
  "GET /api/v1/audit/verify?tenant=default")
    body='{"status":"ok","total_events":1,"gaps":[]}'
    ;;
  "GET /api/v1/governance/health?tenant=default")
    body='{"grade":"A","factors":{"denial_rate":0,"approval_latency_p95":0,"policy_coverage":1,"chain_integrity":1}}'
    ;;
  "DELETE /api/v1/workflow-runs/run-test"|"DELETE /api/v1/workflows/wf-test")
    body='{}'
    ;;
  *)
    status=404
    body="{\"error\":\"unexpected ${method} ${path}\"}"
    ;;
esac

if [[ -n "${out_file}" ]]; then
  printf '%s' "${body}" >"${out_file}"
  [[ "${write_out}" == "%{http_code}" ]] && printf '%s' "${status}"
else
  printf '%s' "${body}"
fi

if [[ "${fail_on_http}" -eq 1 && "${status}" -ge 400 ]]; then
  exit 22
fi
FAKECURL
chmod +x "${FAKE_BIN}/curl"

cat >"${FAKE_BIN}/jq" <<'FAKEJQ'
#!/usr/bin/env bash
set -euo pipefail

raw=0
check=0
while [[ "$#" -gt 0 ]]; do
  case "$1" in
    -r)
      raw=1
      shift
      ;;
    -e)
      check=1
      shift
      ;;
    *)
      break
      ;;
  esac
done
expr="${1:-}"
shift || true
if [[ "$#" -gt 0 ]]; then
  json_input="$(cat "$1")"
else
  json_input="$(cat)"
fi

python_bin="${PYTHON_BIN:-}"
if [[ -z "${python_bin}" ]]; then
  python_bin="$(command -v python || command -v python3 || command -v python.exe)"
fi
JSON_INPUT="${json_input}" "${python_bin}" - "${raw}" "${check}" "${expr}" <<'PY'
import json
import os
import re
import sys

raw = sys.argv[1] == "1"
check = sys.argv[2] == "1"
expr = sys.argv[3]
data = json.loads(os.environ.get("JSON_INPUT", "{}"))

def emit(value):
    if value is None:
        value = ""
    if raw and isinstance(value, str):
        print(value)
    else:
        print(json.dumps(value))

if expr == ".id":
    emit(data.get("id"))
elif expr == ".run_id":
    emit(data.get("run_id"))
elif expr == ".steps.approve.job_id // empty":
    emit(data.get("steps", {}).get("approve", {}).get("job_id", ""))
elif expr == ".status":
    emit(data.get("status", ""))
elif expr.startswith(".status == "):
    ok = (
        data.get("status") == "ok"
        and data.get("total_events", 0) > 0
        and len(data.get("gaps", [])) == 0
    )
    sys.exit(0 if ok else 1)
elif expr.startswith("(.grade | test"):
    factors = data.get("factors", {})
    ok = (
        re.match(r"^[ABCDF]$", str(data.get("grade", ""))) is not None
        and all(k in factors for k in (
            "denial_rate",
            "approval_latency_p95",
            "policy_coverage",
            "chain_integrity",
        ))
    )
    sys.exit(0 if ok else 1)
else:
    raise SystemExit(f"unsupported jq expression: {expr}")

if check:
    sys.exit(0)
PY
FAKEJQ
chmod +x "${FAKE_BIN}/jq"

cat >"${FAKE_BIN}/sleep" <<'FAKESLEEP'
#!/usr/bin/env bash
exit 0
FAKESLEEP
chmod +x "${FAKE_BIN}/sleep"

if ! PATH="${FAKE_BIN}:${PATH}" \
  CURL_LOG="${CURL_LOG}" \
  STATE_FILE="${STATE_FILE}" \
  CORDUM_API_KEY="platform-smoke-test-key" \
  CORDUM_TENANT_ID="default" \
  CORDUM_API_BASE="http://fake.local" \
  bash "${ROOT}/tools/scripts/platform_smoke.sh" >"${SANDBOX}/out.log" 2>&1; then
  cat "${SANDBOX}/out.log" >&2
  exit 1
fi

grep -qx 'approval data={"reason":"platform smoke approval"}' "${CURL_LOG}"
if grep -q '"approved"' "${CURL_LOG}"; then
  echo "FAIL: platform_smoke sent obsolete approved boolean body" >&2
  cat "${CURL_LOG}" >&2
  exit 1
fi
grep -q '\[platform_smoke\] done' "${SANDBOX}/out.log"

echo "platform_smoke approval body test passed"

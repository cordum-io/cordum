# Cordum Quick Start (5 minutes)

This walkthrough starts a local stack and runs a minimal approval-only workflow
without any external workers.

Want the fastest path? Run:

```bash
export CORDUM_API_KEY="$(openssl rand -hex 32)"
export CORDUM_TENANT_ID=default
./tools/scripts/quickstart.sh
```

`quickstart.sh` brings up the stack and runs the approval smoke test for you.

## Prerequisites

- Docker + Docker Compose
- curl
- jq
- Go (optional, only if using `cordumctl`)

## Step 1: Set API key + tenant

Set an API key and tenant before starting:

```bash
export CORDUM_API_KEY="$(openssl rand -hex 32)"
export CORDUM_TENANT_ID=default
```

Docker Compose reads `.env` automatically; the helper scripts read environment
variables from your shell, so keep the `export` lines when running scripts.

## Step 2: Start the stack

```bash
go run ./cmd/cordumctl up
```

Or:

```bash
docker compose build
docker compose up -d
```

## Step 3: Confirm the gateway is healthy

```bash
API_KEY=${CORDUM_API_KEY:?set CORDUM_API_KEY}
TENANT_ID=${CORDUM_TENANT_ID:-default}
curl -sS http://localhost:8081/api/v1/status \
  -H "X-API-Key: ${API_KEY}" \
  -H "X-Tenant-ID: ${TENANT_ID}" | jq
```

## Step 4: Create a workflow

```bash
API_KEY=${CORDUM_API_KEY:?set CORDUM_API_KEY}
ORG_ID=${CORDUM_ORG_ID:-default}
TENANT_ID=${CORDUM_TENANT_ID:-${ORG_ID}}
workflow_id=$(curl -sS -X POST http://localhost:8081/api/v1/workflows \
  -H "X-API-Key: ${API_KEY}" \
  -H "X-Tenant-ID: ${TENANT_ID}" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "hello-world",
    "org_id": "'"${ORG_ID}"'",
    "steps": {
      "approve": {
        "type": "approval",
        "name": "Approve"
      }
    }
  }' | jq -r '.id')

echo "workflow: ${workflow_id}"
```

## Step 5: Start a run

```bash
run_id=$(curl -sS -X POST http://localhost:8081/api/v1/workflows/${workflow_id}/runs \
  -H "X-API-Key: ${API_KEY}" \
  -H "X-Tenant-ID: ${TENANT_ID}" \
  -H "Content-Type: application/json" \
  -d '{}' | jq -r '.run_id')

echo "run: ${run_id}"
```

## Step 6: Approve the workflow gate job

```bash
# Find the gate job for this run in the approvals queue
job_id=$(curl -sS http://localhost:8081/api/v1/approvals \
  -H "X-API-Key: ${API_KEY}" \
  -H "X-Tenant-ID: ${TENANT_ID}" | \
  jq -r --arg run_id "${run_id}" '.items[] | select(.workflow_run_id == $run_id) | .job.id' | head -n1)

curl -sS -X POST http://localhost:8081/api/v1/approvals/${job_id}/approve \
  -H "X-API-Key: ${API_KEY}" \
  -H "X-Tenant-ID: ${TENANT_ID}" \
  -H "Content-Type: application/json" \
  -d '{}' >/dev/null
```

## Step 7: Check status

```bash
curl -sS http://localhost:8081/api/v1/workflow-runs/${run_id}?org_id=${ORG_ID} \
  -H "X-API-Key: ${API_KEY}" \
  -H "X-Tenant-ID: ${TENANT_ID}" | jq -r '.status'
```

Expected output: `succeeded`.

## Step 8: Clean up

```bash
curl -sS -X DELETE http://localhost:8081/api/v1/workflow-runs/${run_id}?org_id=${ORG_ID} \
  -H "X-API-Key: ${API_KEY}" \
  -H "X-Tenant-ID: ${TENANT_ID}" >/dev/null
curl -sS -X DELETE http://localhost:8081/api/v1/workflows/${workflow_id}?org_id=${ORG_ID} \
  -H "X-API-Key: ${API_KEY}" \
  -H "X-Tenant-ID: ${TENANT_ID}" >/dev/null
```

## Next steps

- If you did not run `quickstart.sh`, run the built-in smoke test: `bash ./tools/scripts/platform_smoke.sh`
- Install the hello pack: `examples/hello-pack` + `examples/hello-worker-go`
- Explore the dashboard: `http://localhost:8082`

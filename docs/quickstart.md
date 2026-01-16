# Cordum Quick Start (5 minutes)

This walkthrough starts a local stack and runs a minimal approval-only workflow
without any external workers.

## Prerequisites

- Docker + Docker Compose
- curl
- jq

## Step 1: Start the stack

```bash
./cmd/cordumctl/cordumctl up
```

Or:

```bash
docker compose build
docker compose up -d
```

## Step 2: Confirm the gateway is healthy

```bash
curl -sS http://localhost:8081/api/v1/status | jq
```

## Step 3: Create a workflow

```bash
API_KEY=${CORDUM_API_KEY:-super-secret-key}
workflow_id=$(curl -sS -X POST http://localhost:8081/api/v1/workflows \
  -H "X-API-Key: ${API_KEY}" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "hello-world",
    "org_id": "default",
    "steps": {
      "approve": {
        "type": "approval",
        "name": "Approve"
      }
    }
  }' | jq -r '.id')

echo "workflow: ${workflow_id}"
```

## Step 4: Start a run

```bash
run_id=$(curl -sS -X POST http://localhost:8081/api/v1/workflows/${workflow_id}/runs \
  -H "X-API-Key: ${API_KEY}" \
  -H "Content-Type: application/json" \
  -d '{}' | jq -r '.run_id')

echo "run: ${run_id}"
```

## Step 5: Approve the step

```bash
curl -sS -X POST http://localhost:8081/api/v1/workflows/${workflow_id}/runs/${run_id}/steps/approve/approve \
  -H "X-API-Key: ${API_KEY}" \
  -H "Content-Type: application/json" \
  -d '{"approved": true}' >/dev/null
```

## Step 6: Check status

```bash
curl -sS http://localhost:8081/api/v1/workflow-runs/${run_id} \
  -H "X-API-Key: ${API_KEY}" | jq -r '.status'
```

Expected output: `succeeded`.

## Step 7: Clean up

```bash
curl -sS -X DELETE http://localhost:8081/api/v1/workflow-runs/${run_id} \
  -H "X-API-Key: ${API_KEY}" >/dev/null
curl -sS -X DELETE http://localhost:8081/api/v1/workflows/${workflow_id} \
  -H "X-API-Key: ${API_KEY}" >/dev/null
```

## Next steps

- Run the built-in smoke test: `./tools/scripts/platform_smoke.sh`
- Install the hello pack: `examples/hello-pack` + `examples/hello-worker-go`
- Explore the dashboard: `http://localhost:8082`

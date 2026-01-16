# Hello Pack

This pack registers a minimal workflow that dispatches a single worker job on
`job.hello-pack.echo` and validates the step input with a JSON schema.

## Install

```bash
./cmd/cordumctl/cordumctl pack install ./examples/hello-pack
```

## Run

```bash
curl -sS -X POST http://localhost:8081/api/v1/workflows/hello-pack.echo/runs \
  -H "X-API-Key: ${CORDUM_API_KEY:-super-secret-key}" \
  -H "Content-Type: application/json" \
  -d '{"message":"hello from pack","author":"demo"}' | jq
```

## Worker

Start a worker that consumes `job.hello-pack.echo` before running the workflow.
See `examples/hello-worker-go` for a ready-to-run example.

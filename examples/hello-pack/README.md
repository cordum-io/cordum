# Hello Pack (Example)

This pack installs a single workflow that dispatches `job.hello-pack.echo` and
validates input/output with JSON Schemas.

## Install

From the repo root:

```bash
go run ./cmd/cordumctl pack install ./examples/hello-pack
```

## Run

```bash
curl -sS -X POST http://localhost:8081/api/v1/workflows/hello-pack.echo/runs \
  -H "X-API-Key: ${CORDUM_API_KEY:-super-secret-key}" \
  -H "Content-Type: application/json" \
  -d '{"message":"hello from pack","author":"demo"}'
```

## Uninstall

```bash
go run ./cmd/cordumctl pack uninstall hello-pack
```

> Note: The worker for `job.hello-pack.echo` lives in `examples/hello-worker-go`.

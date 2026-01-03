# Local E2E (platform-only)

This document captures end-to-end flows validated against the platform-only stack. No workers or LLMs are required.

## Stack (compose)
- Infra: NATS `4222`, Redis `6379`.
- Control plane: scheduler, safety kernel, API gateway (HTTP `:8081`, gRPC `:8080`, metrics `:9092`), workflow engine (`:9093/health`).
- Optional: context engine (`:50070`).

## Automated smoke

### Platform smoke (curl + jq)

```bash
./tools/scripts/platform_smoke.sh
```

### CLI smoke (coretexctl)

```bash
./tools/scripts/coretexctl_smoke.sh
```

## Manual flow (no workers)

1) Create a workflow with an approval step:

```bash
export CORETEX_API_KEY=super-secret-key
curl -sS -X POST http://localhost:8081/api/v1/workflows \
  -H 'Content-Type: application/json' \
  -H "X-API-Key: $CORETEX_API_KEY" \
  -d '{"name":"local-e2e","org_id":"default","steps":{"approve":{"type":"approval"}}}'
```

2) Start a run:

```bash
curl -sS -X POST http://localhost:8081/api/v1/workflows/<workflow_id>/runs \
  -H 'Content-Type: application/json' \
  -H "X-API-Key: $CORETEX_API_KEY" \
  -d '{}'
```

3) Approve the step:

```bash
curl -sS -X POST http://localhost:8081/api/v1/workflows/<workflow_id>/runs/<run_id>/steps/approve/approve \
  -H 'Content-Type: application/json' \
  -H "X-API-Key: $CORETEX_API_KEY" \
  -d '{"approved": true}'
```

4) Delete the run and workflow:

```bash
curl -sS -X DELETE http://localhost:8081/api/v1/workflow-runs/<run_id> \
  -H "X-API-Key: $CORETEX_API_KEY"

curl -sS -X DELETE http://localhost:8081/api/v1/workflows/<workflow_id> \
  -H "X-API-Key: $CORETEX_API_KEY"
```

## Notes
- Safety policy (`config/safety.yaml`) denies `sys.*` and allows `job.*` for the default tenant.
- Scheduler timeouts come from `config/timeouts.yaml`.
- Cancellation uses `sys.job.cancel` (BusPacket JobCancel).
- Use repo-local caches when running scripts (`GOCACHE=.cache/go-build`).

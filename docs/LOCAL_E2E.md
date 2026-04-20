# Local E2E (platform-only)

This document captures end-to-end flows validated against the platform-only stack. No workers or LLMs are required.

## Stack (compose)
- Infra: NATS `4222`, Redis `6379`.
- Control plane: scheduler, safety kernel, API gateway (HTTP `:8081`, gRPC `:8080`, metrics `:9092`), workflow engine (`:9093/health`).
- Optional: context engine (`:50070`).
- Optional UI: dashboard (`:8082`).

## Automated smoke

### Platform smoke (curl + jq)

```bash
bash ./tools/scripts/platform_smoke.sh
```

### Install-to-approval E2E

This script simulates a clean install, then runs the approval workflow smoke test.

```bash
export CORDUM_API_KEY=<your-api-key>
export CORDUM_TENANT_ID=default
./tools/scripts/e2e_install_workflow.sh
```

To reuse an existing install directory, set `CORDUM_E2E_REUSE=1`.
To clean and reinstall, set `CORDUM_E2E_CLEAN=1`.
If ports are already in use, set `CORDUM_E2E_ALLOW_PORTS=1` (or override the list via `CORDUM_E2E_PORTS`).
If you need to delete a custom `DEST_DIR` outside `/tmp/cordum-e2e`, set `CORDUM_E2E_ALLOW_DELETE=1`.

### CLI smoke (cordumctl)

Requires `cordumctl` on `PATH` (build with `make build SERVICE=cordumctl` and add `./bin` to `PATH`).

```bash
./tools/scripts/cordumctl_smoke.sh
```

## Manual flow (no workers)

1) Create a workflow with an approval step:

```bash
export CORDUM_API_KEY=<your-api-key>
export CORDUM_TENANT_ID=default
curl -sS -X POST http://localhost:8081/api/v1/workflows \
  -H 'Content-Type: application/json' \
  -H "X-API-Key: $CORDUM_API_KEY" \
  -H "X-Tenant-ID: $CORDUM_TENANT_ID" \
  -d '{"name":"local-e2e","org_id":"default","steps":{"approve":{"type":"approval"}}}'
```

2) Start a run:

```bash
curl -sS -X POST http://localhost:8081/api/v1/workflows/<workflow_id>/runs \
  -H 'Content-Type: application/json' \
  -H "X-API-Key: $CORDUM_API_KEY" \
  -H "X-Tenant-ID: $CORDUM_TENANT_ID" \
  -d '{}'
```

3) Approve the step:

```bash
curl -sS -X POST http://localhost:8081/api/v1/workflows/<workflow_id>/runs/<run_id>/steps/approve/approve \
  -H 'Content-Type: application/json' \
  -H "X-API-Key: $CORDUM_API_KEY" \
  -H "X-Tenant-ID: $CORDUM_TENANT_ID" \
  -d '{"approved": true}'
```

4) Delete the run and workflow:

```bash
curl -sS -X DELETE http://localhost:8081/api/v1/workflow-runs/<run_id> \
  -H "X-API-Key: $CORDUM_API_KEY" \
  -H "X-Tenant-ID: $CORDUM_TENANT_ID"

curl -sS -X DELETE http://localhost:8081/api/v1/workflows/<workflow_id> \
  -H "X-API-Key: $CORDUM_API_KEY" \
  -H "X-Tenant-ID: $CORDUM_TENANT_ID"
```

## Dashboard Feature Testing

After smoke tests, verify dashboard features at `http://localhost:8082`:

- **Delete workflow**: WorkflowDetailPage > Delete button (with confirmation dialog)
- **Renew lock**: ToolsPage > Locks > Renew button (extends lock TTL)
- **Step approval**: Runs use `approveStep` API for workflow step approvals

## Notes
- Safety policy (`config/safety.yaml`) denies `sys.*` and allows `job.*` for the default tenant.
- Scheduler timeouts come from `config/timeouts.yaml`.
- Cancellation uses `sys.job.cancel` (BusPacket JobCancel).
- Use repo-local caches when running scripts (`GOCACHE=.cache/go-build`).

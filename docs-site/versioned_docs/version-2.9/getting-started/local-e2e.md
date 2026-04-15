---
sidebar_position: 5
title: "Local E2E Testing"
slug: /getting-started/local-e2e
---

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

This script simulates a clean install, runs the baseline approval workflow smoke test,
and then runs a decision-ready approval validation that proves actionable approval
data survives from workflow creation to `/api/v1/approvals` and resolved history.

```bash
export CORDUM_API_KEY=<your-api-key>
export CORDUM_TENANT_ID=default
./tools/scripts/e2e_install_workflow.sh
```

The decision-ready validation creates a temporary workflow approval with structured
business context and asserts the approval list exposes:

- `decision_summary.source=workflow_payload`
- `decision_summary.context_status=available`
- decision-first fields such as `vendor`, `amount`, and `why`
- a non-empty `context_ptr`
- dereferenced `job_input.decision.*` data for the approval record
- resolved approval history that still contains the decision summary plus resolver
  metadata after approval

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

1) Create a workflow with a decision-ready approval step:

```bash
export CORDUM_API_KEY=<your-api-key>
export CORDUM_TENANT_ID=default
curl -sS -X POST http://localhost:8081/api/v1/workflows \
  -H 'Content-Type: application/json' \
  -H "X-API-Key: $CORDUM_API_KEY" \
  -H "X-Tenant-ID: $CORDUM_TENANT_ID" \
  -d '{
    "name":"local-e2e",
    "org_id":"default",
    "steps":{
      "approve":{
        "type":"approval",
        "name":"Manager Approval",
        "input":{
          "amount":"${input.request.amount}",
          "currency":"${input.request.currency}",
          "vendor":"${input.request.vendor}",
          "items":"${input.request.items}",
          "approval_reason":"${input.request.reason}",
          "next_effect":"Approve to continue Manager Approval."
        },
        "input_schema":{
          "type":"object",
          "properties":{
            "amount":{"type":"number"},
            "currency":{"type":"string"},
            "vendor":{"type":"string"},
            "items":{"type":"array"},
            "approval_reason":{"type":"string"},
            "next_effect":{"type":"string"}
          },
          "required":["amount","currency","vendor","items","approval_reason"]
        }
      }
    }
  }'
```

Save the returned workflow ID:

```bash
export WORKFLOW_ID=<workflow_id>
```

2) Start a run with real decision context (the approval step dispatches a gate job to the Approvals queue):

```bash
curl -sS -X POST "http://localhost:8081/api/v1/workflows/${WORKFLOW_ID}/runs" \
  -H 'Content-Type: application/json' \
  -H "X-API-Key: $CORDUM_API_KEY" \
  -H "X-Tenant-ID: $CORDUM_TENANT_ID" \
  -d '{
    "request":{
      "amount":1250,
      "currency":"USD",
      "vendor":"Acme Travel",
      "items":["flight","hotel"],
      "reason":"manager threshold exceeded"
    }
  }'
```

Save the returned run ID:

```bash
export RUN_ID=<run_id>
```

3) List approvals, inspect the decision-ready fields, and approve the gate job:

```bash
# Capture the workflow approval record
curl -sS "http://localhost:8081/api/v1/approvals?include_resolved=false" \
  -H "X-API-Key: $CORDUM_API_KEY" \
  -H "X-Tenant-ID: $CORDUM_TENANT_ID" \
  | jq --arg wf "$WORKFLOW_ID" --arg run "$RUN_ID" '
      .items[]
      | select(.workflow_id == $wf and .workflow_run_id == $run)
    '

# Save the gate job ID
export JOB_ID=$(
  curl -sS "http://localhost:8081/api/v1/approvals?include_resolved=false" \
    -H "X-API-Key: $CORDUM_API_KEY" \
    -H "X-Tenant-ID: $CORDUM_TENANT_ID" \
    | jq -r --arg wf "$WORKFLOW_ID" --arg run "$RUN_ID" '
        .items[]
        | select(.workflow_id == $wf and .workflow_run_id == $run)
        | .job.id
      '
)

# Inspect the decision-first summary and dereferenced workflow payload
curl -sS "http://localhost:8081/api/v1/approvals?include_resolved=false" \
  -H "X-API-Key: $CORDUM_API_KEY" \
  -H "X-Tenant-ID: $CORDUM_TENANT_ID" \
  | jq --arg wf "$WORKFLOW_ID" --arg run "$RUN_ID" '
      .items[]
      | select(.workflow_id == $wf and .workflow_run_id == $run)
      | {
          context_ptr,
          decision_summary: {
            source: .decision_summary.source,
            context_status: .decision_summary.context_status,
            vendor: .decision_summary.vendor,
            amount: .decision_summary.amount,
            why: .decision_summary.why,
            next_effect: .decision_summary.next_effect,
            completeness: .decision_summary.completeness,
            missing_fields: .decision_summary.missing_fields
          },
          decision_payload: .job_input.decision
        }
    '

# Approve the gate job by job ID
curl -sS -X POST "http://localhost:8081/api/v1/approvals/${JOB_ID}/approve" \
  -H 'Content-Type: application/json' \
  -H "X-API-Key: $CORDUM_API_KEY" \
  -H "X-Tenant-ID: $CORDUM_TENANT_ID" \
  -d '{"reason":"approved for smoke","note":"decision-ready manual check"}'

# Confirm the run completed
curl -sS "http://localhost:8081/api/v1/workflow-runs/${RUN_ID}" \
  -H "X-API-Key: $CORDUM_API_KEY" \
  -H "X-Tenant-ID: $CORDUM_TENANT_ID" \
  | jq '{status, id}'

# Confirm resolved history still retains the approval summary + audit fields
curl -sS http://localhost:8081/api/v1/approvals \
  -H "X-API-Key: $CORDUM_API_KEY" \
  -H "X-Tenant-ID: $CORDUM_TENANT_ID" \
  | jq --arg job "$JOB_ID" '
      .items[]
      | select(.job.id == $job)
      | {
          status,
          resolved_by,
          resolved_comment,
          resolved_at,
          decision_summary: {
            source: .decision_summary.source,
            context_status: .decision_summary.context_status,
            vendor: .decision_summary.vendor,
            amount: .decision_summary.amount,
            why: .decision_summary.why
          }
        }
    '
```

4) Delete the run and workflow:

```bash
curl -sS -X DELETE "http://localhost:8081/api/v1/workflow-runs/${RUN_ID}" \
  -H "X-API-Key: $CORDUM_API_KEY" \
  -H "X-Tenant-ID: $CORDUM_TENANT_ID"

curl -sS -X DELETE "http://localhost:8081/api/v1/workflows/${WORKFLOW_ID}" \
  -H "X-API-Key: $CORDUM_API_KEY" \
  -H "X-Tenant-ID: $CORDUM_TENANT_ID"
```

## Approval validation guide

Use these checks when the smoke script or manual flow fails:

- `decision_summary.context_status=available` — expected happy path. Workflow payload
  was persisted and the gateway successfully hydrated the approval context.
- `decision_summary.context_status=missing` — the approval record still references a
  workflow payload (`context_ptr` usually exists), but the stored payload could not be
  found. Check context-store persistence, cleanup timing, and whether the backing store
  was reset after the run started.
- `decision_summary.context_status=unavailable` — the gateway could not access the
  memory/context store at all. Check Redis/context-store wiring, gateway startup logs,
  and any local dependency failures.
- `decision_summary.context_status=malformed` — a payload was found, but it could not be
  decoded into a valid approval context envelope. Check custom test data, manual payload
  edits, and any workflow code that writes invalid JSON into the stored context.
- `decision_summary.source=policy_only` or a missing `context_ptr` — you are likely
  looking at a legacy/non-workflow approval path. That is allowed for backward
  compatibility, but it should not be the result for the workflow validation above.

For the decision-ready workflow path, approvers should see business fields such as
vendor, amount, reason/why, and next effect first. Workflow, run, and job identifiers
remain available for audit/debugging, but they should be treated as secondary metadata.

## Dashboard Feature Testing

After smoke tests, verify dashboard features at `http://localhost:8082`:

- **Delete workflow**: WorkflowDetailPage > Delete button (with confirmation dialog)
- **Renew lock**: ToolsPage > Locks > Renew button (extends lock TTL)
- **Workflow approvals**: Approval steps appear on the Approvals page with a "Workflow Gate" badge

## Notes
- Safety policy (`config/safety.yaml`) denies `sys.*` and allows `job.*` for the default tenant.
- Scheduler timeouts come from `config/timeouts.yaml`.
- Cancellation uses `sys.job.cancel` (BusPacket JobCancel).
- Use repo-local caches when running scripts (`GOCACHE=.cache/go-build`).

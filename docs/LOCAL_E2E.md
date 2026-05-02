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

### Edge fake-hook E2E

Drives the Edge P0 backend against a running Cordum stack with synthetic
Claude hook payloads. CI-safe: uses synthetic file-path strings only and
never reads or stats a real `.env`. The script runs from the repo root.

```bash
bash tools/scripts/edge_fake_hook_e2e.sh        # default: SKIP unless integration mode
make edge-fake-hook-e2e                          # same, via Makefile target
```

For package-level backend regression commands that do not require a running
stack, see [Edge backend integration tests](../TESTING.md#edge-backend-integration-tests).
For the security closure list used by final P0 acceptance, see
[docs/security/edge-p0-threat-model.md § EDGE-032 acceptance checklist](security/edge-p0-threat-model.md#edge-032-acceptance-checklist).

**Prerequisites**
- A running Cordum stack at `CORDUM_GATEWAY` (default `https://localhost:8081`
  with `./certs/ca/ca.crt` present, otherwise `http://localhost:8081`).
  Set `CORDUM_EDGE_E2E_START_STACK=1` to run `make dev-up` first (requires
  Docker on `PATH`).
- `curl` and `jq` on `PATH` (or `CORDUM_JQ` pointing at a `jq` binary).
- A Cordum API key for the test tenant (any tenant whose API key is
  authorized for the `/api/v1/edge/*` routes).
- Demo Edge policy must be loaded for the test tenant — the script verifies
  `examples/cordum-edge-pack/overlays/policy.fragment.yaml` is on disk and
  assumes the agentd/Gateway has the matching overlay loaded so
  `claude-code.deny-secret-reads` and `claude-code.require-approval-for-edits`
  fire deterministically.
- **Default (hook) mode only**: `./bin/cordum-hook` and `./bin/cordum-agentd`
  on disk (the script auto-builds them via `go build` if `go` is on `PATH`),
  plus `openssl` for `CORDUM_AGENTD_NONCE` generation. Set
  `CORDUM_EDGE_E2E_BYPASS_HOOK=1` to skip the hook subprocess and drive
  gates via Gateway-direct requests instead (CI hosts without a Go
  toolchain or `openssl`).

**Environment variables**

| Variable | Default | Notes |
| --- | --- | --- |
| `CORDUM_API_KEY` | _empty_ | Required in strict mode. |
| `CORDUM_TENANT_ID` | `default` | Used in `X-Tenant-ID`. |
| `CORDUM_GATEWAY` | auto | Auto-detects http/https from `./certs/ca/ca.crt`. |
| `CORDUM_TLS_CA` | `./certs/ca/ca.crt` | TLS CA cert for Gateway. |
| `CORDUM_INTEGRATION` | _empty_ | Set to `1` to make missing prerequisites a FAIL. |
| `CORDUM_EDGE_E2E_START_STACK` | _empty_ | Set to `1` to run `make dev-up` first. |
| `CORDUM_EDGE_E2E_TIMEOUT` | `10` | Bounded wait seconds for HTTP requests / approvals. |
| `CORDUM_EDGE_E2E_KEEP_TMP` | _empty_ | Set to `1` to skip temp-dir cleanup (debug only). |
| `CORDUM_EDGE_E2E_BYPASS_HOOK` | _empty_ | Set to `1` to skip the cordum-hook + agentd subprocess and drive every gate via direct Gateway requests. Use only when the host cannot build/run the hook + agentd binaries. |
| `CORDUM_EDGE_E2E_AGENTD_PORT` | `0` | Pin the agentd loopback port. `0` picks a free port; override only when the host forbids ephemeral binds. |

**Expected output (strict mode)**

```text
PASS edge_session_setup
PASS edge_pretooluse_deny
PASS edge_approval_flow
PASS edge_posttooluse_artifact
PASS edge_evidence_export
```

**SKIP semantics**: in default mode (`CORDUM_INTEGRATION` unset) the script
prints exactly one `SKIP edge_fake_hook_e2e: <reason>` line and exits `0`.
This makes the script safe to wire into CI lanes that do not stand up the
Cordum stack.

**Exit codes**

| Code | Meaning |
| --- | --- |
| `0` | All gates PASS, or SKIP taken in non-integration mode. |
| `1` | Gate assertion FAIL. |
| `2` | Usage error or missing prerequisite in strict mode. |
| `124` | Bounded wait timed out. |

**Real-Claude demo**: this script is the CI-safe variant. The manual
real-Claude demo (a developer running `claude` with the cordum-hook plugin
configured against a live agentd) lives separately and is not exercised
here.

**Hook vs bypass mode**: by default the script spawns a process-local
`cordum-agentd` subprocess (with a freshly-generated `CORDUM_AGENTD_NONCE`)
and pipes synthetic Claude hook JSON through `cordum-hook` for each gate.
The hook → agentd → Gateway path is the EDGE-027 acceptance path; QA
verifies it against PRD §24. Setting `CORDUM_EDGE_E2E_BYPASS_HOOK=1`
substitutes Gateway-direct `/api/v1/edge/evaluate` and `/api/v1/edge/events`
calls for the hook subprocess and is intended only for CI hosts without
the Go toolchain or `openssl`. PASS line shapes are identical between
modes.

**EDGE-017.4 forward-compat**: the agentd loopback nonce is currently
embedded in the URL query string (`http://127.0.0.1:<port>/v1/edge/hooks/
claude?nonce=<value>`) and exported to `cordum-hook` via
`CORDUM_AGENTD_URL`. When EDGE-017.4 (cordum-hook reads nonce from
`CORDUM_AGENTD_HOOK_NONCE` env and sends it as `X-Cordum-Agentd-Nonce`
header) lands, the script's `compose_agentd_url` and `run_hook` helpers
will switch to the env-and-header transport. PASS line shapes will stay
stable across that migration.

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

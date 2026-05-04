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
The EDGE-032 acceptance checklist used for final P0 acceptance lives in the
internal Edge P0 threat model (Cordum engineering).

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
- **Mandatory rebuild before strict mode** (`CORDUM_INTEGRATION=1`): if any
  code under `core/edge/`, `core/controlplane/gateway/`, `cmd/cordum-hook/`,
  or `cmd/cordum-agentd/` has changed since the running stack was started,
  rebuild BOTH the local Edge binaries AND the `api-gateway` image before
  running the script:
  ```bash
  make edge-rebuild-e2e    # rebuilds bin/cordum-{hook,agentd,ctl} + api-gateway image, recreates the gateway container
  ```
  Skipping this step is the EDGE-044 trap: a fresh `cordum-hook` /
  `cordum-agentd` produces post-EDGE-041 `_redacted`-keyed events, but a
  stale `api-gateway:dev` image runs the pre-EDGE-041 classifier that
  reads bare keys only — every PreToolUse silently falls through to
  default-deny and the e2e gates 2-5 fail with `no matching rule`. The
  `make edge-rebuild-e2e` target is the single source of truth that keeps
  binaries and image in lockstep.

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

**Gate coverage matrix**

| # | Gate | Asserts |
| --- | --- | --- |
| 1 | `edge_session_setup` | session/execution Gateway-direct create + round-trip GETs |
| 2 | `edge_pretooluse_deny` | classifier + evaluate fresh-deny path (`hook.pre_tool_use` against `claude-code.deny-secret-reads`) |
| 3 | `edge_approval_flow` | enqueue → approve → retry consumes via approval_ref AND auto-consumes via action_hash; third call hits the terminal "already consumed" path |
| 4 | `edge_approval_rejected` (EDGE-056) | enqueue → reject (separate resolver principal to dodge self_approval_denied) → retry asserts `decision=DENY` and `.reason` contains "rejected" (case-insensitive); second reject of the terminal approval is non-2xx |
| 5 | `edge_posttooluse_artifact` | hook.post_tool_use event with synthetic artifact pointer round-trips into the Gateway session-events listing |
| 6 | `edge_evidence_export` | session-export endpoint returns the recorded events with bounded redaction |

**Deferred — EDGE-056 scope cut, tracked as task-e56bd1d7 (EDGE-056-EXPIRED)**: `gate_approval_expired` is NOT yet covered.
The blocker is `enqueueEdgeEvaluateApproval` at `core/controlplane/gateway/handlers_edge_evaluate.go:1058`
hardcoding `TTL: 5 * time.Minute` with no per-request `expires_at` override path and no env-var
knob. The follow-up adds the gate using `approval_ttl_seconds: 2` once `task-4c2b24d2`
(EDGE-059 — TTL override) lands DONE. The 2-second-TTL bounded-sleep gate that DoD #2 of
EDGE-056 demands cannot be implemented within the existing Gateway API surface AND the script's
max-10s sleep rail; expiration is exercised today only by the Go-test internal time-mocking
pattern in `edge_evaluate_test.go`. Two-phase chain: task-4c2b24d2 (EDGE-059) → task-e56bd1d7
(EDGE-056-EXPIRED) → final 6/6 coverage.

**Why this matters**: full 6-state coverage would have caught EDGE-039 (gateway/agentd EventID
collision) and EDGE-042 (action_hash auto-consume) at integration time instead of in the final
review sweep. EDGE-056 ships 5/6 today (gate_approval_rejected); the EDGE-059 → EDGE-056-EXPIRED
chain closes the last cell to 6/6.

**Expected output (strict mode)**

```text
PASS edge_session_setup
PASS edge_pretooluse_deny
PASS edge_approval_flow
PASS edge_approval_rejected
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

**Header-only hook nonce auth**: default hook mode now keeps
`CORDUM_AGENTD_URL` as the bare
`http://127.0.0.1:<port>/v1/edge/hooks/claude` endpoint. The script passes the
same runtime nonce to `cordum-hook` through `CORDUM_AGENTD_HOOK_NONCE`, and
`cordum-hook` sends it to agentd as `X-Cordum-Agentd-Nonce`. URL query-string
nonces (`?nonce=`) are refused; PASS line shapes stayed stable across the
EDGE-017.4.1 removal.

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

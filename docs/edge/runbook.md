# Cordum Edge P0 operator runbook

This runbook helps a demo operator recover the Cordum Edge P0 Claude Code demo
without weakening security posture. It assumes the operator is using the P0 path:

```text
cordumctl edge claude -> cordum-agentd -> cordum-hook -> Gateway Edge APIs
```

Use it with [demo.md](demo.md), [cordumctl edge doctor](cordumctl-edge-doctor.md),
and [configuration.md](configuration.md). The EDGE-032 acceptance checklist
in the internal Edge P0 threat model is the production sign-off reference
(Cordum engineering).

## Operating rules

- Use synthetic demo data only. Never paste real secrets, customer data, raw
  `.env` contents, raw prompts, transcripts, or command output into prompts,
  screenshots, issue comments, logs, or docs.
- Treat `CORDUM_API_KEY`, `CORDUM_AGENTD_NONCE`, and
  `CORDUM_AGENTD_HOOK_NONCE` as secrets. They must not appear in Claude settings,
  dashboard evidence, metrics labels, exported bundles, or chat handoffs.
- Real Claude Code is manual/optional. The automated readiness path is
  `tools/scripts/edge_fake_hook_e2e.sh`.
- The wrapper is the developer/demo path. It is not fleet enforcement by itself;
  managed settings, endpoint controls, binary trust, and service bootstrap are
  enterprise controls tracked by the threat model and follow-up tasks.

## Fast triage flow

1. **Run doctor first** when the wrapper or dashboard does not behave as
   expected:

   ```bash
   ./bin/cordumctl edge doctor --json
   ```

   Exit `0` means no blocking diagnostics. Exit `1` means at least one failure.
   Exit `2` means warning-only posture that needs operator review.

2. **Verify tenant and identity** match across CLI, Gateway, and dashboard:

   ```bash
   echo "$CORDUM_GATEWAY"
   echo "$CORDUM_TENANT_ID"
   echo "$CORDUM_PRINCIPAL_ID"

   # Or — dump the resolved config (api_key redacted) with per-field
   # source attribution so you can see flag/env/yaml/default precedence
   # at a glance before launching anything:
   cordumctl edge claude --print-config
   ```

   Do not echo `CORDUM_API_KEY` or any nonce value.

3. **Reproduce with the fake-hook E2E** when real Claude behavior is unclear:

   ```bash
   bash tools/scripts/edge_fake_hook_e2e.sh
   CORDUM_INTEGRATION=1 bash tools/scripts/edge_fake_hook_e2e.sh
   ```

   Default mode should SKIP safely if no stack is available. Strict mode should
   emit the five PASS lines documented in [demo.md](demo.md#automated-acceptance-path--fake-hook-e2e).

4. **Check the security boundary before applying a workaround.** If the proposed
   fix would persist a nonce, bypass tenant auth, disable redaction, store raw
   tool payloads, or claim wrapper-only enterprise enforcement, stop and consult
   the internal Edge P0 threat model (Cordum engineering).

## Common failures and recovery

| Symptom | Likely cause | Recovery |
| --- | --- | --- |
| `cordumctl edge claude` says Gateway/API key/tenant/principal metadata is missing. | Required env/flags are absent or empty. | Export `CORDUM_GATEWAY`, `CORDUM_API_KEY`, `CORDUM_TENANT_ID`, and `CORDUM_PRINCIPAL_ID`, or pass equivalent flags. Re-run `cordumctl edge doctor --json`. |
| Gateway returns `401` or `403`. | API key invalid, wrong auth scheme, tenant header missing, or key not authorized for `/api/v1/edge/*`. | Use the generated local-stack API key for the demo tenant. Confirm `X-Tenant-ID`/`CORDUM_TENANT_ID` exactly match the dashboard tenant. Do not paste the key into logs. |
| Cross-tenant or missing session/export errors return `404`. | The session belongs to a different tenant, or the dashboard/API is using a different tenant header. | Use the session's tenant from the wrapper dry-run summary. Re-open dashboard with that tenant selected and retry export with the matching `X-Tenant-ID`. |
| `claude not found` or wrapper cannot launch Claude. | Claude Code is not installed or not on `PATH`. | For manual demo, install Claude Code or pass `--claude-path`. For CI/new engineer validation, use `--dry-run` and the fake-hook E2E instead. |
| `cordum-hook not found` or `cordum-agentd not found`. | Binaries were not built or PATH points to stale binaries. | Run `make build SERVICE=cordum-hook`, `make build SERVICE=cordum-agentd`, and `make build SERVICE=cordumctl`, or pass `--hook-command` and `--agentd-path` to explicit paths. |
| Agentd fails to start or bind. | Loopback port already in use, invalid `CORDUM_AGENTD_SOCKET`, remote/broad bind rejected, or strict state-dir permissions fail. | Let the wrapper choose a free loopback port. Keep `CORDUM_AGENTD_SOCKET` HTTP loopback only. On Windows strict-perms failures, move the state directory under the user profile or fix inherited ACLs before retry. |
| `agentd nonce must be supplied via CORDUM_AGENTD_HOOK_NONCE` or query nonce auth fails. | Old settings persisted `?nonce=` or nonce was hand-edited into `CORDUM_AGENTD_URL`. | Regenerate settings with `cordumctl edge claude`; keep `CORDUM_AGENTD_URL` bare and let the launcher inject `CORDUM_AGENTD_HOOK_NONCE` at runtime. |
| Hook times out or Claude reports an unresponsive hook. | Hook wall-clock budget is too close to Claude's 5 second deadline, Gateway/Safety Kernel is slow, or agentd is unavailable. | Use generated `CORDUM_AGENTD_HOOK_TIMEOUT=4.5s`; avoid custom values `>=5s`; check Gateway/Safety Kernel health; rerun fake-hook E2E to isolate Claude from backend latency. |
| Safety Kernel or policy evaluate appears unavailable. | Gateway cannot reach Safety Kernel or policy route returns 5xx. | Run doctor; check Gateway logs and `SAFETY_KERNEL_ADDR`; in `observe` mode evidence may be degraded, but `enforce`/`enterprise-strict` should fail risky actions closed. Do not relabel degraded evidence as allow. |
| Demo policy decisions do not match expected deny/approval. | Demo policy overlay missing, tenant has a different policy snapshot, or action text/tool kind does not match fixture rules. | Verify `examples/cordum-edge-pack/overlays/policy.fragment.yaml` is loaded for the tenant. Use the exact prompts from [demo.md](demo.md). Check timeline policy snapshot/rule IDs before retrying. |
| Approval drawer is empty after `REQUIRE_APPROVAL`. | Wrong tenant/principal/role, expired approval, stream stale, or session detail is filtered. | Refresh the session detail page, clear decision/execution filters, confirm the requester principal and tenant, then use list/detail approval APIs if the UI is stale. |
| Approval retry keeps failing. | Approval expired, rejected, consumed already, action hash/input hash changed, or retry prompt was not the same action. | Re-run the same action text/tool target exactly once after approval. If already consumed, create a new approval; do not mutate hashes or reuse stale approval refs. |
| Inline approval wait times out. | Local/demo `CORDUM_AGENTD_INLINE_APPROVAL_WAIT_TIMEOUT` elapsed before an operator approved. | Approve from the dashboard and retry the action manually. For scripted demos, prefer fake-hook E2E approval flow with bounded waits. |
| Dashboard Edge Sessions list is empty. | Session did not register, wrong tenant selected, Gateway auth failed, or event stream disconnected. | Check dry-run session/execution IDs, dashboard tenant selector, browser devtools network errors, and Gateway `/api/v1/edge/sessions` response for the same tenant. |
| Session detail shows `Timeline: 0 events` while session status is `running`. | Most often **benign** — no Claude tool call has triggered a hook yet, so no `AgentActionEvent` has been recorded. The dashboard EmptyState text reflects this ("has not emitted any agent action events yet"). After the first PreToolUse/PostToolUse hook fires, events appear within ~1 second. If 0 events persists after a confirmed tool invocation, the bug class is **session/event chain**: agentd not bound to the wrapper-created EdgeSession (CORDUM_EDGE_SESSION_ID/EXECUTION_ID propagation), agentd evidence event ID colliding with Gateway's evaluate event ID, or events/batch idempotency-key collision. | First wait 5 seconds after a tool call. If still 0, run `curl -sS "$CORDUM_GATEWAY/api/v1/edge/sessions/<session-id>/events" -H "X-Tenant-ID: $CORDUM_TENANT_ID" -H "X-API-Key: $CORDUM_API_KEY"` to ask the Gateway directly — if Gateway returns events but dashboard does not, suspect the WebSocket stream (see "Timeline or event inspector shows stale data" below). If Gateway returns 0 events, suspect the hook→agentd→Gateway write chain: check agentd stderr for evaluate/RecordDecisionEvidence calls, verify CORDUM_EDGE_SESSION_ID matches the Gateway session, and verify cordum-hook stdout is non-empty (degraded fail-mode silently exits 0). |
| Timeline or event inspector shows stale data. | WebSocket/SSE stream disconnected or query cache was not invalidated. | Refresh the page, reconnect to the local stack, and compare with `GET /api/v1/edge/sessions/<id>/events`. File a dashboard bug if API data is correct but UI remains stale. |
| Artifact panel is empty after PostToolUse. | The hook recorded the decision but no artifact pointer metadata was attached, or export/artifact route failed. | Inspect the session events for artifact pointer metadata. P0 does not inline artifact bodies; absence of bodies is expected, absence of pointer metadata after the scripted gate is a bug. |
| Evidence export fails or contains no approvals. | Wrong tenant/session ID, export route auth error, or approval flow was not completed. | Use the exact session ID from dry-run/dashboard, same `X-Tenant-ID`, and rerun approval gate. Export should include issue/approve/consume records once the approval flow completes. |
| Docker is unavailable. | Local stack cannot be started by `make dev-up`. | Use an existing shared/local stack, or run the fake-hook E2E without `CORDUM_INTEGRATION` to get a non-destructive SKIP. Do not mark strict E2E green without a running stack. |
| `make dev-up` starts but strict E2E still cannot reach Gateway. | TLS CA mismatch, port collision, slow startup, or Gateway not ready. | Confirm `CORDUM_GATEWAY`, `CORDUM_TLS_CA`, and published port. Increase only the script's bounded wait via `CORDUM_EDGE_E2E_TIMEOUT` if startup is legitimately slow. |
| Raw `claude` works without Cordum governance. | The user launched raw Claude outside the wrapper. | For local demo, relaunch through `cordumctl edge claude`. For enterprise, use managed settings and endpoint/binary controls; do not claim wrapper-only enforcement. |
| A screenshot or export accidentally includes sensitive material. | Operator captured real data or an unredacted view. | Delete the artifact from the demo bundle, rotate any exposed secret, rerun with synthetic data, and document the incident using the threat model's redaction/audit guidance. |

## Recovery playbooks

### Gateway auth or tenant mismatch

1. Run `cordumctl edge doctor --json` and note only status/remediation fields.
2. Confirm the demo tenant in CLI env, dashboard tenant selector, and API request
   header is the same string.
3. Regenerate a local-stack API key if `401`/`403` persists.
4. Retry the smallest safe API probe first, then rerun `cordumctl edge claude
   --dry-run`.
5. If the failure is cross-tenant leakage instead of a clean denial/404, stop
   and file a security bug; do not continue the demo.

### Agentd or hook startup failure

1. Rebuild the three binaries and pass explicit paths:

   ```bash
   make build SERVICE=cordumctl
   make build SERVICE=cordum-hook
   make build SERVICE=cordum-agentd
   ./bin/cordumctl edge claude \
     --agentd-path ./bin/cordum-agentd \
     --hook-command ./bin/cordum-hook \
     --dry-run
   ```

2. Ensure generated settings contain a bare loopback `CORDUM_AGENTD_URL` and no
   `?nonce=` query string.
3. Do not copy hook nonce values into settings, logs, screenshots, or issues.
4. If strict state permissions fail, fix the state directory ACL instead of
   disabling secret-handling checks.

### Policy mismatch

1. Confirm the demo overlay is installed for the same tenant as the session.
2. Use the exact demo prompts and synthetic paths from [demo.md](demo.md).
3. Inspect the timeline decision event for `policy_snapshot`, rule ID, action
   hash, and input hash.
4. If the rule snapshot is different from the demo overlay, update the tenant
   policy or use a tenant that has the fixture loaded.
5. Do not edit tests or docs to match a broken policy; fix the tenant fixture.

### Approval timeout or stale approval

1. Use the approval drawer to approve/reject before the inline wait deadline, or
   let the wait time out and retry manually.
2. Keep the retry action identical: same tool kind, target path, command/edit
   text, policy snapshot, action hash, and input hash.
3. If approval was consumed once, create a new approval instead of replaying the
   old ref.
4. If another principal attempts self-approval or stale approval reuse and it
   succeeds, stop and file a security bug.

### Dashboard stream disconnected

1. Refresh the session detail page and clear UI filters.
2. Query events directly for the same tenant/session if needed:

   ```bash
   curl -sS "$CORDUM_GATEWAY/api/v1/edge/sessions/<session-id>/events" \
     -H "X-API-Key: $CORDUM_API_KEY" \
     -H "X-Tenant-ID: $CORDUM_TENANT_ID"
   ```

3. If API data is correct but UI remains stale, capture a sanitized screenshot
   and file a dashboard regression with the session ID only.
4. If API data is missing, rerun fake-hook E2E to distinguish backend/session
   issues from dashboard-only stream issues.

### Security incident during a demo

Use this path if a secret, raw transcript, customer datum, or unredacted payload
appears in a screenshot, log, event, export, or chat handoff.

1. Stop the demo and delete local copies of the sensitive artifact.
2. Rotate any exposed credential or ask the credential owner to rotate it.
3. Preserve only sanitized metadata: timestamp, tenant, session ID, event ID,
   route, and request ID.
4. Compare the incident to the threat mapping and known gaps catalog in
   the internal Edge P0 threat model (Cordum engineering).
5. File a follow-up bug with sanitized reproduction steps. Do not attach raw
   payloads.

## Per-session execution cap and DeleteSession cleanup

The Gateway enforces a per-session execution cap to keep one EdgeSession from
fanning out to thousands of `AgentExecution` rows. Configure with
`CORDUM_EDGE_MAX_EXECUTIONS_PER_SESSION` (default 100). When the cap is hit,
`POST /api/v1/edge/executions` returns `429 max_executions_exceeded` with
`details.{limit, current}`. Operator response: end the session and start a new
one; do not raise the cap globally to mask a runaway agent — investigate the
loop.

`DeleteSession` cleanup is paged (EDGE-037). The cleanup walks the
session-to-executions index in `maxStorePageLimit` (200) row chunks and runs a
per-page `MULTI/EXEC` for the execution data + secondary-index `ZRem`s before
running a final atomic batch for the session-level keys and indexes. Three
operational consequences:

1. Memory and pipeline size are bounded regardless of execution count.
2. If a per-page batch fails (network blip, Redis error, store-level
   `ErrValidation`), the wrapped error reports the page number and the
   `cleaned` count of executions removed before the failure. The session
   record itself stays intact, so re-invoking `DeleteSession` is safe and
   resumes from where it stopped (already-deleted executions just do not
   appear in the next iteration's `ZRange`).
3. If the loop succeeds but the final session-level batch fails, individual
   executions are gone but the session record remains. Re-invoke
   `DeleteSession` to drop the now-empty session record; the per-page loop
   will simply observe zero executions and proceed straight to the
   session-level delete.

In log triage, `cleaned=N` in the wrapped error tells you how far the cleanup
got; missing artifacts/audit pointers under those execution IDs are expected
on the cleaned subset.

## Go / no-go checklist before demo signoff

- `cordumctl edge doctor --json` has no unexpected `fail` entries.
- Fake-hook E2E strict mode emits the five PASS lines, or the run is explicitly
  marked SKIP because integration prerequisites are absent.
- Dashboard shows session, execution, timeline, redacted event inspector,
  approval drawer, artifact pointer panel, and evidence export for the same
  tenant/session.
- Export bundle contains metadata, decisions, approvals, hashes, and artifact
  manifests only; no raw secret material.
- P0 limitations are stated honestly: real Claude is optional/manual, wrapper is
  not enterprise enforcement, and P1/P2/P3 controls remain deferred until their
  tasks are approved.

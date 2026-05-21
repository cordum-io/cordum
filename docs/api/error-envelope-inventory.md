# Error Envelope Inventory (task-5b8a4174)

Status: INVENTORY — implementation tasks are filed per endpoint family. This
page is a contract review, not a broad handler rewrite.

## Scope and method

Commands run from `cordum/` on branch `wip/2026-05-15-orphan-rescue`:

```bash
git grep -n "writeErrorJSON" -- core/controlplane/gateway/*.go ':!core/controlplane/gateway/*_test.go'
# plus an inline Python call parser to exclude comments, tests, and the helper definition
```

The parser counts real production calls to `writeErrorJSON(...)`, extracts the
second argument when it is a literal `http.Status*`, and groups call sites by
handler family. Dynamic helper status expressions are listed separately because
some map to multiple status codes at runtime.

Existing coded envelopes were also checked:

- `writeJSONError(w, status, code, message)` is used by MCP approvals, MCP
  verify, and the audit-events parse-error path.
- `writeEdgeError(...)` is the dedicated `/api/v1/edge/*` envelope for modern
  Edge handlers.
- `writeTierLimitJSON(...)` already returns `{error, code, status, ...}` for
  entitlement failures.
- `docs/api/openapi/cordum-api.yaml` already models generic `Error.code` as
  optional; this inventory intentionally preserves that backwards-compatible
  schema.

## Total `writeErrorJSON` usages

- Production (non-test) call sites: **722**
- Files with call sites: **48**
- Known 4xx call sites: **504**
- Known 5xx call sites: **200**
- Dynamic/helper status call sites: **18**

### By status code

| Status | Count |
|---|---:|
| 400 | 269 |
| 401 | 16 |
| 403 | 85 |
| 404 | 89 |
| 409 | 28 |
| 413 | 7 |
| 422 | 1 |
| 429 | 9 |
| 500 | 81 |
| 501 | 1 |
| 502 | 4 |
| 503 | 114 |
| dynamic/helper | 18 |

### By endpoint family

| Family | Calls | Status mix |
|---|---:|---|
| agents | 17 | 400=7, 404=4, 503=6 |
| approvals | 8 | 403=2, 404=1, 409=1, 503=1, dynamic=3 |
| audit | 13 | 400=2, 403=6, 503=3, dynamic=2 |
| auth | 68 | 400=28, 401=11, 403=1, 404=8, 409=1, 429=1, 500=17, 503=1 |
| chat | 18 | 400=6, 403=2, 404=2, 500=4, 503=4 |
| config | 19 | 400=7, 403=3, 404=2, 409=1, 500=6 |
| copilot | 6 | 400=1, 401=1, 403=2, 404=1, 501=1 |
| delegation | 24 | 400=11, 401=2, 404=3, 422=1, 429=1, 503=5, dynamic=1 |
| dlq | 11 | 400=1, 403=1, 404=1, 500=4, 503=4 |
| edge | 12 | 400=5, 403=4, 413=1, 503=2 |
| evals | 74 | 400=41, 403=5, 404=9, 409=4, 413=1, 429=2, 503=12 |
| governance | 7 | 400=2, 403=3, 503=2 |
| helpers | 11 | 400=2, 403=3, 413=1, 500=1, 502=1, 503=3 |
| jobs | 85 | 400=15, 403=11, 404=8, 409=4, 413=4, 429=2, 500=12, 502=1, 503=27, dynamic=1 |
| legal | 13 | 400=3, 403=1, 404=1, 409=2, 500=3, 503=3 |
| license | 2 | 403=1, 503=1 |
| locks | 13 | 400=4, 403=4, 404=2, 409=2, 503=1 |
| mcp | 20 | 400=6, 401=1, 403=6, 404=2, 503=3, dynamic=2 |
| middleware | 8 | 401=1, 403=3, 404=1, 429=3 |
| packs | 35 | 400=20, 404=5, 409=1, 500=4, 503=3, dynamic=2 |
| policy | 74 | 400=36, 403=6, 404=11, 409=3, 500=5, 502=2, 503=9, dynamic=2 |
| pools | 28 | 400=12, 404=5, 409=7, 500=4 |
| rbac | 18 | 400=7, 404=2, 500=5, 503=4 |
| shadow | 19 | 400=9, 403=6, 500=1, dynamic=3 |
| stream | 4 | 400=1, 404=1, 500=1, 503=1 |
| telemetry | 3 | 400=2, 503=1 |
| topics | 12 | 400=7, 404=2, 503=3 |
| velocity | 9 | 400=6, 404=2, 409=1 |
| worker_credentials | 12 | 400=6, 404=2, 503=3, dynamic=1 |
| workers | 12 | 400=5, 404=4, 500=2, 503=1 |
| workflow | 67 | 400=17, 403=15, 404=10, 409=1, 500=12, 503=11, dynamic=1 |

### Dynamic/helper status call sites

| File:line | Status expression | Notes |
|---|---|---|
| `handlers_approvals.go:891` | `result.status` | approval repair result path |
| `handlers_approvals.go:1333` | `result.status` | approve-job result path |
| `handlers_approvals.go:1550` | `result.status` | reject-job result path |
| `handlers_audit_compliance.go:93` | `httpErr.status` | compliance export query parser |
| `handlers_audit_verify.go:105` | `httpErr.status` | audit verify query parser |
| `handlers_delegation.go:200` | `status` | delegation issue policy helper |
| `handlers_jobs.go:1626` | `perr.status` | memory-policy helper |
| `handlers_mcp_outbound.go:93` | `herr.status` | MCP range parser |
| `handlers_mcp_usage.go:101` | `herr.status` | MCP range parser |
| `handlers_packs.go:128` | `installErr.Status` | pack install validation/conflict |
| `handlers_packs.go:1351` | `installErr.Status` | marketplace install validation/conflict |
| `handlers_policy_bundles.go:496` | `outcome.Status` | policy signing strict-mode helper |
| `handlers_policy_global.go:211` | `outcome.Status` | policy signing strict-mode helper |
| `handlers_shadow_results.go:280` | `httpErr.status` | shadow summary range parser |
| `handlers_shadow_results.go:424` | `httpErr.status` | shadow comparisons range parser |
| `handlers_shadow_results.go:629` | `httpErr.status` | shadow timeseries range parser |
| `handlers_worker_credentials.go:114` | `status` | pool/topic existence validation |
| `handlers_workflows.go:408` | `perr.status` | memory-policy helper |

## Dashboard and SDK consumer evidence

Current dashboard behavior does **not** require a `code` field on every generic
4xx response:

- `dashboard/src/api/client.ts` reads `body.error` / `body.message` and then
  throws `ApiError(status, message, body)` for generic failures.
- `dashboard/src/api/retry.ts` and `dashboard/src/hooks/useApprovals.ts`
  inspect `ApprovalConflictPayload.code` only for approval conflict retry and
  optimistic-removal semantics.
- `dashboard/src/hooks/useMcpApprovals.ts` checks MCP approval codes such as
  `self_approval_denied` and `mcp_approvals_unavailable`; those routes already
  use `writeJSONError`.
- `dashboard/src/hooks/useEdgeSessions.ts` maps dedicated Edge error envelopes
  through `mapEdgeErrorEnvelope`; modern Edge routes already use `writeEdgeError`.
- `dashboard/src/lib/friendlyError.ts` uses `body.code` or selected `body.error`
  strings when present, then falls back to status-level messages.

Conclusion: use endpoint-family follow-ups for client-visible semantic states
(idempotency, approval/actionability, policy authoring conflicts, identity/admin
validation, legacy Edge envelope consistency, MCP/delegation machine clients).
Do not mass-convert all helpers and middleware.

## Filed follow-up tasks

| Task | Scope |
|---|---|
| `task-b5cbf22f` | job/workflow/approval semantic 4xx codes |
| `task-c0c97ec7` | policy/evals/packs authoring 4xx codes |
| `task-1d474b65` | identity/settings/admin 4xx codes |
| `task-e419bed7` | legacy Edge binary-integrity EdgeError envelope |
| `task-a3b1dfdd` | MCP/delegation operator and SDK-facing 4xx codes |

## Per-family decision

Decision values:

- `ROUTE_ALL_THROUGH_writeJSONError` — the family should use a coded envelope
  for all remaining generic 4xx/5xx errors in scope.
- `ROUTE_SOME_THROUGH_writeJSONError` — only route-specific semantic failures
  need codes; cross-cutting auth/rate-limit/body-size/infrastructure errors can
  remain generic or use their existing helper.
- `LEAVE_AS_IS` — current `{error,status}` contract is enough for known clients.

| Family | Decision | Reason | Follow-up |
|---|---|---|---|
| agents | ROUTE_SOME_THROUGH_writeJSONError | Dashboard agent identity panel distinguishes 404 by status today; stable codes would help SDK/admin clients for missing id, nonexistent identity, and limit errors. | `task-1d474b65` |
| approvals | ROUTE_SOME_THROUGH_writeJSONError | Approval-specific conflicts already have structured payloads; remaining result.status paths should be reviewed with jobs/workflows so dashboard retry/friendlyError semantics stay pinned. | `task-b5cbf22f` |
| audit | LEAVE_AS_IS | Audit-events parse 400s were converted in task-cfabb264; tier-limit payloads already include `code`; remaining query/tenant/availability errors are status-driven. | None |
| auth | ROUTE_SOME_THROUGH_writeJSONError | Login, password change, and user-management 4xxs are dashboard-facing; stable auth/admin codes would improve form-level UX and SDK behavior. | `task-1d474b65` |
| chat | LEAVE_AS_IS | Chat callers currently show message/status only; no code-specific dashboard behavior found. | None |
| config | ROUTE_SOME_THROUGH_writeJSONError | Settings/config validation and conflicts are admin-dashboard-facing. | `task-1d474b65` |
| copilot | LEAVE_AS_IS | Dashboard handles the 501 pending state by status; no code-specific consumer found. | None |
| delegation | ROUTE_SOME_THROUGH_writeJSONError | Delegation token errors are agent/SDK-facing and include rate-limit, chain-depth, and validation semantics. | `task-a3b1dfdd` |
| dlq | LEAVE_AS_IS | DLQ pages use status/message; no code-specific retry or UX branch found. | None |
| edge | ROUTE_ALL_THROUGH_writeJSONError | `/api/v1/edge/binary-integrity/events` is under the Edge API but still emits generic `{error,status}` instead of `EdgeError`. | `task-e419bed7` |
| evals | ROUTE_SOME_THROUGH_writeJSONError | Dataset/run authoring has client-facing validation, conflict, and cap states consumed by dashboard pages. | `task-c0c97ec7` |
| governance | LEAVE_AS_IS | Health/analytics callers use status/message; no structured-code branch found. | None |
| helpers | LEAVE_AS_IS | Shared helper wrappers represent cross-cutting generic errors; endpoint tasks should wrap only semantic route errors. | None |
| jobs | ROUTE_SOME_THROUGH_writeJSONError | Submit and job-control APIs include idempotency, backpressure, memory-policy, tenant, and agent-principal semantics; dashboard/friendlyError already has idempotency/approval code hooks. | `task-b5cbf22f` |
| legal | LEAVE_AS_IS | Operator/legal-hold UI is status/message-driven; no current code-specific consumer. | None |
| license | LEAVE_AS_IS | Tier-limit helpers already return `code`; remaining license errors are status-driven. | None |
| locks | LEAVE_AS_IS | Lock endpoints are supporting admin primitives; no current code-specific consumer found. | None |
| mcp | ROUTE_SOME_THROUGH_writeJSONError | MCP approvals/verify already emit codes; usage/outbound/range and tool errors should be reviewed for SDK/operator semantics. | `task-a3b1dfdd` |
| middleware | LEAVE_AS_IS | Cross-cutting 401/403/429/413 are handled by status in the dashboard and OpenAPI global responses. | None |
| packs | ROUTE_SOME_THROUGH_writeJSONError | Pack install/marketplace errors are client-facing and dynamic `PackInstallError.Status` should expose stable conflict/validation codes. | `task-c0c97ec7` |
| policy | ROUTE_SOME_THROUGH_writeJSONError | Policy authoring/simulation/global/bundle errors feed Policy Studio and friendlyError code paths. | `task-c0c97ec7` |
| pools | ROUTE_SOME_THROUGH_writeJSONError | Pool admin CRUD has validation/conflict semantics but no code-specific consumer today; group with identity/admin instead of mass rewrite. | `task-1d474b65` |
| rbac | ROUTE_SOME_THROUGH_writeJSONError | RBAC and role-management 4xxs are admin-dashboard-facing. | `task-1d474b65` |
| shadow | ROUTE_SOME_THROUGH_writeJSONError | Shadow-results query validation and policy shadow conflicts feed policy dashboard pages. | `task-c0c97ec7` |
| stream | LEAVE_AS_IS | Stream helper errors are generic status/message surfaces. | None |
| telemetry | LEAVE_AS_IS | Telemetry callers use status/message only. | None |
| topics | ROUTE_SOME_THROUGH_writeJSONError | Topic admin CRUD has validation/not-found semantics; group with identity/admin if SDK clients need pinned codes. | `task-1d474b65` |
| velocity | ROUTE_SOME_THROUGH_writeJSONError | Velocity-rule policy authoring is dashboard-facing and belongs with policy authoring codes. | `task-c0c97ec7` |
| worker_credentials | ROUTE_SOME_THROUGH_writeJSONError | Worker credential validation is admin/SDK-facing, including pool/topic existence decisions. | `task-1d474b65` |
| workers | ROUTE_SOME_THROUGH_writeJSONError | Worker admin CRUD is dashboard-facing; group with identity/admin. | `task-1d474b65` |
| workflow | ROUTE_SOME_THROUGH_writeJSONError | Run start/cancel/rerun has idempotency, memory-policy, conflict, and tenant semantics. | `task-b5cbf22f` |

## Representative spot checks

| File:line | Handler/helper | Assessment |
|---|---|---|
| `handlers_auth.go:439` | `handleLogin` | dashboard-facing auth form; should get route-specific codes in identity/admin follow-up |
| `handlers_agents.go:258` | `handleGetAgent` | dashboard-facing identity lookup; 404 status is currently enough but code useful for SDK pinning |
| `handlers_jobs.go:1568` | `handleSubmitJobHTTP` | backpressure/rate-limit semantics should be coded with job submission follow-up |
| `handlers_jobs.go:1626` | `handleSubmitJobHTTP` | dynamic memory-policy status needs stable memory-policy codes |
| `handlers_workflows.go:408` | `handleStartRun` | dynamic memory-policy status mirrors jobs follow-up |
| `handlers_approvals.go:337` | `handleCancelRun` | workflow busy/retry conflict should align with workflow codes |
| `handlers_policy_bundles.go:496` | `handlePutPolicyBundle` | policy signing strict-mode status should expose policy/signing codes |
| `handlers_policy_global.go:211` | `handlePutPolicyGlobal` | same signing helper as bundle authoring |
| `handlers_evals_datasets.go:145` | eval dataset create | request-too-large/validation states are dashboard-facing |
| `handlers_packs.go:128` | `handleInstallPack` | dynamic pack install error should expose stable validation/conflict codes |
| `handlers_mcp_outbound.go:93` | `handleMCPOutbound` | operator query parser; review with MCP/delegation but low priority |
| `handlers_shadow_results.go:280` | `handleShadowResultsSummary` | dashboard query parser; review with policy/shadow codes |
| `handlers_edge_binary_integrity.go:151` | binary-integrity ingest | Edge route currently uses generic envelope; should use EdgeError codes |
| `middleware.go:557` | `apiKeyMiddleware` | cross-cutting auth status handled centrally by dashboard; no mass conversion |
| `helpers.go:1165` | `writeJSONDecodeError` | cross-cutting body-size/invalid-json helper; endpoint-specific wrappers can override where needed |

## Out of scope for this task

- Mass conversion of all 722 call sites.
- Making `Error.code` required in OpenAPI; it must remain optional for backwards
  compatibility.
- Changing generated dashboard clients or route handlers in this inventory task.
- Replacing the dedicated Edge envelope or existing tier-limit coded envelope.

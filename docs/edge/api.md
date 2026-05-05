# Edge API reference

This page documents the P0 `/api/v1/edge/*` REST surface. The canonical wire
schema is the [OpenAPI spec](../api/openapi/cordum-api.yaml); this page is the
operator-oriented map of routes, request/response shapes, auth, and errors.

## Global contract

- **Authentication:** every Edge route uses the existing Gateway auth path
  (`X-API-Key` or a bearer-token `Authorization` header) plus `X-Tenant-ID`.
- **Tenant isolation:** route handlers reject missing or mismatched tenant data.
  Cross-tenant resources return the same not-found envelope as missing resources
  where needed to avoid enumeration.
- **Bodies:** write routes accept `application/json` and bounded request bodies.
  Raw prompts, raw tool payloads, command output, transcripts, signed URLs,
  bearer tokens, and API keys are rejected or redacted before persistence.
- **Errors:** Edge errors use `EdgeError`:
  `{ code, message, request_id, details? }`. `details` must contain only
  bounded, redacted, enum-like values.
- **Pagination:** list routes return `{ items, next_cursor }`. `limit` is
  bounded by the Gateway; omit `cursor` for the first page.
- **OpenAPI:** schemas named below are defined in
  [`docs/api/openapi/cordum-api.yaml`](../api/openapi/cordum-api.yaml).

## Error codes

| HTTP | Stable codes you may see | Notes |
| --- | --- | --- |
| 400 | `invalid_request`, `invalid_json`, `missing_required_field`, `missing_path_param`, `raw_payload_rejected`, `artifact_pointer_invalid`, `idempotency_key_invalid` | Validation, bad JSON, unsafe raw payloads, invalid artifact pointers, or malformed idempotency key. |
| 401 | `unauthorized` | Missing/invalid Gateway credentials. |
| 403 | `access_denied`, `tenant_required`, `tenant_mismatch`, `tenant_access_denied`, `self_approval_denied` | Authenticated caller cannot use this tenant/resource or tried to self-approve. |
| 404 | `not_found` | Missing, cross-tenant, or intentionally hidden resource. |
| 409 | `conflict`, `session_terminal`, `execution_terminal`, `execution_session_mismatch`, `approval_conflict`, `approval_not_actionable`, `idempotency_conflict`, `idempotency_window_expired` | Terminal resource, approval state/CAS conflict, or idempotent replay mismatch. |
| 413 | `request_too_large` | Body/export exceeds configured limits. |
| 429 | `max_executions_exceeded`, `event_cap_exceeded` | Write-side fanout caps. Execution creates return `max_executions_exceeded` after 100 executions/session (default). Event writes return `event_cap_exceeded` after 5000 events/execution. End the session/execution and start a new one to continue recording evidence. |
| 502 | `upstream_error` | Upstream policy/evaluate dependency failed in a way the route could not degrade. |
| 503 | `service_unavailable`, `store_unavailable` | Gateway store or Edge dependency is unavailable. |
| 500 | `internal_error` | Unexpected server failure; response remains redacted. |

## Sessions

| Method/path | Request shape | Response shape | Notes |
| --- | --- | --- | --- |
| `POST /api/v1/edge/sessions` | `EdgeSessionCreateRequest`: `principal_id`, `principal_type`, `agent_product`, `agent_version`, `mode`, repo/git/cwd metadata, `trace_id`, optional `workflow_run_id`/`job_id`, `policy_snapshot`, `enforcement_layers`, `policy_mode`, `labels`. | `201 EdgeSessionCreateResponse`: `session_id`, `execution_id`, `trace_id`, `policy_snapshot`, `dashboard_url`, `session`, `execution`. | Creates a session and initial execution. `tenant_id` may appear in the body but must match `X-Tenant-ID`. |
| `GET /api/v1/edge/sessions?principal_id=&cursor=&limit=` | Query only. | `200 EdgeSessionPageResponse`: `items`, `next_cursor`. | Dashboard list API. Filter by `principal_id`; status/product filtering is currently client-side. |
| `GET /api/v1/edge/sessions/{session_id}` | Path parameter. | `200 EdgeSession`. | Returns one tenant-scoped session. |
| `POST /api/v1/edge/sessions/{session_id}/heartbeat` | Path parameter. | `200 EdgeHeartbeatResponse`: `session_id`, `heartbeat_alive`. | Refreshes active session heartbeat. |
| `POST /api/v1/edge/sessions/{session_id}/end` | `EdgeEndSessionRequest`: optional `status`, `ended_at`. | `200 EdgeSession`. | Ends a session. Terminal sessions reject incompatible follow-up writes with conflict errors. |

## Executions

| Method/path | Request shape | Response shape | Notes |
| --- | --- | --- | --- |
| `POST /api/v1/edge/executions` | `EdgeExecutionCreateRequest`: required `session_id`; optional `adapter`, `mode`, `workflow_run_id`, `step_id`, `job_id`, `attempt`, `trace_id`, `worker_id`, `policy_snapshot`, `labels`. | `201 EdgeAgentExecution`. | Adds another execution under an existing session. Bounded by the per-session execution cap (`CORDUM_EDGE_MAX_EXECUTIONS_PER_SESSION`, default 100) — the (cap+1)th call returns `429 max_executions_exceeded` with `details.{limit,current}`. |
| `GET /api/v1/edge/executions/{execution_id}` | Path parameter. | `200 EdgeAgentExecution`. | Tenant-scoped execution lookup. |
| `POST /api/v1/edge/executions/{execution_id}/end` | `EdgeEndExecutionRequest`: optional `status`, `ended_at`. | `200 EdgeAgentExecution`. | Ends an execution; terminal executions reject incompatible event writes. |

## Policy evaluate

| Method/path | Request shape | Response shape | Notes |
| --- | --- | --- | --- |
| `POST /api/v1/edge/evaluate` | `EdgeEvaluateRequest`: required `session_id`, `execution_id`, `principal_id`, `layer`, `kind`; optional `event_id`, agent metadata, `tool_name`, `tool_use_id`, `input_redacted`, `input_hash`, repo/git/cwd metadata, `action_name`, `capability`, `risk_tags`, `labels`, `artifact_ptrs`. | `200 EdgeEvaluateResponse`: `decision`, `reason`, `rule_id`, `policy_snapshot`, optional `approval_ref`, `constraints`, `updated_input`, `event_id`, `degraded`, `error_code`, hook-friendly `permission_decision`, `exit_code`, terminal copy, and wait hints. | The Gateway computes/validates hashes, calls Safety Kernel policy, creates approvals for `REQUIRE_APPROVAL`, and records decision evidence. Safety outages may still return `200` with `degraded=true` when policy mode permits. |

Do not send raw `tool_input`, raw prompts, transcript text, authorization
headers, or provider secrets to `evaluate`; send redacted action input and hashes.

## Approvals

| Method/path | Request shape | Response shape | Notes |
| --- | --- | --- | --- |
| `GET /api/v1/edge/approvals?status=&session_id=&execution_id=&action_hash=&cursor=&limit=` | Query only. | `200 EdgeApprovalPageResponse`: `items`, `next_cursor`. | Lists tenant-scoped approvals for dashboard/operator UX. |
| `GET /api/v1/edge/approvals/{approval_ref}` | Path parameter. | `200 EdgeApproval`. | Original requester or authorized operator/admin can see the approval; hidden resources return `404 not_found`. |
| `POST /api/v1/edge/approvals/{approval_ref}/approve` | `EdgeApprovalDecisionRequest`: optional bounded `reason`. | `200 EdgeApproval`. | Resolves a pending approval. Self-approval returns `403 self_approval_denied`; stale/terminal approvals return conflict. |
| `POST /api/v1/edge/approvals/{approval_ref}/reject` | `EdgeApprovalDecisionRequest`: optional bounded `reason`. | `200 EdgeApproval`. | Rejects a pending approval. Same auth/conflict behavior as approve. |
| `POST /api/v1/edge/approvals/{approval_ref}/wait` | `{ timeout_ms? }`; server defaults/clamps the wait budget. | `200 EdgeApproval`. | Bounded wait used by local/demo inline approval flows. It returns the current approval, possibly still pending. |

Approvals bind to `action_hash`, `input_hash`, `policy_snapshot`, requester, and
status so an approval cannot be replayed against a different action/input.

## Events

| Method/path | Request shape | Response shape | Notes |
| --- | --- | --- | --- |
| `POST /api/v1/edge/events` | `EdgeAgentActionEventWriteRequest`: required `event_id`, `session_id`, `execution_id`, `ts`, `layer`, `kind`, `decision`, `status`; optional principal/tool/action metadata, redacted input/hash, `approval_ref`, artifact pointers, duration, errors, labels. Optional `Idempotency-Key` header. | `201 EdgeAgentActionEvent`. | Appends one event and assigns/validates sequence. Idempotent retries replay the first success or return conflict. The 5001st event for one execution returns `429 event_cap_exceeded`. |
| `POST /api/v1/edge/events/batch` | `EdgeAgentActionEventBatchRequest`: `events[]` with the same event shape. Optional `Idempotency-Key` header. | `201 EdgeAgentActionEventBatchResponse`: `items`. | Appends events in input order. Agentd uses this for atomic hook receipt + decision evidence. A batch that would exceed 5000 events for any execution returns `429 event_cap_exceeded` and writes nothing. |
| `GET /api/v1/edge/sessions/{session_id}/events?cursor=&limit=&kind=&decision=&since=&until=` | Path/query only. | `200 EdgeAgentActionEventPageResponse`: `items`, `next_cursor`. | Lists session events with bounded filters. |
| `GET /api/v1/edge/executions/{execution_id}/events?cursor=&limit=&kind=&decision=&since=&until=` | Path/query only. | `200 EdgeAgentActionEventPageResponse`. | Lists events for one execution. |

Event idempotency is scoped by tenant and endpoint. If the idempotency record
expires but the logical `event_id` is already present, the API returns
`409 idempotency_window_expired` and does not append a duplicate.

### Idempotency max-in-flight contract (EDGE-061)

The redis-side idempotency record TTL is 24 hours by default, but is
**refreshed on every Reserve retry** so a long-running flow (e.g. an
approval-bound request waiting on a human reviewer) keeps once-semantics
even past the original 24-hour window. To bound zombie state, the record
is rejected once its `created_at` exceeds the 7-day max-in-flight cap;
further Reserve or Complete attempts return
`409 idempotency_record_expired` (sentinel `ErrIdempotencyRecordExpired`).
The caller must generate a fresh idempotency key to make new progress.

Strategy: A+B (TTL extension on retry, capped at 7 days). The cap is
enforced at both `ReserveIdempotency` and `CompleteIdempotency` entry, so
a stuck pending record can never silently transition to completed past
the cap nor accept additional retry attempts.

## Evidence export

| Method/path | Request shape | Response shape | Notes |
| --- | --- | --- | --- |
| `POST /api/v1/edge/sessions/{session_id}/export` | `{ max_events? }`. | `200 SessionExportBundle`: `manifest_version`, `generated_at`, `tenant_id`, `session`, executions/events, artifact pointer metadata, missing-artifact reasons, and truncation metadata. | Metadata-only audit bundle. `include_artifact_bodies` is not accepted in P0. `413 request_too_large` means reduce `max_events` or raise `CORDUM_EDGE_EXPORT_MAX_BYTES` within allowed bounds. |

For the full artifact type catalog and missing-artifact reason semantics, see
[Edge evidence export](../edge-export.md).

## Related contract pages

- [Edge API observability notes](../edge-api.md)
- [Edge policy templates and approval retry](../edge-policy.md)
- [Edge evidence export](../edge-export.md)
- [Claude hook mapper](claude-hook-mapper.md)
- [cordum-agentd](cordum-agentd.md)

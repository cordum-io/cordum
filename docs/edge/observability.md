# Edge observability

Cordum Edge P0 emits Prometheus metrics, structured logs, and audit/SIEM
events for the Claude command-hook + local agentd + Gateway Edge API path.
This surface is compliance evidence, not a second job lifecycle: Edge actions
are modeled as `EdgeSession -> AgentExecution -> AgentActionEvent`, and
`job_id` is populated only when an Edge execution is explicitly linked to a real
Cordum Job or workflow run.

All metric emission goes through `core/edge.Recorder` and the Prometheus
implementation in `core/edge/observability_prom.go`. Call sites must not create
ad hoc registries. Tenant IDs are intentionally not metric labels; tenant
correlation belongs in audit/log records.

## EDGE-072 reviewer metric gap audit

External reviewer names omit Cordum's `cordum_edge_` namespace. For existing
metrics with a different name, keep the shipped name and document the mapping so
existing dashboards/alerts are not broken.

| Reviewer's metric | Status | Cordum metric or mapping | Decision |
| --- | --- | --- | --- |
| `edge_session_created_total` | DIFFERENT-NAME | `cordum_edge_sessions_created_total` | Document mapping; do not rename. |
| `edge_execution_started_total` | DIFFERENT-NAME | `cordum_edge_executions_started_total` | Document mapping; do not rename. |
| `edge_execution_completed_total` | DIFFERENT-NAME | `cordum_edge_executions_ended_total` | `ended` is the terminal lifecycle metric; do not rename. |
| `edge_event_persisted_total` | PRESENT | `cordum_edge_event_persisted_total` | Added in EDGE-072 after store commit. |
| `edge_event_redacted_total` | PRESENT | `cordum_edge_event_redacted_total` | Added in EDGE-072 at request redaction boundaries. |
| `edge_redaction_failed_total` | PRESENT | `cordum_edge_redaction_failed_total` | Existing EDGE-071 metric; EDGE-072 added the Gateway Edge-input site. |
| `edge_policy_decision_total{decision}` | DIFFERENT-NAME | `cordum_edge_action_decisions_total{layer,kind,decision,mode}` | Superset; document mapping. |
| `edge_ws_connections_active` | DIFFERENT-NAME | `cordum_edge_stream_clients` | Active Edge stream clients; document mapping. |
| `edge_ws_events_sent_total` | PRESENT | `cordum_edge_ws_events_sent_total` | Added in EDGE-072 on successful stream enqueue. |
| `edge_store_cleanup_total` | DIFFERENT-NAME | `cordum_edge_session_cleanup_keys_deleted_total`, `cordum_edge_session_cleanup_duration_seconds`, `cordum_edge_session_cleanup_deadline_total`, `cordum_edge_session_swept_total` | Cleanup is split into outcome/duration/sweeper counters; document mapping. |
| `edge_hook_timeout_total` | PRESENT | `cordum_edge_hook_timeout_total` | Added in EDGE-072 for request/gateway/kernel timeout phases. |

## Metric inventory

`Source` names the recorder method or code path that emits the metric.
`Dashboard hook` records whether a P0 dashboard consumer exists. New EDGE-072
metrics are backend-only for now by design; building new widgets is out of scope
for EDGE-072.

| Metric | Type | Labels | Source / emission path | Dashboard hook |
| --- | --- | --- | --- | --- |
| `cordum_edge_sessions_created_total` | counter | `mode`, `agent_product` | `RecordSessionCreated` on session creation. | Backend/API state powers Edge Sessions; no Prometheus widget. |
| `cordum_edge_sessions_ended_total` | counter | `mode`, `status` | `RecordSessionEnded` on terminal session state. | Backend-only metric. |
| `cordum_edge_sessions_active` | gauge | `mode` | `SetSessionsActive` from active-session accounting. | Backend-only metric. |
| `cordum_edge_executions_started_total` | counter | `mode`, `agent_product` | `RecordExecutionStarted` on execution creation. | Backend/API state powers session detail; no metric widget. |
| `cordum_edge_executions_ended_total` | counter | `mode`, `status` | `RecordExecutionEnded` on terminal execution state. | Backend-only metric. |
| `cordum_edge_create_execution_aborted_total` | counter | `reason` | `RedisStore.CreateExecution` aborts when parent is terminal/missing. | Backend-only metric. |
| `cordum_edge_session_cleanup_duration_seconds` | histogram | none | `RedisStore.DeleteSession` bounded cleanup duration. | Backend-only; see retention runbook. |
| `cordum_edge_session_cleanup_keys_deleted_total` | counter | none | `RedisStore.DeleteSession` deleted Redis-key count. | Backend-only; see retention runbook. |
| `cordum_edge_session_cleanup_deadline_total` | counter | none | `RedisStore.DeleteSession` foreground deadline exceeded. | Backend-only alert candidate. |
| `cordum_edge_session_event_cap_rejected_total` | counter | none | `AppendEvents` / idempotent append rejects per-execution cap. | Backend-only alert candidate. |
| `cordum_edge_session_swept_total` | counter | none | Retention sweeper removes aged sessions. | Backend-only; see retention runbook. |
| `cordum_edge_event_persisted_total` | counter | `layer`, `kind`, `decision` | `RedisStore.AppendEvents` and completed idempotent append after successful commit only. | Backend-only for now. |
| `cordum_edge_event_redacted_total` | counter | `outcome` | Gateway Edge event/evaluate request normalization after redaction. | Backend-only for now. |
| `cordum_edge_hook_timeout_total` | counter | `phase` | Claude runner request/gateway deadline paths and agentd kernel deadline path. | Backend-only for now. |
| `cordum_edge_action_decisions_total` | counter | `layer`, `kind`, `decision`, `mode` | `RecordActionDecision` from evaluate/hook decision path. | Governance views consume API/audit decision data, not this metric. |
| `cordum_edge_actions_denied_total` | counter | `layer`, `kind`, `reason_code` | `RecordActionDenied` on deny/throttle outcomes. | Governance views consume API/audit decision data. |
| `cordum_edge_approvals_requested_total` | counter | `layer`, `kind` | `RecordApprovalRequested` when approval is required/enqueued. | Approvals UI consumes API state; metric backend-only. |
| `cordum_edge_approvals_resolved_total` | counter | `layer`, `kind`, `outcome` | `RecordApprovalResolved` on terminal approval outcomes. | Approvals UI consumes API state; metric backend-only. |
| `cordum_edge_approval_enqueue_aborted_total` | counter | `reason` | Store fail-closed guard in `EnqueueApproval`. | Backend-only metric. |
| `cordum_edge_append_events_aborted_total` | counter | `reason` | `AppendEvents` aborts after parent session/execution turns terminal. | Backend-only metric. |
| `cordum_edge_idempotency_ttl_extended_total` | counter | `state` | Idempotency reserve/replay TTL refresh path. | Backend-only metric. |
| `cordum_edge_idempotency_window_expired_total` | counter | `phase` | Reserve/complete/append idempotency expiry rejections. | Backend-only metric. |
| `cordum_edge_degraded_total` | counter | `mode`, `component`, `reason_code` | `RecordDegraded` on degraded Gateway/agentd/hook/evidence paths. | Backend-only alert candidate. |
| `cordum_edge_fail_closed_total` | counter | `mode`, `reason_code` | `RecordFailClosed` when enterprise/workflow enforcement blocks on governance miss. | Backend-only alert candidate. |
| `cordum_edge_agentd_response_write_aborted_total` | counter | `reason` | agentd local-server slow-loris/write-abort guard. | Backend-only metric. |
| `cordum_edge_agentd_shutdown_forced_total` | counter | `reason` | agentd graceful-shutdown subcomponent timeout. | Backend-only metric. |
| `cordum_edge_export_request_rejected_total` | counter | `reason` | Edge export request validation rejections. | Export UI consumes API errors; metric backend-only. |
| `cordum_edge_redaction_failed_total` | counter | `site`, `reason` | Edge redaction call sites fail closed. | Backend-only alert candidate. |
| `cordum_edge_artifact_exports_total` | counter | `artifact_type`, `result` | Artifact/evidence export attempts/results. | Backend-only metric. |
| `cordum_edge_hook_latency_seconds` | histogram | `hook_event`, `decision` | Claude hook end-to-end latency observation. | Backend-only SLO input. |
| `cordum_edge_evaluate_latency_seconds` | histogram | `layer`, `kind`, `decision` | Gateway evaluate latency observation. | Backend-only SLO input. |
| `cordum_edge_cache_lookups_total` | counter | `layer`, `kind`, `result` | agentd safe-allow cache lookup result. | Backend-only metric. |
| `cordum_edge_stream_clients` | gauge | none | Edge WebSocket stream client connect/disconnect accounting. | Dashboard uses the stream; no Prometheus widget. |
| `cordum_edge_ws_events_sent_total` | counter | `tenant_present` | Gateway stream bridge when an Edge event is accepted into the broadcast queue. | Backend-only for now. |
| `cordum_edge_stream_drops_total` | counter | `reason` | Gateway stream bridge drop paths. | Backend-only alert candidate. |

### Shadow detector metrics (EDGE-143 design — proposed, not yet emitted)

The six metrics below are reserved by the EDGE-143 design at
[`kubernetes-ci-shadow-detector-design.md`](kubernetes-ci-shadow-detector-design.md)
§13.1. No call site emits them in the current build; they will land
with the EDGE-143.1 / EDGE-143.5 / EDGE-143.6 / EDGE-143.7 follow-up
tasks (`task-8f72d421`, `task-973d8bd7`, `task-cb1f5f2f`,
`task-8ab4001f`). Names, types, and labels are pinned here so the
inventory is stable when those tasks wire emission. All labels honor
the `### Bounded label sets` discipline below — tenant IDs are never
label values, in line with this file's invariant.

| Metric | Labels | Type | Unit | Meaning |
| --- | --- | --- | --- | --- |
| `cordum_edge_shadow_findings_total` | `source_type`, `risk`, `status` | counter | 1 | Total shadow findings emitted by detectors. One increment per finding write (mirrors PRD §17.1). |
| `cordum_edge_shadow_findings_active` | `source_type`, `tenant_present` | gauge | 1 | Active (non-resolved, non-expired) findings, periodically re-derived from the store. `tenant_present` is `true`/`false` only — never the tenant value. |
| `cordum_edge_shadow_detector_poll_duration_seconds` | `source_type` | histogram | seconds | Per-detector scan-cycle wall-clock duration. |
| `cordum_edge_shadow_detector_failures_total` | `source_type`, `reason_code` | counter | 1 | Detector scan failures (transient or persistent). `reason_code` is bounded — `k8s_api_unavailable`, `ci_api_rate_limited`, `auth_failed`, `redacted`, plus the universal `unknown` / `other`. |
| `cordum_edge_shadow_exceptions_active` | none | gauge | 1 | Operator-declared exceptions currently in effect, periodically re-derived from the store. |
| `cordum_edge_shadow_remediations_emitted_total` | `class` | counter | 1 | One increment per remediation template emitted by the EDGE-142 generator (`class` is the bounded remediation-class enum from `§12.1`). |

The cluster / namespace / workload / pod-UID / repo / run-ID / signal
identifiers from the `§13.1` audit-extra catalog are intentionally
**not** label values — they live in finding records and audit `extra`
fields, where high cardinality is acceptable.

### Bounded label sets

Recorder normalizers collapse unrecognized values to `other` or `unknown`.
Never add raw command strings, prompts, file paths, signed URLs, session IDs,
event IDs, approval refs, rule IDs, arbitrary error strings, bearer/API tokens,
or tenant IDs as labels.

| Label | Allowed values / bounding rule |
| --- | --- |
| `layer` | `hook`, `mcp`, `llm`, `runtime`, `workflow`, `system`, `other`, `unknown`. |
| `kind` | `hook.*`, `session.*`, `execution.*`, `mcp.*`, `llm.*`, `runtime.*`, `approval.*`, `other`, `unknown`. |
| `decision` | `allow`, `deny`, `require_approval`, `throttle`, `constrain`, `degraded`, `recorded`, `unknown`, `other`. |
| `mode` | `observe`, `local-dev`, `local-dev-enforce`, `enterprise-strict`, `workflow`, `unknown`, `other`. |
| `agent_product` | `claude-code`, `codex`, `cursor`, `unknown`, `other`. |
| `status` | Edge lifecycle statuses plus `unknown`/`other`. |
| `outcome` | Redaction: `applied`, `skipped`, `partial`, `failed`, `unknown`, `other`; approval outcomes use the approval enum. |
| `phase` | Hook timeout: `request`, `gateway`, `kernel`, `unknown`, `other`. |
| `tenant_present` | `true` or `false`; never the tenant value. |
| `reason`, `reason_code`, `site`, `component`, `artifact_type`, `result`, `hook_event`, `cache result` | Bounded per helper in `observability_prom.go`; unknown input collapses. New `reason_code` values reserved by EDGE-143: `k8s_api_unavailable`, `ci_api_rate_limited`, `auth_failed`, `redacted` (see `### Shadow detector metrics`). |
| `source_type` | EDGE-143 shadow-detector label. Bounded: `local`, `kubernetes`, `ci`, `network`, plus `unknown` / `other`. Reserved for the shadow-detector metrics above; not yet emitted. |
| `risk` | EDGE-143 shadow-detector label. Bounded: `low`, `medium`, `high`, plus `unknown` / `other`. Mirrors the `risk` field on `ShadowAgentFinding`. |
| `class` | EDGE-143 remediation label. Bounded: `attach_mcp_gateway`, `attach_edge_session`, `deploy_managed_settings`, `route_via_llm_proxy`, `register_ci_workflow`, `declare_exception`, `resolve_manually`, plus `unknown` / `other` (matches `kubernetes-ci-shadow-detector-design.md` §12.1). |

## Structured logs

Use the shared attribute builders in `core/edge/observability.go`: `EventLogAttrs`,
`SessionLogAttrs`, `ExecutionLogAttrs`, `ApprovalLogAttrs`,
`ExportResultLogAttrs`, `HookSummaryLogAttrs`, `EvaluateSummaryLogAttrs`, and
`ErrorLogAttrs`. These helpers emit only bounded IDs, enum-like fields,
timestamps, hashes, counts, redaction level, and status/decision metadata.
They intentionally do not log raw `InputRedacted` maps, labels, hook payloads,
prompts, tool output, approval reason text, signed artifact URIs,
Authorization headers, API keys, or hook nonces.

## Audit / SIEM events

Edge reuses the existing audit pipeline (`core/audit.AuditSender` and
`audit.SIEMEvent`). Audit emission is best-effort and must not change
policy/evaluate/hook decisions if the audit pipeline is unavailable.

### ProvenanceGate audit-chain verification

For destructive actions that present a backend `approval_ref`, the gateway's
production action-gate pipeline wires `ProvenanceGate` to the existing
per-tenant audit hash chain (`server.auditChainer` +
`core/audit.VerifyChain`). The verifier bounds each check to the approval
window (`CreatedAt` through max of `ResolvedAt`, `ConsumedAt`, and now) and
uses the tenant stream (`audit:chain:<tenant>`) rather than scanning unrelated
tenant history.

The accepted provenance event is resolved-only. The audit chain must contain a
canonical `EventEdgeApprovalResolved` / `edge.approval_resolved` event with
decision `approved` or `approve`, the same tenant, and bounded `extra` values
whose `approval_ref` and `action_hash` exactly match the `EdgeApproval`. An
`EventEdgeApprovalRequested` / `edge.approval_requested` row proves that review
was requested, but it does not prove that the destructive retry was approved and
does not satisfy `ProvenanceGate` by itself.

Verifier dependencies fail closed. If Redis or the audit chainer/verifier is
missing or unavailable, the gate returns `service_unavailable` with an
`audit_chain_verifier_unavailable` / `audit_chain_verify_failed` sub-reason so
operators see a dependency outage instead of a surprise `internal_error` after
human approval. Tampering signals still deny hard as integrity failures:
compromised hash/HMAC/linkage returns `audit_chain_compromised`, and a missing
resolved approval event, malformed event JSON, wrong tenant/ref/hash, or
non-approved terminal decision returns `audit_evidence_missing`.

Retention-trimmed history can produce a `partial` verifier status. Partial is
acceptable only when the requested window still has authenticated in-window
resolved approval evidence; a zero-event window or explicit
missing/out-of-order gap is treated as an evidence gap and fails closed. The
stream scan and chain verification are capped by the shared audit verification
limit, so high-volume tenants cannot trigger unbounded Redis work. When
`CORDUM_AUDIT_HMAC_KEY` is configured, the verifier uses
`Chainer.HMACKeyForVerify`; key material is never logged, returned in API
errors, or copied into audit `extra`.

| Event type | Builder / source | Severity source |
| --- | --- | --- |
| `edge.session_started` | `SIEMEventForSessionStarted` | `INFO`. |
| `edge.session_ended` | `SIEMEventForSessionEnded` | `INFO`; `HIGH` for failed/degraded sessions. |
| `edge.execution_started` | `SIEMEventForExecutionStarted` | `INFO`. |
| `edge.execution_ended` | `SIEMEventForExecutionEnded` | `INFO`, `MEDIUM`, or `HIGH` by terminal status. |
| `edge.action_attempted` | Reserved action-attempt evidence type. | `INFO` when used. |
| `edge.policy_decision` | `SIEMEventForAction` for allow/recorded decisions. | `INFO`. |
| `edge.action_denied` | `SIEMEventForAction` for deny/throttle. | `HIGH` for deny, `MEDIUM` for throttle. |
| `edge.approval_requested` | `SIEMEventForAction` or `SIEMEventForApprovalRequested`. | `MEDIUM`. |
| `edge.approval_resolved` | `SIEMEventForApprovalResolved`. | `INFO`, `MEDIUM`, or `HIGH` by outcome. |
| `edge.approval_rejected` | `SIEMEventForApprovalResolved` rejected branch. | `HIGH`. |
| `edge.approval_expired` | `SIEMEventForApprovalResolved` expired branch. | `MEDIUM`. |
| `edge.artifact_exported` | `SIEMEventForArtifactExported`. | Result-based. |
| `edge.agentd_degraded` | `SIEMEventForDegraded`. | `MEDIUM`; `HIGH` in local-dev-enforce. |
| `edge.fail_closed` | `SIEMEventForFailClosed`. | `CRITICAL`. |

### Reviewer audit-field mapping

| Required field | Audit JSON field / Extra key | Source |
| --- | --- | --- |
| `tenant_id` | `tenant_id` | `TenantID` from Edge session/execution/action/approval/artifact input, bounded by `boundedID`. |
| `principal_id` | `identity` | Edge principal/resolver ID (`PrincipalID` or resolver ID). Cordum's SIEM schema names the field `identity`. |
| `session_id` | `extra.session_id` | `actionExtra`, `sessionExtra`, `executionExtra`, approval/artifact extras. |
| `execution_id` | `extra.execution_id` | `actionExtra`, `executionExtra`, approval/artifact extras. |
| `policy_decision` | `decision` | Normalized `AgentActionEvent.Decision`, approval outcome, fail-closed deny, or degraded. |
| `classifier_result` | `action`, `risk_tags`, `capabilities` | `AgentActionEvent.ActionName`, `RiskTags`, and single classifier capability. |
| `redaction_status` | `extra.redaction_status` | `actionExtra`: label-provided bounded status or derived `applied`/`skipped`. |
| `event_counts` | `extra.event_counts` | `executionExtra`: `events=<n>,allow=<n>,deny=<n>,require_approval=<n>,artifacts=<n>`. |
| `timestamps` | `timestamp` | Event/session/execution/artifact timestamp, or UTC now if source timestamp is absent. |

### SIEMEvent field sources

| SIEM JSON field | Edge source / rule |
| --- | --- |
| `timestamp` | Builder-specific source timestamp or `time.Now().UTC()`. |
| `event_type` | Edge audit constant chosen by the builder and decision/status/result. |
| `severity` | Builder logic from decision, terminal status, result, mode, or fail-closed path. |
| `tenant_id` | Bounded Edge tenant ID. |
| `agent_id`, `agent_name`, `agent_risk_tier` | Not populated by Edge P0 builders. |
| `job_id` | Execution `JobID` only when linked to a real Cordum Job/workflow. |
| `action` | Bounded action name or fixed lifecycle action string. |
| `decision` | Normalized policy/approval/fail/degraded decision when applicable. |
| `matched_rule` | Bounded rule ID for action/approval events when present. |
| `reason` | Intentionally not populated from raw Edge reason text; bounded reason codes live in `extra.reason_code`. |
| `risk_tags` | Bounded action risk tags. |
| `capabilities` | Bounded action capability. |
| `policy_version` | Not populated by Edge P0; policy snapshots live in `extra.policy_snapshot`. |
| `identity` | Bounded principal/resolver ID. |
| `extra` | Safe bounded maps below. |
| `seq`, `event_hash`, `prev_hash`, `hmac` | Added by the audit chain/exporter, not Edge builders. |

### Safe `extra` fields

| Builder | Allowed `extra` keys |
| --- | --- |
| `actionExtra` | `session_id`, `execution_id`, `event_id`, `layer`, `kind`, `tool_name`, `input_hash`, `policy_snapshot`, `tier`, `approval_ref`, `redaction_status`. |
| `sessionExtra` | `session_id`, `mode`, `status`, `agent_product`. |
| `executionExtra` | `execution_id`, `session_id`, `adapter`, `mode`, `status`, `workflow_run_id`, `step_id`, `attempt`, `event_counts`. |
| `approvalExtra` / approval requested | `approval_ref`, `session_id`, `execution_id`, `event_id`, `rule_id`, `policy_snapshot`, plus bounded `action_hash` / `input_hash` when available. |
| `approvalExtra` / approval resolved | `approval_ref` plus bounded terminal metadata such as `action_hash`, `input_hash`, and `policy_snapshot`. This is the only approval lifecycle event kind that can satisfy `ProvenanceGate` for approved destructive actions. |
| `artifact` export | `artifact_type`, `result`, `session_id`, `execution_id`, `event_id`, `sha256`, `redaction_level`, `retention_class`. Raw artifact URI is never included. |
| degraded / fail-closed | `mode`, `component`, `reason_code`. |

Forbidden raw fields and payloads must never appear in audit JSON or `extra`:
`raw_prompt`, `raw_tool_input`, `raw_stderr`, `secret_token`, `.env_content`,
raw `InputRedacted`, labels, raw hook payloads, prompts, transcripts, command
output, signed URLs, Authorization headers, API keys, hook nonces, and free-form
errors. EDGE-072 tests serialize audit events and assert fake secret patterns do
not survive; `tools/scripts/lint_no_secret_log.sh` also scans serialized
audit-event fixtures for secret-shaped strings.

## Streams and idempotency

Edge action events are forwarded to the existing Gateway WebSocket stream as
compact `edge.event` messages. Generic `cordum_gateway_ws_*` metrics remain the
transport-health source; Edge-specific stream metrics count Edge bridge clients,
successful event enqueues, and drops. Tenant filtering and quarantine/redaction
behavior are preserved.

Edge event idempotency conflicts return the standard Edge error envelope
`{code, message, request_id, details?}`. Error details are centrally redacted
before serialization, so idempotency keys, signed URLs, bearer tokens, and raw
payload snippets cannot leak to clients.

## See also

- [Edge docs index](README.md)
- [Edge retention, caps, and cleanup](retention.md)
- [Edge Claude hook](cordum-hook.md)
- [cordum-agentd](cordum-agentd.md)
- [Edge evidence export](../edge-export.md)
- [Audit subsystem](../audit.md)

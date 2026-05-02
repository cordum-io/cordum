# Edge observability

Cordum Edge P0 emits metrics, structured logs, and audit/SIEM events for the
Claude command-hook + local agentd + Gateway Edge API path. This observability
surface is compliance evidence, not a second job lifecycle: Edge actions are
modeled as `EdgeSession -> AgentExecution -> AgentActionEvent`, and `job_id` is
populated only when an Edge execution is explicitly linked to a real Cordum Job
or workflow run.

## Metrics

Edge metrics are emitted through `core/edge.Recorder`; call sites must not create
ad hoc Prometheus metrics. The Prometheus implementation uses namespace
`cordum_edge`. Tenant values passed to the recorder are intentionally ignored by
metric labels to avoid cardinality blow-ups; tenant correlation belongs in logs
and SIEM events.

| Metric | Type | Labels | Notes |
|--------|------|--------|-------|
| `cordum_edge_sessions_created_total` | counter | `mode`, `agent_product` | Session creation. |
| `cordum_edge_sessions_ended_total` | counter | `mode`, `status` | Terminal session status. |
| `cordum_edge_sessions_active` | gauge | `mode` | Active session count. |
| `cordum_edge_executions_started_total` | counter | `mode`, `agent_product` | Agent execution start. |
| `cordum_edge_executions_ended_total` | counter | `mode`, `status` | Terminal execution status. |
| `cordum_edge_action_decisions_total` | counter | `layer`, `kind`, `decision`, `mode` | Policy/evaluate/hook decision count. |
| `cordum_edge_actions_denied_total` | counter | `layer`, `kind`, `reason_code` | Denials only; reason codes are bounded. |
| `cordum_edge_approvals_requested_total` | counter | `layer`, `kind` | Approval request surfaced. |
| `cordum_edge_approvals_resolved_total` | counter | `layer`, `kind`, `outcome` | `approved`, `rejected`, `expired`, `timeout`, `invalidated`, `consumed`. |
| `cordum_edge_degraded_total` | counter | `mode`, `component`, `reason_code` | Gateway/agentd/hook degraded outcomes. |
| `cordum_edge_fail_closed_total` | counter | `mode`, `reason_code` | Enterprise/local fail-closed outcomes. |
| `cordum_edge_artifact_exports_total` | counter | `artifact_type`, `result` | Evidence export attempts/results. |
| `cordum_edge_hook_latency_seconds` | histogram | `hook_event`, `decision` | Command-hook/agentd hook latency. |
| `cordum_edge_evaluate_latency_seconds` | histogram | `layer`, `kind`, `decision` | Gateway evaluate latency. |
| `cordum_edge_cache_lookups_total` | counter | `layer`, `kind`, `result` | agentd safe-allow cache hit/miss/expiry. |
| `cordum_edge_stream_clients` | gauge | none | Active Edge stream clients, summed across tenants. |
| `cordum_edge_stream_drops_total` | counter | `reason` | Edge stream drops; reasons below. |

Allowed label sets are deliberately small:

- `layer`: `hook`, `mcp`, `llm`, `runtime`, `workflow`, `system`, or `other`.
- `kind`: `hook.*`, `session.*`, `execution.*`, `mcp.*`, `llm.*`, `runtime.*`,
  `approval.*`, or `other`.
- `decision`: `allow`, `deny`, `require_approval`, `throttle`, `constrain`,
  `degraded`, `recorded`, `unknown`, or `other`.
- `mode`: `observe`, `local-dev`, `local-dev-enforce`, `enterprise-strict`,
  `workflow`, `unknown`, or `other`.
- `stream_drops_total.reason`: `marshal_error`, `client_buffer_full`,
  `tenant_filter`, `stopped`, `unknown`, or `other`.

Never add raw command strings, file paths, prompts, signed URLs, full session IDs,
event IDs, approval refs, rule IDs, arbitrary error strings, or bearer/API tokens
as metric labels. The normalizers collapse unrecognized or secret-shaped input to
`other`/`unknown`.

## Structured logs

Use the shared attribute builders in `core/edge/observability.go`:

- `EventLogAttrs` for `AgentActionEvent`
- `SessionLogAttrs` for `EdgeSession`
- `ExecutionLogAttrs` for `AgentExecution`
- `ApprovalLogAttrs` for approvals
- `ExportResultLogAttrs` for artifact/export results
- `HookSummaryLogAttrs` for hook outcomes
- `EvaluateSummaryLogAttrs` for evaluate outcomes
- `ErrorLogAttrs` for bounded, redacted errors

These helpers emit only bounded IDs, enum-like fields, timestamps, hashes, counts,
redaction level, and status/decision metadata. They intentionally do not log raw
`InputRedacted` maps, labels, raw hook payloads, prompts, tool output, approval
reason text, signed artifact URIs, Authorization headers, or API tokens.

## Audit / SIEM events

Edge audit events reuse the existing audit pipeline (`core/audit.AuditSender` and
`audit.SIEMEvent`). No new SIEM product or transport is introduced by Edge.

| Event type | When emitted | Severity |
|------------|--------------|----------|
| `edge.session_started` | Edge session creation | `INFO` |
| `edge.session_ended` | Edge session terminal state | `INFO` / `HIGH` for failed/degraded |
| `edge.execution_started` | Agent execution creation | `INFO` |
| `edge.execution_ended` | Execution terminal state | `INFO` / `HIGH` for failed/degraded |
| `edge.action_attempted` | Reserved action-attempt evidence | `INFO` |
| `edge.policy_decision` | Allow/recorded policy decision | `INFO` |
| `edge.action_denied` | Deny/throttle outcome | `HIGH` / `MEDIUM` |
| `edge.approval_requested` | `REQUIRE_APPROVAL` decision or approval record | `MEDIUM` |
| `edge.approval_resolved` | Approval terminal outcome | `INFO` / `MEDIUM` / `HIGH` |
| `edge.approval_rejected` | Explicit rejection | `HIGH` |
| `edge.approval_expired` | Approval expired/timed out | `MEDIUM` |
| `edge.artifact_exported` | Evidence/session export attempt | `INFO`, `MEDIUM`, or `HIGH` by result |
| `edge.agentd_degraded` | Gateway/agentd/hook degraded path | `MEDIUM`; `HIGH` for local-dev-enforce |
| `edge.fail_closed` | Enterprise-strict fail-closed denial | `CRITICAL` |

Safe `Extra` fields include bounded session/execution/event IDs, layer, kind,
tool name, input/action hashes, policy snapshot, approval ref, artifact type,
result, retention/redaction level, component, mode, and bounded reason code.
Raw `DecisionReason`, `ErrorMessage`, `Reason`, `ResolutionReason`,
`InputRedacted`, arbitrary labels, signed URLs, prompts, and tool payloads are not
placed in SIEM `Extra`.

Audit emission is best-effort and must not change policy/evaluate/hook decisions
if the audit pipeline is unavailable.

## Streams and idempotency

Edge action events are forwarded to the existing Gateway WebSocket stream via the
EDGE-007 bridge. The generic `cordum_gateway_ws_*` metrics remain the source for
WebSocket transport health; Edge-specific stream metrics count only Edge stream
pressure/drop reasons at the bridge. Tenant filtering and quarantine/redaction
behavior are preserved.

Edge event idempotency conflicts return the standard Edge error envelope
`{code, message, request_id, details?}`. Error details are centrally redacted
before serialization, so idempotency keys, signed URLs, bearer tokens, and raw
payload snippets cannot leak to clients.

## Redaction rules

- Do not store raw unredacted Claude hook payloads, prompts, tool outputs, or
  transcripts in Redis events, logs, docs, metrics, or audit events.
- Persist large/redacted evidence via artifact pointers and hashes.
- Use `RedactValue`, `RedactJSON`, or the existing mapper/classifier redaction
  helpers before persistence or logging.
- Synthetic examples in docs/tests may use obvious fake tokens only to prove the
  no-leak contract; never paste real payloads or credentials.

## See also

- [Edge Claude hook](edge/cordum-hook.md)
- [cordum-agentd](edge/cordum-agentd.md)
- [Edge evidence export](edge-export.md)
- [Audit subsystem](audit.md)

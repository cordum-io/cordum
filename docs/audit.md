# Audit Subsystem

This document describes the audit event pipeline, SIEM export, and dashboard UI.

Source code:

- `core/audit/exporter.go` — SIEM event schema and export factory
- `core/audit/buffer.go` — Buffered async export with retry
- `core/audit/webhook.go` — Webhook (HTTP POST) backend
- `core/audit/syslog.go` — Syslog (RFC 5424) backend
- `core/audit/datadog.go` — Datadog HTTP intake backend
- `core/audit/cloudwatch.go` — AWS CloudWatch Logs backend
- `core/audit/nats.go` — NATS-based audit event consumer
- `core/controlplane/gateway/gateway.go` — HTTP request audit (`AuditEvent`)
- `core/controlplane/gateway/policybundles/audit.go` — Policy bundle audit entries
- `dashboard/src/pages/AuditLogPage.tsx` — Audit log dashboard page
- `dashboard/src/components/audit/` — Audit UI components

## 1. Overview

Cordum emits structured audit events for security-relevant actions: safety
decisions, approvals, policy changes, violations, and authentication events.
Events are written to Redis and optionally exported to external SIEM systems
via one of four configurable backends.

<!-- TODO: detailed data flow diagram — gateway emits events → Redis list → consumer reads → buffer → exporter -->

## 2. Event Types

The audit subsystem defines these event types (from `core/audit/exporter.go`
and migration helpers in `core/audit/config.go`):

| Constant | Value | Description |
|----------|-------|-------------|
| `EventSafetyDecision` | `safety.decision` | Safety kernel allow/deny/throttle decisions |
| `EventPolicyDecisionV2` | `policy.decision.v2` | Unified Policy Studio `Decision` event emitted during the job/edge policy migration window |
| `EventSafetyApproval` | `safety.approval` | Human approval or rejection of gated jobs |
| `EventPolicyChange` | `safety.policy_change` | Policy configuration changes |
| `EventSafetyViolation` | `safety.violation` | Safety policy violations |
| `EventSystemAuth` | `system.auth` | Authentication events (login, key creation, user management) |

### Policy Studio unified decisions (Backend 3 + Backend 4)

Job-side Safety Kernel decisions and Edge policy decisions are migrating from
their legacy shapes (`safety.decision` and `edge.*`) to the unified
`core/policy.Decision` shape used by Policy Studio. During the transition,
matched-rule decisions can be written in both shapes through the existing SIEM
and hash-chain machinery.

`policy.decision.v2` uses the same `SIEMEvent` envelope so existing exporters
and chain verification continue to work. The normalized fields are:

| Field | Mapping |
|-------|---------|
| `event_type` | `policy.decision.v2` |
| `decision` | Unified `Decision.Type` (`allow`, `deny`, `require_human`, `throttle`, `allow_with_constraints`, `quarantine`, `redact`) |
| `matched_rule` | Unified `Decision.RuleID` |
| `policy_version` | Unified `Decision.BundleVersion` when present, otherwise the legacy policy snapshot |
| `reason` | First trace reason, falling back to the legacy reason |
| `extra.source` | Unified `Decision.Source` (`job` for Safety Kernel decisions, `edge` for Edge classifier decisions) |
| `extra.bundle_id` / `extra.bundle_version` | Optional bundle binding metadata |
| `extra.input_ref` / `extra.output_ref` | Optional blob references for inputs/outputs |
| `extra.audit_hash` | Optional unified decision audit hash |

Emission mode is controlled by `AUDIT_UNIFIED_DECISION_MODE`:

| Value | Behavior |
|-------|----------|
| unset / `dual` | Default. Emit the legacy decision event first (`safety.decision` for jobs or the matching `edge.*` event for Edge), then `policy.decision.v2` for matched-rule decisions. |
| `legacy` | Emit only the legacy decision shape. Use for rollback if downstream SIEM rules are not ready for v2. |
| `unified` | Emit only `policy.decision.v2` for matched-rule decisions. Use after downstream consumers cut over. |

Values are case-insensitive. Invalid values safely default to `dual` and are
logged once. Decisions without a matched rule remain legacy-only until the
unified evaluator can provide a stable rule/default identifier; this avoids
inventing unverifiable rule ids in the audit chain. The shared reader helper
folds job and Edge `policy.decision.v2` events into one stream by parsing
`extra.source`.

Edge has two persisted evidence paths during the migration window:
Gateway `/edge/evaluate` and agentd `/edge/events` decision evidence. Fresh
agentd evidence that is derived from a Gateway evaluate response carries the
Gateway decision event id; the Gateway verifies that referenced event in the
same execution before suppressing duplicate audit emission. Cache-hit,
degraded, and local-only agentd evidence has no such verified reference and
continues to emit audit normally.

Rollback/cutover guidance:

1. Start with the default `dual` mode and update SIEM dashboards/alerts to read
   `policy.decision.v2` while keeping legacy queries active.
2. If a downstream consumer breaks, set `AUDIT_UNIFIED_DECISION_MODE=legacy`
   and restart the affected producer; no data migration is required because
   both shapes use the same chain/export pipeline.
3. After all consumers read v2, set `AUDIT_UNIFIED_DECISION_MODE=unified`.
   Keep legacy parsing code for at least one retention window so historical
   audit-chain verification can still display old events.

Edge-specific notes:

- `ALLOW`, `DENY`, `REQUIRE_APPROVAL`, `THROTTLE`, `CONSTRAIN`, and
  `RECORDED` continue to be persisted on `AgentActionEvent` for compatibility.
- Unified Edge decisions use `extra.source=edge`; `RECORDED` maps to
  `Decision.Type=allow` with a trace marker that it was observation-only.
- Gateway evaluate decisions and agentd local/cache/degraded decision evidence
  both bridge into this shared transition path, while non-decision Edge events
  remain outside the policy decision stream.

### Output Policy events (added 2026-04)

Two-phase output safety scanning (`docs/output-policy.md`) emits the
following events through the same SIEM pipeline:

| Constant | Value | Description |
|----------|-------|-------------|
| `EventPolicyDecision` | `policy.decision` | Output policy `ALLOW` / `QUARANTINE` / `REDACT` decision (one per scan) |
| `EventPolicyScan` | `policy.scan` | Per-scanner scan result with finding type (`secret_leak`, `pii`, `injection`) and confidence |
| `EventPolicyQuarantine` | `policy.quarantine` | Job entered `OUTPUT_QUARANTINED` state with remediation pointer |
| `EventPolicyOverride` | `policy.override` | Operator-issued override that releases a quarantined job (admin-only; logged with actor + reason) |
| `EventPolicyReplay` | `policy.replay` | Historical scan rerun against the current policy (used by Replay tab) |

### Governance Timeline events (added 2026-04)

The Governance Timeline (dashboard surface backed by
`/api/v1/governance/decisions`) consumes the same audit log via a new
event type:

| Constant | Value | Description |
|----------|-------|-------------|
| `EventGovernanceTimeline` | `governance.timeline.entry` | Composite entry that joins a `safety.decision` (or output `policy.decision`) with its approval, replay, and override history for a single job/run |

Governance Timeline entries are not duplicates of the underlying
`safety.decision` events — they are derivation views materialized by
the gateway for narrative inspection in the dashboard. Both are
exported, but downstream consumers should de-duplicate on `job_id` +
`event_type` if they want raw decisions only.

### Edge events (Cordum Edge P0)

Cordum Edge reuses the same `SIEMEvent` export pipeline for local agent
governance evidence. Edge events describe `EdgeSession -> AgentExecution ->
AgentActionEvent` evidence; they are **not** Cordum Job lifecycle events unless
the execution is linked to a real production `job_id` or workflow run.

| Constant | Value | Description |
|----------|-------|-------------|
| `EventEdgeSessionStarted` | `edge.session_started` | Edge session creation |
| `EventEdgeSessionEnded` | `edge.session_ended` | Edge session terminal state |
| `EventEdgeExecutionStarted` | `edge.execution_started` | Agent execution creation |
| `EventEdgeExecutionEnded` | `edge.execution_ended` | Agent execution terminal state |
| `EventEdgePolicyDecision` | `edge.policy_decision` | Allow/recorded Edge policy decision |
| `EventEdgeActionDenied` | `edge.action_denied` | Deny/throttle outcome |
| `EventEdgeApprovalRequested` | `edge.approval_requested` | Human approval required/requested |
| `EventEdgeApprovalResolved` | `edge.approval_resolved` | Approval reached terminal outcome |
| `EventEdgeApprovalRejected` | `edge.approval_rejected` | Approval explicitly rejected |
| `EventEdgeApprovalExpired` | `edge.approval_expired` | Approval expired/timed out |
| `EventEdgeArtifactExported` | `edge.artifact_exported` | Evidence/session export attempt |
| `EventEdgeAgentdDegraded` | `edge.agentd_degraded` | Gateway/agentd/hook degraded mode |
| `EventEdgeFailClosed` | `edge.fail_closed` | Enterprise/local fail-closed denial |

Edge `extra` fields are bounded/redacted: session/execution/event IDs, layer,
kind, tool name, hashes, policy snapshot, approval ref, artifact type/result,
mode/component, and stable reason codes. Raw prompts, tool payloads, signed URLs,
approval reason text, `InputRedacted` maps, arbitrary labels, bearer tokens, and
API keys must never be placed in SIEM `extra`.

Matched Edge decisions additionally emit `policy.decision.v2` according to
`AUDIT_UNIFIED_DECISION_MODE`; legacy Edge event ordering is preserved in dual
mode.

See [Edge observability](edge-observability.md) for the full metric, log, and
audit contract.

Severity levels: `CRITICAL`, `HIGH`, `MEDIUM`, `LOW`, `INFO`.

<!-- TODO: document which actions map to which event types and severities -->

## 3. SIEM Event Schema

Each exported event uses the `SIEMEvent` struct:

| Field | Type | Description |
|-------|------|-------------|
| `timestamp` | `time.Time` | Event timestamp |
| `event_type` | `string` | One of the event type constants above |
| `severity` | `string` | Severity level |
| `tenant_id` | `string` | Tenant that triggered the event |
| `agent_id` | `string` | Agent involved (if applicable) |
| `job_id` | `string` | Job involved (if applicable) |
| `action` | `string` | Specific action taken |
| `decision` | `string` | Safety decision (allow/deny/require_approval/throttle) |
| `matched_rule` | `string` | Policy rule that matched |
| `reason` | `string` | Human-readable reason |
| `risk_tags` | `[]string` | Risk tags from the job request |
| `capabilities` | `[]string` | Capabilities from the job request |
| `policy_version` | `string` | Active policy version |
| `identity` | `string` | Actor identity |
| `extra` | `map[string]string` | Additional context |

## 4. HTTP Request Audit

The gateway logs every HTTP request as an `AuditEvent` (defined in
`gateway.go`) capturing method, route, status, duration, tenant, principal,
role, and auth source. This is separate from the SIEM export pipeline.

<!-- TODO: document how HTTP audit events are stored and queried -->

## 5. Action-Level Audit

The gateway records fine-grained audit entries via `appendAuditEntryNamed` for:

- Job approvals and rejections (including failure reasons)
- User creation, update, deletion, password changes
- API key creation and revocation
- Workflow run cancellations
- Policy bundle operations

<!-- TODO: document the Redis storage format and query patterns for action audit entries -->

## 6. Query API

- `GET /api/v1/policy/audit` — List policy audit entries

<!-- TODO: document query parameters, pagination, filtering, and response format -->

## 7. SIEM Export Configuration

| Env Var | Description |
|---------|-------------|
| `CORDUM_AUDIT_EXPORT_TYPE` | Export backend: `webhook`, `syslog`, `datadog`, `cloudwatch`, or `none` |
| `CORDUM_AUDIT_BUFFER_SIZE` | Async buffer size for export batching |
| `CORDUM_AUDIT_EXPORT_MAX_RETRIES` | Max retry attempts for failed exports |
| `AUDIT_UNIFIED_DECISION_MODE` | Job-policy decision shape: `dual` (default), `legacy`, or `unified` |

### Webhook

| Env Var | Description |
|---------|-------------|
| `CORDUM_AUDIT_EXPORT_WEBHOOK_URL` | HTTP POST endpoint for audit events |
| `CORDUM_AUDIT_EXPORT_WEBHOOK_SECRET` | HMAC signing secret for webhook payloads |

### Syslog (RFC 5424)

| Env Var | Description |
|---------|-------------|
| `CORDUM_AUDIT_EXPORT_SYSLOG_ADDR` | Syslog server address (e.g., `tcp://host:514`) |

### Datadog

| Env Var | Description |
|---------|-------------|
| `CORDUM_AUDIT_EXPORT_DD_API_KEY` | Datadog API key |
| `CORDUM_AUDIT_EXPORT_DD_SITE` | Datadog site (default: `datadoghq.com`) |
| `CORDUM_AUDIT_EXPORT_DD_TAGS` | Comma-separated tags (e.g., `env:prod,team:platform`) |

### AWS CloudWatch Logs

| Env Var | Description |
|---------|-------------|
| `CORDUM_AUDIT_EXPORT_CW_LOG_GROUP` | CloudWatch log group name |
| `CORDUM_AUDIT_EXPORT_CW_LOG_STREAM` | CloudWatch log stream name |

## 8. Dashboard UI

The audit log page (`/audit`) provides:

- **AuditFiltersBar** — Filter by event type, severity, tenant, time range
- **AuditTimeline** — Chronological event visualization
- **AuditEventCard** — Individual event summary cards
- **AuditDetailPanel** / **AuditEntryDetail** — Expanded event details
- **AuditIntegrityPanel** — Cryptographic integrity verification
- **AuditExport** — Export filtered results
- **AuditTransportBadge** — Transport type indicator
- **SavedFiltersDropdown** — Reusable filter presets

<!-- TODO: screenshots and detailed UI workflow documentation -->

## See Also

- [configuration-reference.md](configuration-reference.md) — Full env var reference
- [edge-observability.md](edge-observability.md) — Edge metrics, structured logs, and SIEM event contract
- [production.md](production.md) — Production hardening (audit export setup)

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
- `core/controlplane/gateway/handlers_audit_events.go` — SIEM-feed list endpoint (`GET /api/v1/audit/events`); see [`audit/list-api.md`](audit/list-api.md) for the contract
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

The audit subsystem defines these event types (from `core/audit/exporter.go`):

| Constant | Value | Description |
|----------|-------|-------------|
| `EventSafetyDecision` | `safety.decision` | Safety kernel allow/deny/throttle decisions |
| `EventSafetyApproval` | `safety.approval` | Human approval or rejection of gated jobs |
| `EventPolicyChange` | `safety.policy_change` | Policy configuration changes |
| `EventSafetyViolation` | `safety.violation` | Safety policy violations |
| `EventSystemAuth` | `system.auth` | Authentication events (login, key creation, user management) |

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
| `EventEdgeApprovalRequested` | `edge.approval_requested` | Human approval required/requested; lifecycle context only |
| `EventEdgeApprovalResolved` | `edge.approval_resolved` | Approval reached terminal outcome; approved outcomes can satisfy Edge provenance |
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

**Descriptive action targets.** `edge.action_denied` and `edge.policy_decision`
additionally carry a hard-coded allowlist of classifier-derived descriptors so a
responder can see *what class of thing* was targeted without any raw
path/command/prompt: `target_class` (`secret`/`source_code`/`file`/`unknown`),
`target_sensitive_area` (`auth`), `target_traversal` (`true`), `command_class`
(`destructive`/`deploy`/`network`/`dependency_change`/`safe`/`unknown`),
`command_family` (e.g. `filesystem_delete`/`git_push`/`network_egress`/`install`),
`mcp_server` / `mcp_tool` / `mcp_action`, and `runtime_event`
(`process.exec`/`file.read`/`file.write`/`network.connect`/`dns.query`) /
`runtime_host`. A composed `target_summary` gives one pivotable string —
`shell:<class>/<family>`, `file:<class>/<area>`, `mcp:<server>/<action>`, or
`runtime:<event>/<host>` (e.g. `shell:destructive/filesystem_delete`,
`file:secret`, `mcp:github/create_issue`). Each key is copied from classifier
output only (never raw input), bounded, and emitted only when present;
caller-supplied labels are never copied. The full classifier label set is
available to auditors via `events[].labels` in the session export bundle
(`edge.export.v1`, unchanged).

For destructive Edge actions, `ProvenanceGate` accepts only a resolved approval
audit event with decision `approved` or `approve` and exact tenant,
`approval_ref`, and `action_hash` matches. A requested-only approval event does
not prove approval and is treated as missing provenance.

See [Edge observability](edge/observability.md) for the full metric, log, and
audit contract.

Severity levels: `CRITICAL`, `HIGH`, `MEDIUM`, `LOW`, `INFO`.

<!-- TODO: document which actions map to which event types and severities -->

### Event categories (governance vs routine)

Every event type is classified as **governance** (security-relevant — the
events an auditor cares about) or **routine** (high-volume operational
telemetry). The classification is a single source of truth in
`core/audit/soc2.go` (`CategoryFor` / `IsGovernanceEvent`, keyed off the
`Event*` constants and CI-guarded so no constant can ship uncategorised) and is
reused by both the compliance export and the `/api/v1/audit/events` read
surface. **Unknown or newly added event types fail open to `governance`**, so a
security event is never silently hidden from a governance-filtered view.

**Routine** (operational telemetry): `system.auth`, `audit.read.events`,
`edge.session_started`, `edge.session_ended`, `edge.execution_started`,
`edge.execution_ended`, `edge.action_attempted`, `mcp.tool_invocation`,
`mcp.tool_outbound_invocation`, `mcp.tool_called`, `worker_handshake`,
`topic_registered`, `topic_unregistered`.

**Governance** (everything else): `safety.*`, `delegation.*`,
`auth.api_key_*` / `auth.role_*`, `edge.policy_decision`, `edge.action_denied`,
`edge.approval_*`, `edge.artifact_exported`, `edge.fail_closed`,
`edge.agentd_degraded`, `shadow_agent.*`, `shadow_eval`, `governance.*`,
`actiongate.denied`, `approval.revision_mismatch`, `license.*`,
`mcp.tool_approval`, `mcp.tool_denied`, `mcp.signature_invalid`,
`heartbeat_disagreement`, `worker_trust_change`.

> **Borderline calls** (review here; adjust the map in `soc2.go` if your threat
> model differs): the edge session/execution lifecycle and
> `edge.action_attempted` are **routine** — they fire on every action, not just
> denials. `mcp.tool_invocation` / `mcp.tool_outbound_invocation` are **routine**
> call-volume telemetry, whereas `mcp.tool_approval` / `mcp.tool_denied` are
> **governance**. `topic_registered` / `topic_unregistered` are **routine**;
> `heartbeat_disagreement` and `edge.artifact_exported` are **governance**.

Both the compliance export and the events endpoint filter on this taxonomy via
`?category=governance` (or `routine`) — see §6 and §7.

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

### Actor identity

Every authenticated governance event (`safety.decision`, `safety.approval`, job
submit, and approve/reject) records a non-empty `identity` describing **who**
performed the action. The value is resolved identically on the HTTP and gRPC
transports — both route through `policybundles.ActorIdentity` — so the same
credential always produces the same identity regardless of transport.

Two companion fields travel inside `extra`:

- `identity_source` — how the identity was derived (taxonomy below).
- `identity_label` — the human-readable key name, when the key has one, so a
  reviewer can read `ci-deploy` next to the stable id `mk_3f9c`.

**`identity_source` taxonomy**

| `identity_source` | When | `identity` value | `identity_label` |
|-------------------|------|------------------|------------------|
| `principal` | The credential is bound to a principal | The principal id | _(empty)_ |
| `api_key:<id>` | Authenticated by API key with no bound principal; `<id>` is the **stable key id** — a managed key's id, or `static:<fp>` for a static key | The same stable key id | The key's name, e.g. `ci-deploy` |
| `api_key_fp` | Defense-in-depth fallback when only a raw key is present | `sha256(key)[:12]` fingerprint | _(empty)_ |

Example — a managed key `mk_3f9c` named `ci-deploy`, used without a bound
principal, emits `identity = "mk_3f9c"`, `identity_source = "api_key:mk_3f9c"`,
and `identity_label = "ci-deploy"`.

**Actor vs. agent.** `identity` / `identity_source` / `identity_label` describe
the **actor** — the authenticated caller (human principal or API key) that made
the request. They are distinct from the **agent** dimension
(`agent_id` / `agent_name` / `agent_risk_tier`, including the `unlinked`
sentinel), which describes the workload the job runs as. The two are resolved
independently: a key-authenticated submit on behalf of an `unlinked` agent still
records a precise actor identity.

**Raw keys are never written.** No audit event, `extra` value, or log line ever
contains a raw API key — only the principal id, the stable key id, the key name,
or a truncated `sha256(key)[:12]` fingerprint. This is enforced by a test that
serializes a key-derived event and asserts the raw key is absent.

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
- `GET /api/v1/audit/events` — Paginated SIEM-feed read surface over the
  per-tenant audit chain (see [`audit/list-api.md`](audit/list-api.md))

`/api/v1/audit/events` accepts these additive filters (all optional; an absent
param means "no constraint on that dimension"):

| Query param | Description |
|-------------|-------------|
| `event_type` | Exact event type, matched case-insensitively |
| `severity` | One of `CRITICAL`/`HIGH`/`MEDIUM`/`LOW`/`INFO`, case-insensitive |
| `category` | `governance` or `routine` (see §2); an unknown value is rejected with `400 INVALID_CATEGORY` |
| `search` | Substring match over action / type / agent / job / identity / reason |
| `from` / `to` | RFC 3339 timestamp bounds (inclusive) |
| `limit` | Page size, capped at 200 |
| `cursor` | Opaque Redis stream id from the previous page's `next_cursor` |

`category` is resolved per row from the governance/routine taxonomy, so
`?category=governance` returns only security-relevant events and drops routine
telemetry such as `system.auth` and `mcp.tool_invocation`. The applied
`event_type` / `severity` / `category` filters are reflected in the
`audit.read.events` meta-event so the read surface is itself auditable.

<!-- TODO: document the full response envelope (items / next_cursor / returned) -->

## 7. SIEM Export Configuration

| Env Var | Description |
|---------|-------------|
| `CORDUM_AUDIT_EXPORT_TYPE` | Export backend: `webhook`, `syslog`, `datadog`, `cloudwatch`, or `none` |
| `CORDUM_AUDIT_BUFFER_SIZE` | Async buffer size for export batching |
| `CORDUM_AUDIT_EXPORT_MAX_RETRIES` | Max retry attempts for failed exports |

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

### Compliance export filters (`GET /api/v1/audit/export`)

The on-demand compliance export streams the per-tenant audit chain as NDJSON or
CSV with a leading manifest. Alongside `format` / `from` / `to` / `excel` /
`limit`, it accepts the same row filters as the events endpoint (§6):

| Query param | Description |
|-------------|-------------|
| `event_type` | Exact event type, matched case-insensitively |
| `severity` | `CRITICAL`/`HIGH`/`MEDIUM`/`LOW`/`INFO`, case-insensitive (unknown → `400`) |
| `category` | `governance` or `routine` (see §2); unknown → `400 invalid category` |

The applied filter is echoed in the response headers
(`X-Cordum-Export-Filter-Category` / `-Severity` / `-Event-Type`) and recorded
in the manifest:

| Manifest field | Meaning |
|----------------|---------|
| `row_filter_applied` | `true` when any row filter was applied (always present) |
| `row_filter` | The applied `{event_type, severity, category}` (omitted when none) |

**A filtered export is not a tamper gap.** The manifest's `event_count` is the
**post-filter** row count, but `chain_verification` always runs over the
**full** range — the filter only gates which rows are *emitted*, never the
integrity check. A governance-only export can therefore show an `event_count`
far smaller than the verified chain length; the `row_filter` record is exactly
what tells an auditor that difference is a filter, not missing events.
`GET /api/v1/audit/verify` is unaffected by export filters.

> **Truncation caveat.** `limit` / `MaxEvents` counts events *before* the row
> filter is applied. A narrow `?category=governance` filter over a very noisy
> range can hit the cap and set `truncated_at_max` before emitting many
> governance rows. When you see `truncated_at_max` together with a `row_filter`,
> narrow the `from`/`to` window rather than raising the cap.

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

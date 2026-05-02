# ADR-010: Edge P0 Architecture Decisions and Acceptance Gate

- Status: Proposed
- Date: 2026-04-30

## Context

EDGE-000 passed the Week 0 Claude Code deny spike: Claude Code `2.1.123` fired the `PreToolUse:Bash` hook before execution, sent `tool_name=Bash` plus `tool_input.command`, blocked `rm -rf`, surfaced the Cordum denial reason, and adapted after denial. The evidence is recorded in `docs/demo-edge-claude-spike.md`.

That pass unblocks P0 planning, not unrestricted production implementation. The spike used an HTTP hook only because it was the fastest validation path. HTTP hooks can deny with a 2xx JSON response, but connection failures, non-2xx responses, and timeouts are non-blocking in Claude Code. Production enforcement therefore must use a local command hook (`cordum-hook`) that talks to local `cordum-agentd` and fails closed in strict modes.

This ADR resolves the non-obvious P0 decisions before backend implementation starts:

- `AgentExecution` semantics relative to existing `Job` and workflow state.
- Approval UX defaults and the role of `approval_ref` retry guidance.
- `cordum-agentd` and Gateway unavailable behavior by policy mode.
- Token storage and managed-settings tradeoffs.
- Shadow Agents scope for P0.
- The exact P0 acceptance checklist and Moe task mapping.

## Decision

### EDGE-000 gate result

EDGE-000 PASS is the gate condition for continuing P0 planning. It does **not** make HTTP hooks acceptable for production enforcement.

Production Claude Code enforcement must use:

- `type: "command"` hook configuration for `cordum-hook`.
- Local `cordum-agentd` over a loopback-only HTTP endpoint (default `CORDUM_AGENTD_URL=http://127.0.0.1:8765/v1/edge/hooks/claude`). The hook client rejects non-loopback hosts so the boundary stays on the same machine; a future user-only Unix-domain-socket or named-pipe transport is a possible hardening, not a P0 prerequisite. This loopback HTTP boundary is unrelated to the EDGE-000 HTTP hook server, which remains a disposable spike (see below).
- Explicit timeout and degraded/fail-closed behavior.
- Structured deny JSON or command-hook exit-code semantics that block when a decision cannot be produced in strict modes.

### Production-shaped P0

EDGE-000's HTTP hook server is a disposable spike, but P0 after this ADR must be production-shaped rather than a separate throwaway demo. The demo and docs should exercise the real P0 architecture path with feature flags and safe defaults: command hook, local `cordum-agentd`, Gateway Edge APIs, Safety Kernel policy/evaluate, approvals, redaction, EdgeSession/AgentActionEvent logs, audit events, artifact pointers, dashboard, docs, and tests.

Do not build a separate demo-only stack that has to be deleted or rebuilt for production. Demo polish belongs on top of the production-shaped components and their failure modes.

### AgentExecution semantics

`Job` remains Cordum's production work unit. Do not turn every agent action, coding-agent session, or hook callback into a `Job`, and do not duplicate the existing Job lifecycle state inside `AgentExecution`.

`AgentExecution` is evidence and runtime metadata for one governed agent run. It may be linked to a Job or workflow when one exists, but it is not the scheduler's source of truth for dispatch, retry, completion, or workflow progress. A Claude Code tool/action call never becomes a Cordum `Job` by itself; it becomes an ordered `AgentActionEvent` in the session log plus audit/evidence events for policy decisions, denials, approvals, redacted summaries, hashes, artifact pointers, and export evidence.

Use these relationships for P0:

- Local coding sessions: `EdgeSession -> AgentExecution -> AgentActionEvent`.
- Production workflow actions: `WorkflowRun/Step/JobAttempt -> AgentExecution -> AgentActionEvent` when the workflow step uses an external or internal agent.
- Direct production jobs: `Job/JobAttempt -> AgentExecution -> AgentActionEvent` when an agent runtime performs the job attempt.

Dashboard implications:

- The Edge Sessions surfaces own session/execution/action timelines.
- Job Detail and Workflow Run Detail may show linked `AgentExecution` panels only when a job or workflow link exists.
- Job status remains sourced from the existing Job/Workflow model, not from `AgentExecution.status`.
- Edge evidence can link back to jobs/workflows, but local coding sessions can exist without any Job.

### Approval and fail-mode defaults

Default real-world approval UX is immediate deny with an `approval_ref` and retry guidance. The hook/agentd path should create or reference an approval request, return a clear deny reason, and tell the operator how to retry after approval instead of holding an interactive terminal indefinitely.

Bounded inline wait is allowed only as an explicit local/demo opt-in. When enabled, the wait must have a configured timeout and timeout must return deny plus retry guidance. P0 must not rely on interactive `permissionDecision: "defer"`; defer/resume semantics are reserved for future non-interactive `claude -p` or SDK-style flows where the parent process can resume explicitly.

Policy-mode fail behavior:

| Mode | Gateway unavailable | `cordum-agentd` unavailable | Expected UX | Audit/degraded output |
|---|---|---|---|---|
| `observe` | Allow action when no enforcement decision is required; mark session degraded. | Warn that hooks are not enforcing; continue only if observe mode is explicit. | Yellow/degraded status, never silent success. | Emit degraded event with component and reason; do not record a false allow decision. |
| `local-dev enforce` | Deny high-risk actions; optionally allow cached known-safe actions only when cache policy says so. | Deny risky actions because the local enforcer is unavailable. | Clear terminal reason with `approval_ref` or retry/diagnostic guidance. | Emit policy unavailable / agentd unavailable event and include degraded status in EdgeSession. |
| `enterprise-strict` | Deny all governed actions that require a fresh decision. | Deny all governed actions because local enforcement cannot prove safety. | Fail closed with support/doctor instructions; no bypass-by-timeout. | Auditable fail-closed decision with tenant, mode, component, and bounded redacted reason. |
| `workflow requires=edge-governance` | Fail fast before dispatch or fail the action/attempt if already running. | Fail the job/attempt because required governance is unavailable. | Workflow-visible failure with retry-after-governance guidance. | Job/workflow audit event plus Edge degraded/fail-closed event linked when possible. |

### Security, token storage, and product scope

Token storage decisions:

- Do not put long-lived Cordum API keys, tenant admin tokens, or model provider secrets in Claude settings.
- Prefer the loopback-only `cordum-agentd` HTTP endpoint (default `http://127.0.0.1:8765/v1/edge/hooks/claude`) for hook-to-agentd communication. Remote Gateway URLs are rejected by the hook client, so the trust boundary stays on the same machine. A future user-only Unix-domain-socket or named-pipe transport remains a hardening option but is not required for P0.
- Treat the loopback agentd nonce as a token-class secret. `EDGE-017.4` resolves the settings-leak finding by keeping `CORDUM_AGENTD_URL` bare, injecting `CORDUM_AGENTD_HOOK_NONCE` only into the runtime hook process environment, and sending it as `X-Cordum-Agentd-Nonce`; the nonce must not appear in generated Claude settings, managed-settings JSON, persisted state, logs, metrics, or evidence exports.
- Use short-lived scoped session tokens only where a hook or wrapper must authenticate across a process boundary.
- Redact token-like values before logs, events, artifacts, dashboard state, or evidence exports.
- Enterprise deployments should use managed settings plus OS keychain, service bootstrap, or agentd-held credentials rather than user-editable secrets.
- Dev mode may use a scoped token in generated local settings only when the loopback HTTP boundary alone is insufficient; that token must be short-lived, least-privilege, and called out as a local-development tradeoff.

Shadow Agents scope:

- P0 has no full Shadow Agents nav item, list, detail page, store, scanner, or remediation dashboard.
- P0 may expose an optional local observe-only check through `cordumctl edge doctor` if it helps operators detect obvious ungoverned local Claude/Codex/Cursor processes.
- P0 must label any such check as observe-only, privacy-sensitive, and non-enforcing.
- Real shadow scanner, finding store, remediation generator, Kubernetes/CI detector, runtime sidecar, and dashboard surfaces are P3 work.

OSS / enterprise boundary:

| Area | Core / OSS P0 | Enterprise entitlement |
|---|---|---|
| Local demo | `cordumctl edge claude`, local `cordum-hook`/`cordum-agentd`, EdgeSession/event capture, demo policies. | Managed rollout packaging, fleet policy defaults, signed/notarized binaries if required by customer distribution. |
| Evidence | Basic EdgeSession event timeline, artifact pointers, and local evidence export. | SIEM/compliance export, long-retention policies, enterprise evidence packs, org-wide reporting. |
| Enforcement | Local dev enforce and workflow `requires=edge-governance` contracts. | Managed settings enforcement, bypass prevention, production cluster enforcement, fleet-wide fail-closed defaults. |
| Shadow/runtime detection | Optional local observe-only `edge doctor` signal. | Org-wide shadow scans, runtime sidecar/detector integrations, remediation workflows. |

## Acceptance Gate

P0 implementation may start only after this ADR is accepted and the production-gate/workflow-retry branch conflict is resolved by PR #241 or an equivalent fix, unless a human explicitly overrides that gate. The first implementation task allowed after EDGE-001 is **EDGE-002: Edge data model contracts and JSON schemas**.

### P0 acceptance checklist from PRD 24.7

| PRD 24.7 acceptance item | Moe task coverage | Gate expectation |
|---|---|---|
| `cordumctl edge claude` launches Claude Code with generated hook settings. | EDGE-015 through EDGE-021: command hook, `cordum-agentd`, CLI wrapper, generated settings, doctor checks. | Operator can run the wrapper locally and verify settings source without editing global Claude settings. |
| Dashboard shows a live EdgeSession. | EDGE-002/003 data and store; EDGE-005/006/007 APIs and stream; EDGE-022 through EDGE-024 dashboard list/detail/live timeline. | EdgeSession appears live with bounded, redacted action metadata and no Job lifecycle duplication. |
| PreToolUse events are stored and streamed. | EDGE-006/007 API + stream; EDGE-016/018 hook/agentd event ingestion. | Event includes hook name, tool name, redacted command/path fields, policy outcome, latency, and correlation IDs. |
| Reading `.env` or secret-like files is denied. | EDGE-008/009/010 policy; EDGE-015 through EDGE-018 enforcement path; EDGE-027/028 tests. | Denial happens before execution, reason reaches Claude/operator, and audit event is retained without secret content. |
| Editing protected files requires approval, and the dashboard can approve a pending action. | EDGE-011/012 approvals; EDGE-025 dashboard approval UI. | Default UX is deny + `approval_ref` + retry guidance; bounded inline wait is demo-only opt-in. |
| PostToolUse is audited and artifacts are captured. | EDGE-013 artifacts/export; EDGE-014 logs/audit/metrics/traces; EDGE-016 hook events. | Artifacts are pointers or redacted bounded content, not raw transcripts or secrets. |
| Evidence bundle can be exported. | EDGE-013 export; EDGE-029/030 docs/demo and operator runbook. | Export includes decision/event/artifact references needed for demo and QA, with redaction defaults. |
| Logs and telemetry are redacted. | EDGE-004 normalization/redaction, EDGE-014 observability, EDGE-031 security review. | No raw transcript, command payload, env file, token, or secret-like string is persisted by default. |
| Docs and demo can be followed end-to-end. | EDGE-029/030 docs/demo. | Demo script proves launch, denial, approval/retry, artifact export, and evidence review. |
| Basic Shadow Agent signal exists without expanding scope. | EDGE-021 optional `cordumctl edge doctor`; full Shadow Agents surfaces are explicitly P3. | P0 may report local observe-only diagnostics, but must not ship Shadow Agents nav/list/detail/store/scanner/remediation dashboard. |
| P0 does not weaken security or production gates. | EDGE-031 security; EDGE-032 final acceptance. | Strict modes fail closed, HTTP hook spike stays non-production, and backend work waits for PR #241/equivalent unless overridden. |

### Moe P0 task map

- EDGE-002/003: data model contracts, JSON schemas, and EdgeSession/action/artifact store.
- EDGE-005/006/007: `/api/v1/edge/*` APIs, event ingestion, live stream, and query surfaces.
- EDGE-008/009/010: policy evaluation, rule packs, protected-path defaults, and explainable deny/allow outcomes.
- EDGE-011/012: approval request model, approval/retry semantics, and audit linkage.
- EDGE-013: artifact pointers, evidence bundle export, and retention-aware packaging.
- EDGE-014: logs, audit events, metrics, traces, degraded/fail-closed telemetry, and OpenTelemetry coverage.
- EDGE-015 through EDGE-021: production command hook, local `cordum-agentd`, CLI wrapper, generated Claude settings, setup validation, and `edge doctor`.
- EDGE-022 through EDGE-026: Edge dashboard list/detail/live timeline, approvals UI, evidence export view, and UX polish.
- EDGE-027/028: unit/integration/E2E tests for policy, hook/agentd, API stream, approvals, dashboard, and failure modes.
- EDGE-029/030: docs, demo script, operator runbook, and customer-facing evidence walkthrough.
- EDGE-031: security and privacy review, redaction audit, token handling review, and strict-mode fail-closed verification.
- EDGE-032: final P0 acceptance pass against this ADR, PRD 24.7, and the roadmap gates.

## Consequences

Positive:

- P0 can proceed from a verified hook deny behavior without overstating HTTP hook safety.
- Production design remains fail-closed for strict enforcement modes.
- Backend and dashboard implementation tasks can share one decision record.

Tradeoffs:

- The local demo may still use explicit opt-in inline waits for operator clarity, but the default real-world approval path must avoid indefinite terminal blocking.
- P0 remains focused on Edge Sessions and Claude governance; broader Shadow Agent detection moves to later roadmap phases.

## Follow-up backlog notes

Future EDGE task plans must explicitly account for these discovered follow-ups instead of rediscovering them during implementation:

- `/api/v1/edge/*` API contracts need a standard error shape, request/trace IDs, tenant-safe details, body-size failures, policy-unavailable failures, approval-timeout failures, idempotency keys, and duplicate handling for event batch ingestion, approval responses, artifact export, and stream reconnects.
- Retention and privacy defaults need named environment/config flags before persistence ships, including session/event retention, artifact retention, redacted-snippet capture limits, raw transcript disablement, export retention, and tenant override rules. Defaults must avoid raw transcripts and secrets.
- Policy tooling should include `cordumctl edge policy simulate` and `cordumctl edge policy explain` so operators can preview allow/deny/approval outcomes against redacted sample actions before live enforcement.
- EDGE-014 must include OpenTelemetry traces across `cordum-hook -> cordum-agentd -> Gateway -> Safety Kernel -> approval/export`, with span attributes limited to stable IDs, categories, hashes, and redacted bounded fields.
- P2 LLM proxy tasks must cover Anthropic Messages `count_tokens`, required beta/header forwarding, streaming headers, model/version metadata, and session-id / trace-id joins back to EdgeSession evidence.
- Consider a dedicated MCP stdio/HTTP bridge task if MCP Gateway needs to front tools that only speak stdio while the dashboard/API layer expects HTTP/WebSocket semantics.
- P3 Shadow/Runtime work needs separate dashboard surfaces for findings, remediation, runtime events, Kubernetes/CI detectors, and privacy controls; do not smuggle those surfaces into P0.

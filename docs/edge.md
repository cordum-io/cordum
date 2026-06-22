# Cordum Edge

Cordum Edge is the Cordum Compliance Firewall for AI agents. It places a
policy, approval, and evidence boundary between an AI agent and the actions it
wants to take in a developer workspace or managed runtime.

**Cordum stays quiet until governance matters.** Developers see Cordum exactly
when it protects them, their team, and production: a risky command is denied, an
action needs approval, an approved action must be retried safely, or an audit
trail is needed.

## Product model

Edge P0 is built around the Claude Code command-hook path:

```text
Claude Code -> cordum-hook -> local cordum-agentd -> Cordum Gateway
            -> Safety Kernel -> approvals, events, artifacts, dashboard
```

The local pieces exist to capture and enforce the agent action before it runs.
Gateway and Safety Kernel remain the policy authority. Edge does not turn every
agent action into a Cordum Job; Edge records `EdgeSession` -> `AgentExecution` ->
`AgentActionEvent` evidence and links to a job or workflow run only when there is
a real production job.

CordClaw is part of Cordum Edge: it is the Edge execution-firewall/OpenClaw
adapter capability, not a separate product surface. Existing `cordclaw-*` rule
IDs, `job.cordclaw.*` policy topics, `CORDCLAW_*` environment variables, and
package/binary names remain stable compatibility identifiers under the Edge
umbrella.

## Data hierarchy

- **Tenant:** isolation boundary for all `/api/v1/edge/*` routes.
- **Principal:** human or service identity that starts or requests the action.
- **EdgeSession:** one governed agent session with policy mode/snapshot,
  heartbeat, lifecycle, and dashboard metadata.
- **AgentExecution:** one agent process within a session, including product,
  version, repo/cwd metadata, trace IDs, and terminal status.
- **AgentActionEvent:** ordered evidence for hook receipt, evaluate, decision,
  approval, degraded state, and artifact pointers.
- **Approval:** human approval state bound to action/input hashes and requester
  identity.
- **Artifact pointer:** metadata for evidence bodies stored outside Redis events,
  including URI, sha256, size, content type, retention class, and redaction level.

Raw prompts, tool payloads, transcripts, command output, API keys, bearer tokens,
and hook nonces must not be stored in Edge events. Use redacted summaries,
stable hashes, and artifact pointer metadata.

Governance events (`edge.action_denied`, `edge.policy_decision`) surface
*descriptive* action targets without violating that rule: a hard-coded allowlist
of classifier-derived enums is copied into the audit `extra` map — `target_class`,
`target_sensitive_area`, `target_traversal`, `command_class`, `command_family`,
`mcp_server` / `mcp_tool` / `mcp_action`, `runtime_event` / `runtime_host`, plus a
composed `target_summary` (e.g. `shell:destructive/filesystem_delete`,
`file:secret`, `mcp:github/create_issue`). No raw file path, command, prompt, or
secret is ever written — only these safe, bounded descriptors, and caller-supplied
labels are never copied. Auditors needing the complete label set read
`events[].labels` in the session export bundle (`edge.export.v1`, unchanged). See
[Edge observability](edge/observability.md) for the full key/value reference.

## Five P0 capabilities

1. **Sessions and executions** register, heartbeat, end, and inspect governed
   agent runs.
2. **Events and streams** append idempotent action evidence and stream compact
   `edge.event` updates to the dashboard.
3. **Policy/evaluate** classifies agent actions, calls Safety Kernel policy, and
   returns `ALLOW`, `DENY`, `REQUIRE_APPROVAL`, `THROTTLE`, `CONSTRAIN`, or
   evidence-only decisions.
4. **Approvals** let reviewers approve/reject actions while retry checks bind
   the decision to the original action and redacted input hashes. Destructive
   retries also require resolved approval audit provenance for the same tenant,
   `approval_ref`, and `action_hash`; a requested-only approval event is not
   enough.
5. **Artifacts and export** attach metadata-only artifact pointers and export an
   audit-ready session bundle without inlining raw evidence bodies.

## Enforcement layers

- `cordumctl edge claude` is the developer/demo launcher. It starts local
  `cordum-agentd`, renders temporary Claude command-hook settings, injects a
  runtime-only hook nonce, and launches Claude Code. It reserves the local
  `cordum-agentd` loopback port automatically (close-then-bind on Windows, where
  listener handle inheritance is Unix-only), so `--agentd-url` is not required on
  any platform.
- `cordum-hook` is the Claude command hook. It reads one bounded hook payload,
  redacts/maps it, and calls only the local agentd endpoint.
- `cordum-agentd` owns local session lifecycle, hook authentication, Gateway
  evaluate calls, optional safe-allow cache, optional local/demo inline approval
  wait, heartbeat, and shutdown evidence.
- Gateway and Safety Kernel enforce tenant auth, policy modes/snapshots,
  approvals, audit, metrics, and redaction before persistence. The canonical
  metric and audit-field inventory is [Edge observability](edge/observability.md).
- Enterprise managed settings, endpoint controls, binary trust, and
  keychain/service bootstrap are the fleet enforcement boundary. The developer
  wrapper alone does not stop a user from running raw `claude`.

## Policy modes

| Mode | Use | Behavior when Cordum governance is unavailable |
| --- | --- | --- |
| `observe` | Discovery and low-friction dev visibility. | Allow degraded, record evidence where possible. |
| `enforce` / `local-dev-enforce` | Local enforcement for risky or unknown actions. | Allow known-safe actions only; deny risky/unclassified actions. |
| `enterprise-strict` | Managed enterprise rollout. | Fail closed. |
| `requires-edge-governance` | Production workflow action requiring Edge. | Fail closed on Gateway miss regardless of session mode. |

## Retention and artifacts

Edge stores bounded session/event metadata in Gateway stores. Large evidence
bodies belong in the artifact store and are referenced by pointer metadata.
Session export returns JSON evidence plus artifact metadata and is capped by
`CORDUM_EDGE_EXPORT_MAX_BYTES`; P0 does not inline artifact bodies.

Redis evidence fanout is bounded by 100 executions per session and 5000 events
per execution (500,000 events/session worst case). `DeleteSession` cleans up via
bounded `ZSCAN Count=100`, `DEL` batches of at most 100 keys, a 30 second
foreground deadline, and a background retention sweeper. See
[Edge retention, caps, and cleanup](edge/retention.md) for the exact policy and
metrics.

## OSS and enterprise boundary

The OSS/P0 surface includes the Edge data model, Gateway routes, redaction and
hashing helpers, policy/evaluate, approvals, event stream, artifact pointers,
evidence export, and the Claude hook/agentd/CLI demo path. Enterprise rollout
adds managed Claude settings, endpoint controls, binary signing/notarization,
service bootstrap or keychain secret handling, SIEM/compliance export packs,
long-retention policies, and org-wide enforcement reporting.

## Identity contracts

Edge has a small set of identity contracts that bind components together
without colliding their namespaces — gateway-issued `event_id`,
agentd-prefixed evidence id, the `(tenant, session, execution,
action_hash)` approval-reuse tuple, and the approval_ref lifecycle. The
canonical reference is [Edge identity contract](edge/identity-contract.md).
Read it before touching any audit, evidence, approval, or cache code. In
operator terms, ProvenanceGate accepts only an approved
`EventEdgeApprovalResolved` / `edge.approval_resolved` event that matches the
tenant, `approval_ref`, and `action_hash`; malformed, missing, requested-only,
or mismatched evidence fails closed without persisting raw prompts, transcripts,
or tool payloads.

## Start here

- [Edge docs index](edge/README.md)
- [Claude Code wrapper guide](edge-claude-code.md)
- [Manual demo](demo-edge-claude.md)
- [Edge API reference](edge/api.md)
- [Edge configuration](edge/configuration.md)
- [Edge retention, caps, and cleanup](edge/retention.md)
- [Edge observability](edge/observability.md) — metrics, audit fields, and dashboard hook status.
- [Edge identity contract](edge/identity-contract.md) — IDs, hashes, approval lifecycle.
- Edge P0 threat model: internal Cordum engineering.

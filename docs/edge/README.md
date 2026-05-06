# Cordum Edge

Cordum Edge is the Cordum Compliance Firewall for AI agents. It records,
evaluates, and proves agent actions at the boundary where a developer or
managed runtime asks an agent to read, write, run, configure, or deploy.

**Cordum stays quiet until governance matters.** Developers see Cordum exactly
when it protects them, their team, and production: when an action needs a
policy decision, an approval, a retry after approval, or an audit trail.

This page is the Edge P0 index and product overview. For the root product page,
see [docs/edge.md](../edge.md). For the **30-minute new-engineer quickstart**
that walks from clean clone to a live governed Claude session, jump to
[docs/quickstart-edge.md](../quickstart-edge.md).

## What Edge governs

Edge P0 governs the Claude Code command-hook path:

```text
Claude Code -> cordum-hook -> local cordum-agentd -> Gateway /api/v1/edge/*
            -> Safety Kernel policy/evaluate -> approvals, events, artifacts
```

The path is production-shaped, not the Week 0 HTTP spike. `cordum-hook` is a
Claude Code command hook, `cordum-agentd` owns the local Edge session and
heartbeat, and Gateway/Safety Kernel own tenant-aware policy decisions. Edge
actions are **not** modeled as Cordum Jobs; jobs remain production work units
and are only linked when a real workflow/job exists.

CordClaw belongs to Cordum Edge. Treat CordClaw as the Edge
execution-firewall/OpenClaw adapter capability; legacy `cordclaw-*`,
`job.cordclaw.*`, `CORDCLAW_*`, and package/binary names are compatibility
identifiers within Edge, not a separate product namespace.

## Data hierarchy

| Level | Meaning | Stored evidence |
| --- | --- | --- |
| Tenant | Isolation boundary for all Edge APIs and evidence. | Existing Gateway auth plus `X-Tenant-ID`. |
| Principal | Human/service identity that started or requested the action. | `principal_id` on sessions, executions, approvals, and audit. |
| EdgeSession | One governed agent session. | lifecycle, policy mode/snapshot, dashboard URL, heartbeat/end state. |
| AgentExecution | One agent process or execution within the session. | agent product/version, cwd/repo metadata, trace IDs, end status. |
| AgentActionEvent | Ordered action evidence. | hook layer/kind, decision, hashes, approval ref, artifact pointer metadata. |
| Approval | Human decision for a `REQUIRE_APPROVAL` action. | approval ref, requester/reviewer, status, hashes, bounded notes. |
| Artifact pointer | Metadata for evidence bodies outside Redis events. | URI, sha256, size, content type, retention class, redaction level. |

Large raw transcripts, raw prompts, raw tool payloads, command output, bearer
tokens, and API keys do not belong in Redis Edge events. Events carry bounded
redacted summaries, hashes, and artifact pointers.

## P0 capabilities

| Capability | P0 surface | Read more |
| --- | --- | --- |
| Sessions and executions | Register, heartbeat, end, and inspect governed agent runs. | [API](api.md) |
| Events and streams | Append idempotent action events, batch evidence, and stream `edge.event`. | [API](api.md), [observability](observability.md) |
| Policy/evaluate | Classify actions, call Safety Kernel policy, and return allow/deny/approval/constrain decisions. | [policy](../edge-policy.md), [mapper](claude-hook-mapper.md) |
| Approvals | Create, list, approve/reject, and optionally wait on approvals with replay-safe hashes. | [API](api.md) |
| Artifacts and export | Attach artifact pointer metadata and export an audit-ready session evidence bundle. | [evidence export](../edge-export.md) |

## Enforcement layers

1. **Developer/demo launcher.** `cordumctl edge claude` starts a local
   `cordum-agentd`, generates temporary Claude command-hook settings, injects a
   process-only hook nonce, then launches Claude Code. This is the adoption and
   demo path, not an enterprise enforcement boundary by itself.
2. **Claude command hook.** `cordum-hook` receives one bounded hook JSON payload
   on stdin, maps it through the Claude mapper, and calls only local agentd. It
   never calls Gateway directly.
3. **Local agentd.** `cordum-agentd` owns session lifecycle, local hook auth,
   Gateway evaluate calls, optional safe-allow cache, optional local/demo inline
   approval wait, heartbeat, and shutdown evidence.
4. **Gateway and Safety Kernel.** `/api/v1/edge/evaluate` enforces tenant-aware
   auth, redaction, policy snapshot/mode, approval creation, and audit/metrics.
5. **Enterprise managed settings.** Managed Claude settings, endpoint controls,
   binary trust, keychain/service bootstrap, and optional proxy controls prevent
   bypass at fleet scale. The wrapper alone cannot stop a user from running raw
   `claude`.

## Policy modes and fail behavior

| Mode | Intent | Governance-unavailable behavior |
| --- | --- | --- |
| `observe` | Development visibility and low-friction evidence. | Allow degraded actions while recording evidence where possible. |
| `enforce` / `local-dev-enforce` | Local enforcement for risky or unknown actions. | Known-safe actions may proceed; risky or unclassified actions deny/fail closed. |
| `enterprise-strict` | Managed enterprise enforcement. | Fail closed when Cordum governance is unavailable. |
| `requires-edge-governance` tag | Production workflow action that must be governed. | Fail closed on Gateway miss regardless of session mode. |

The exact hook output mapping is in [cordum-hook](cordum-hook.md) and the
evaluate/cache/approval behavior is in [cordum-agentd](cordum-agentd.md).

## Retention, privacy, and artifacts

Edge keeps hot session/event state in the existing Gateway stores and keeps
large bodies out of events. Evidence bodies live behind artifact pointers with
retention metadata and redaction levels. P0 session export returns JSON evidence
plus artifact pointer metadata; it does not inline artifact bodies. Large exports
are capped by `CORDUM_EDGE_EXPORT_MAX_BYTES`.

Redis fanout is bounded at 100 executions per session and 5000 events per
execution. `DeleteSession` uses bounded scans and batched deletes, and Gateway
runs a 30-day retention sweeper by default. Details:
[retention, caps, and cleanup](retention.md).

Use redacted synthetic examples in docs, tests, and demos. Do not paste real API
keys, bearer tokens, `.env` contents, raw prompts, transcripts, command output,
or provider secrets into Edge events, settings files, issue comments, or docs.

## OSS vs enterprise boundary

| Included in OSS/P0 | Enterprise-managed boundary |
| --- | --- |
| Edge data model, Gateway routes, redaction/hashing, policy/evaluate, approvals, event stream, artifact pointers, evidence export, hook/agentd/CLI demo path. | Managed Claude settings rollout, endpoint controls, binary signing/notarization, service bootstrap/keychain secrets, SIEM/compliance export packs, long-retention policies, org-wide enforcement reporting. |

Use [managed settings templates](managed-settings-template.md) to understand the
enterprise shape, but do not treat the developer wrapper as fleet enforcement.

## Demo and operating paths

- **Manual demo:** [demo.md](demo.md) and the root
  [docs/demo-edge-claude.md](../demo-edge-claude.md).
- **CLI reference:** [cli.md](cli.md) and the existing
  [cordumctl edge claude contract](cordumctl-edge-claude.md). Use
  [cordumctl edge doctor](cordumctl-edge-doctor.md) for local diagnostics.
- **Configuration:** [configuration.md](configuration.md).
- **Retention:** [retention.md](retention.md).
- **API reference:** [api.md](api.md) plus the canonical
  [OpenAPI spec](../api/openapi/cordum-api.yaml).
- **Threat model:** Edge P0 threat model is internal Cordum engineering.

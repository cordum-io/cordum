# Cordum Edge for Claude Code

This guide explains how Cordum Edge governs Claude Code through the P0 command
hook path and `cordumctl edge claude` launcher.

**Cordum stays quiet until governance matters.** Developers see Cordum exactly
when it protects them, their team, and production: before a risky tool runs,
when approval is needed, and when evidence must be exported.

## Command

```bash
CORDUM_GATEWAY=http://localhost:8081 CORDUM_API_KEY=<cordum-api-key> CORDUM_TENANT_ID=default cordumctl edge claude -- --print "summarize this repo"
```

Use `cordumctl edge claude [edge flags] -- [claude args...]`. Cordum flags stay
before `--`; Claude arguments go after it. The wrapper supplies the governed
`--settings` file and rejects a forwarded `--settings` override.

See [edge/cli.md](edge/cli.md) for the full flag table.

## Hook and agentd behavior

The runtime path is:

```text
Claude Code command hook -> cordum-hook -> local cordum-agentd -> Gateway evaluate
```

- `cordum-hook` reads one bounded JSON payload from stdin, redacts/maps it, and
  calls only the local agentd URL.
- `cordum-agentd` owns Edge session/execution lifecycle, heartbeat, local hook
  authentication, Gateway evaluate calls, optional safe-allow cache, optional
  local/demo inline approval wait, and shutdown evidence.
- Gateway/Safety Kernel own tenant-aware policy evaluation, approvals, audit,
  metrics, and redaction before persistence.

## Settings generation

The wrapper renders temporary Claude command-hook settings with:

- command hooks for supported Claude events;
- a bare loopback `CORDUM_AGENTD_URL`;
- `CORDUM_AGENTD_HOOK_TIMEOUT=4.5s`;
- non-secret session/execution/platform metadata.

It does not write `CORDUM_API_KEY`, `CORDUM_AGENTD_NONCE`,
`CORDUM_AGENTD_HOOK_NONCE`, provider API keys, bearer tokens, raw prompts, raw
tool payloads, transcripts, or command output to settings.

## Approval UX

A `REQUIRE_APPROVAL` decision becomes a Claude-compatible deny with an
`approval_ref` and retry guidance. Reviewers approve or reject in Cordum. The
agent then retries the same action; replay checks bind the approval to the
action hash, input hash, and policy snapshot. Approval records a governance
decision; it does not edit command content.

For destructive actions, the backend approval must also have matching resolved
audit provenance before the retry is allowed: the tenant audit chain needs an
approved `EventEdgeApprovalResolved` / `edge.approval_resolved` event with the
same `approval_ref` and `action_hash`. A requested-only approval event is not
proof of approval, and raw prompts, transcripts, and tool payloads are not
persisted as audit evidence.

## Fail modes

| Mode | Behavior |
| --- | --- |
| `observe` | Allow degraded actions and record evidence where possible. |
| `enforce` | Allow known-safe degraded actions only; deny risky/unknown actions. |
| `enterprise-strict` | Fail closed when Cordum governance is unavailable. |

Malformed hook input fails closed with redacted stderr. Hook timeout must stay
below Claude Code's 5s command-hook deadline.

## Token tradeoffs

The developer wrapper avoids storing long-lived API keys or hook nonces in
settings/evidence, but same-user process inspection may see runtime process env
while a local demo session is running. That is acceptable for development only.
Enterprise enforcement requires managed settings, endpoint controls, binary
trust, and service-bootstrap/keychain secret handling.

## Next steps

- [Manual demo](demo-edge-claude.md)
- [CLI reference](edge/cli.md)
- [Configuration](edge/configuration.md)
- [Managed settings template](edge/managed-settings-template.md)
- Edge P0 threat model: internal Cordum engineering.

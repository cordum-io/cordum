# Cordum Edge for Claude Code

This guide explains how Cordum Edge governs Claude Code through the P0 command
hook path and `cordumctl edge claude` launcher.

**Cordum stays quiet until governance matters.** Developers see Cordum exactly
when it protects them, their team, and production: before a risky tool runs,
when approval is needed, and when evidence must be exported.

## Command

```bash
CORDUM_GATEWAY=https://localhost:8081 \
CORDUM_TLS_CA=<cordum-checkout>/certs/ca/ca.crt \
CORDUM_API_KEY=<cordum-api-key> \
CORDUM_TENANT_ID=default \
cordumctl edge claude -- --print "summarize this repo"
```

The default stack serves the gateway over HTTPS with a development CA in the
Cordum checkout's `certs/ca/ca.crt`. `CORDUM_TLS_CA` (or `--cacert`) names
that CA, and the launcher passes it to `cordum-agentd` for its Gateway calls.

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

### Local agentd loopback listener (platform note)

`cordumctl edge claude` reserves a loopback `127.0.0.1` port for `cordum-agentd`
and starts agentd on it automatically — no `--agentd-url` override is required on
any platform.

- On Unix/macOS the launcher hands the reserved listener socket to `cordum-agentd`
  across `exec` (handle inheritance), so there is no reserve-then-bind gap.
- On Windows the launcher uses a close-then-bind path instead: it reserves the
  loopback port, releases it, passes only the URL, and `cordum-agentd` binds that
  port itself. Socket-handle inheritance is Unix-only here, and the close-then-bind
  path avoids the Windows `bind: Only one usage of each socket address` failure that
  the inheritance path triggered (`cordum-agentd exited before becoming ready`). The
  `--agentd-url` override is therefore **not** required on Windows.

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

The wrapper renders every mode except `observe` fail-closed: the generated
settings set `CORDUM_AGENTD_FAIL_CLOSED=true`, and the `PreToolUse`,
`UserPromptSubmit`, and `ConfigChange` command hooks carry
`"onFailure": "block"`, so a `cordum-hook` that cannot start, crashes, or times
out blocks the action instead of letting it through. `onFailure` requires
Claude Code v2.1.295 or later; older versions do not honor it. `observe` stays
fail-open by design.

Before it launches Claude Code in a fail-closed mode, the wrapper runs
`claude --version`. `enterprise-strict` refuses to launch a version older than
2.1.295; the other fail-closed modes print a warning and launch. If the version
cannot be read, the wrapper prints a warning and launches. `cordumctl edge
doctor` runs the same check as `claude_code_version`.

`CORDUM_AGENTD_FAIL_CLOSED` defaults to `false`, so fail-open is only possible
for hook settings you wire by hand without `CORDUM_AGENTD_FAIL_CLOSED=true` and
`"onFailure": "block"`: an `enforce` hook wired that way still denies
`PreToolUse` when agentd errors or times out, but lets prompts and config
changes through, and lets any action through when the hook process itself
fails. Run `cordumctl edge doctor` to see the effective posture.

Malformed hook input fails closed with redacted stderr. Hook timeout must stay
below the command-hook `"timeout": 5` that Cordum generates (Claude Code's own
default is 600 seconds).

The same budget bounds the inline approval wait.
`CORDUM_AGENTD_INLINE_APPROVAL_WAIT_TIMEOUT` defaults to `30s`, but agentd ends
the wait 1s before the hook's `4s` agentd budget runs out, so an approval has
about 3 seconds to arrive. One still pending then gets the usual deny with
`approval_ref` and retry guidance, well before Claude Code's timeout fires.
Disable inline wait or use deny-and-retry instead of block-and-wait. See
[edge/configuration.md](edge/configuration.md) for
details.

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

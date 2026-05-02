# cordumctl edge claude — launch wrapper

`cordumctl edge claude` prepares a governed local Claude Code session, starts a
process-local `cordum-agentd`, renders temporary Claude command-hook settings,
and then launches the `claude` binary with those settings. It is the developer
/demo adoption path for Cordum Edge P0; it is **not** an enterprise enforcement
boundary. Fleet enforcement still requires managed Claude settings plus
endpoint controls (see `docs/security/edge-p0-threat-model.md`).

## Usage

```bash
cordumctl edge claude \
  --gateway https://localhost:8081 \
  --api-key "$CORDUM_API_KEY" \
  --tenant default \
  -- --print "Show current repo status"
```

Flags before `--` configure Cordum. Arguments after `--` are forwarded verbatim
to the launched `claude` binary. Cordum supplies `claude --settings
<temp-settings.json>` itself and rejects a forwarded `--settings` override so
the governed settings cannot be bypassed accidentally.

## What the wrapper does

1. Resolves Gateway URL, API key, tenant, principal, cwd, repo, git
   remote/branch/SHA, host/device labels, policy mode, approval-wait timeout,
   and dashboard URL evidence.
2. Generates a 32-byte `crypto/rand` nonce and starts `cordum-agentd` with that
   value in `CORDUM_AGENTD_NONCE` plus Gateway credentials and metadata env.
3. Waits for the local agentd listener and reads the session/execution/dashboard
   evidence from agentd state.
4. Renders a temporary Claude `settings.json` containing command hooks and a
   bare `CORDUM_AGENTD_URL` with **no** nonce query parameter.
5. Launches `claude --settings <temp-settings.json>` with
   `CORDUM_AGENTD_HOOK_NONCE` only in the Claude process environment.
6. Propagates Claude's exit code and cleans up the agentd process plus tempdir.

## Required configuration

`--gateway`, `--api-key`, and `--tenant` are accepted as flags on the `edge
claude` command. They also default from `CORDUM_GATEWAY`, `CORDUM_API_KEY`, and
`CORDUM_TENANT_ID` (tenant defaults to `default` if omitted). Errors are
redacted before printing; API keys and nonces are never printed.

## Optional flags

| Flag | Default | Purpose |
| --- | --- | --- |
| `--claude-path` | `$CLAUDE_PATH` or PATH lookup | Override the Claude Code binary path. |
| `--agentd-path` | `$CORDUM_AGENTD_PATH` or PATH lookup | Override the `cordum-agentd` binary path. |
| `--agentd-url` | reserved free loopback port | Pin the local hook URL (`http://127.0.0.1:<port>/v1/edge/hooks/claude`). |
| `--state-dir` | tempdir-owned state root | Override agentd state directory. |
| `--policy-mode` | `enforce` or `$CORDUM_EDGE_POLICY_MODE` | `observe`, `enforce`, or `enterprise-strict`. |
| `--approval-wait-timeout` | `30s` | Inline approval wait timeout passed to agentd/settings. |
| `--principal` | `$CORDUM_PRINCIPAL_ID`, `$CORDUM_EDGE_PRINCIPAL_ID`, OS user | Principal label for Edge evidence. |
| `--cwd` / `--repo` / `--git-remote` / `--git-branch` / `--git-sha` | cwd + git auto-detect | Repository metadata overrides. |
| `--host-id` / `--device-id` / `--dashboard-url` | host/env auto-detect | Host/device/dashboard metadata overrides. |
| `--hook-command` | `cordum-hook` | Command path embedded in generated hook settings. |
| `--settings-output PATH` | empty | Write generated settings to PATH or `-`; implies no launch and refuses overwrite. |
| `--dry-run` | `false` | Start agentd, render settings, print non-secret summary JSON, skip Claude launch. |
| `--no-launch` | `false` | Start agentd and render settings, then exit without launching Claude. |
| `--verbose` | `false` | Print non-secret diagnostics to stderr. |

## Dry-run and settings output

`--dry-run` exercises the same agentd/session/settings path but skips the Claude
process. It prints a JSON summary containing the resolved metadata, agentd URL,
temporary settings path, session/execution IDs, and dashboard URL. The summary
contains only `api_key_configured: true/false`; it never includes the API key,
agentd nonce, or `CORDUM_AGENTD_HOOK_NONCE`.

Use `--settings-output -` to inspect the generated settings JSON on stdout, or
`--settings-output path/to/settings.json` to create a file. File output is
create-only and refuses to overwrite an existing operator/user settings file.
`--settings-output` implies `--no-launch`.

## Security caveats

- **Same-user impersonation surface:** during a local wrapper run, same-user
  processes may inspect process environments on some platforms and discover the
  runtime hook nonce. This is accepted for developer/demo mode only.
- **No enterprise enforcement by itself:** a developer can still run raw
  `claude` without the wrapper unless managed settings or endpoint policy block
  that path.
- **No binary integrity guarantee:** the wrapper trusts the `claude`,
  `cordum-hook`, and `cordum-agentd` binaries it resolves. Signing and
  notarization are separate enterprise/release controls.
- **No nonce persistence:** `CORDUM_AGENTD_HOOK_NONCE` is injected only into the
  launched Claude process environment. It is not written to generated settings,
  agentd state, logs, metrics labels, or dashboard evidence.

## Verification in Claude Code

After launch, use Claude Code `/hooks` to confirm Cordum command hooks are
active and `/status` to confirm the settings source. Trigger a safe tool action
and verify it reaches local `cordum-agentd`; risky actions should surface the
Cordum decision reason/reference. For CI without a real Claude binary, use the
[Edge fake-hook E2E](../LOCAL_E2E.md#edge-fake-hook-e2e) path.

## Related docs

- [`cordum-hook`](./cordum-hook.md): command-hook runtime, fail modes, and
  Claude output mapping.
- [`cordum-agentd`](./cordum-agentd.md): local session/evidence lifecycle,
  nonce contract, approval wait, and shutdown semantics.
- [`managed-settings-template`](./managed-settings-template.md): enterprise
  managed-settings template and deployment notes.

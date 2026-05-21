# cordumctl edge claude

`cordumctl edge claude` starts a governed local Claude Code session for Cordum
Edge P0. It is the developer/demo path: it starts `cordum-agentd`, renders
temporary Claude command-hook settings, injects the hook nonce into the Claude
process environment, and forwards arguments to `claude`.

It is **not** an enterprise enforcement boundary by itself. A developer who can
run raw `claude` can bypass a wrapper unless managed Claude settings, endpoint
controls, and enterprise bootstrap controls are deployed.

## Syntax

```bash
cordumctl edge claude [edge flags] -- [claude args...]
```

Example with placeholders only:

```bash
CORDUM_GATEWAY=http://localhost:8081 \
CORDUM_API_KEY=<cordum-api-key> \
CORDUM_TENANT_ID=default \
cordumctl edge claude -- --print "summarize the current repository status"
```

Arguments before `--` configure Cordum. Arguments after `--` are forwarded to
Claude Code. The wrapper supplies `claude --settings <temp-settings.json>` and
rejects a forwarded `--settings` override so the governed settings are not
accidentally replaced.

## Required inputs

| Input | Env fallback | Default | Notes |
| --- | --- | --- | --- |
| `--gateway` | `CORDUM_GATEWAY` | `http://localhost:8081` from the global cordumctl flag | Gateway base URL. Use HTTPS outside local dev. |
| `--api-key` | `CORDUM_API_KEY` | none | Secret. Redacted from errors and dry-run output. |
| `--tenant` | `CORDUM_TENANT_ID` | `default` | Tenant used for Gateway auth and evidence. |
| `--principal` | `CORDUM_PRINCIPAL_ID`, `CORDUM_EDGE_PRINCIPAL_ID` | launcher-detected principal where possible | Audit principal for Edge evidence. |

The launcher validates Gateway, API key, tenant, and principal before starting
agentd.

## Optional flags

| Flag | Env/default | Purpose |
| --- | --- | --- |
| `--claude-path` | `CLAUDE_PATH` or PATH lookup | Claude Code binary path. |
| `--agentd-path` | `CORDUM_AGENTD_PATH` or PATH lookup | `cordum-agentd` binary path. |
| `--agentd-url` | reserved free loopback port | Local hook URL to bind and place in settings. |
| `--state-dir` | tempdir-owned state root | Agentd state directory. |
| `--policy-mode` | `CORDUM_EDGE_POLICY_MODE` or `enforce` | `observe`, `enforce`, or `enterprise-strict`. |
| `--approval-wait-timeout` | `30s` | Local/demo inline approval wait timeout passed to agentd. |
| `--cwd` | `CORDUM_EDGE_CWD` or current directory | Working directory for Claude/repo detection. |
| `--repo` | `CORDUM_EDGE_REPO` or auto-detect | Repository label override. |
| `--git-remote` | `CORDUM_EDGE_GIT_REMOTE` or auto-detect | Git remote evidence override. |
| `--git-branch` | `CORDUM_EDGE_GIT_BRANCH` or auto-detect | Git branch evidence override. |
| `--git-sha` | `CORDUM_EDGE_GIT_SHA` or auto-detect | Git SHA evidence override. |
| `--host-id` | `CORDUM_EDGE_HOST_ID` or host detect | Host label override. |
| `--device-id` | `CORDUM_EDGE_DEVICE_ID` or generated detect | Device label override. |
| `--dashboard-url` | `CORDUM_EDGE_DASHBOARD_URL`, `CORDUM_DASHBOARD_URL`, or derived | Dashboard link evidence. |
| `--hook-command` | `CORDUM_HOOK_COMMAND` or `cordum-hook` | Command written into generated Claude settings. |
| `--settings-output PATH` | none | Write generated settings to `PATH` or `-`; implies no launch and refuses overwrite. |
| `--dry-run` | `false` | Start agentd, render settings, print redacted summary JSON, skip Claude launch. |
| `--no-launch` | `false` | Start agentd/render settings, then exit without launching Claude. |
| `--verbose` | `false` | Print non-secret diagnostics to stderr. |

## What the wrapper does

1. Resolves Gateway credentials, tenant, principal, cwd/repo/git/host/device
   metadata, policy mode, approval wait timeout, and dashboard URL evidence.
2. Generates a high-entropy nonce and starts `cordum-agentd` with the nonce in
   `CORDUM_AGENTD_NONCE` plus Gateway credentials and metadata.
3. Waits for agentd to write session/execution/dashboard state.
4. Renders temporary Claude settings with command hooks and a bare
   `CORDUM_AGENTD_URL`.
5. Launches Claude with `CORDUM_AGENTD_HOOK_NONCE` only in the process
   environment.
6. Propagates Claude's exit code and cleans up the tempdir/agentd process.

## Generated settings

Generated settings contain command hooks for Claude events and non-secret
session metadata such as `CORDUM_EDGE_SESSION_ID`, `CORDUM_EDGE_EXECUTION_ID`,
`CORDUM_EDGE_MODE`, `CORDUM_EDGE_PLATFORM`, and `CORDUM_AGENTD_URL`.

Generated settings must not contain:

- `CORDUM_API_KEY`
- `CORDUM_AGENTD_NONCE`
- `CORDUM_AGENTD_HOOK_NONCE`
- `nonce=` URL query strings
- provider API keys, bearer tokens, raw prompts, raw tool payloads, raw
  transcripts, or command output

Use `--settings-output -` to inspect the generated JSON safely. File output is
create-only and refuses to overwrite an existing operator/user settings file.

## Approval UX

When Gateway returns `REQUIRE_APPROVAL`, P0 maps it to a Claude-compatible deny
with `approval_ref` and retry guidance. The user or reviewer approves/rejects in
Cordum, then the agent retries the same action. Replay checks bind the approval
to the original action hash, input hash, and policy snapshot; approval does not
edit the command content.

For destructive actions, Gateway also verifies resolved approval provenance on
the audit chain. A retry is allowed only when the chain contains an approved
`EventEdgeApprovalResolved` / `edge.approval_resolved` row for the same tenant,
`approval_ref`, and `action_hash`; an approval-requested row by itself remains
a pending-review signal, not proof of approval.

The wrapper enables local/demo inline approval wait for convenience. Rejection,
timeout, pending, or wait errors deny the action and ask the user to retry after
review.

## Fail modes

| Mode | Gateway/agentd unavailable | Intended use |
| --- | --- | --- |
| `observe` | Allow degraded and write evidence where possible. | Discovery/local visibility. |
| `enforce` | Allow only known-safe degraded actions; deny risky/unknown actions. | Developer enforcement and demos. |
| `enterprise-strict` | Fail closed. | Managed enterprise rollout. |

Malformed hook input fails closed with redacted stderr. Hook timeout values must
stay below Claude Code's 5s command-hook deadline; generated settings use `4.5s`.

## Token tradeoffs

The wrapper keeps long-lived Gateway/API/provider secrets out of Claude settings
and dashboard evidence. The local hook nonce is still visible to same-user
process inspection on some platforms while the session is running. That tradeoff
is acceptable for developer/demo mode only. Enterprise rollout should use
managed settings plus service bootstrap/keychain controls so users cannot bypass
or inspect enforcement credentials.

## `cordumctl edge managed-settings` — enterprise rollout subcommand

`cordumctl edge managed-settings <export|verify|rollback-template>` is the
enterprise rollout sibling of `cordumctl edge claude`. It is operator/MDM-script
invoked; Cordum never calls Jamf, Intune, or any other MDM API itself. The
end-to-end fleet rollout playbook (Jamf, Intune, Linux/WSL), drift-detection
schedule, and synthetic-rollback test surface live in
[managed-settings-deploy.md](managed-settings-deploy.md).

### Subcommands

| Subcommand | Purpose |
| --- | --- |
| `export` | Render `managed-settings.json` + `managed-mcp.json` into `--output <dir>`. Refuses to overwrite without `--force`. Rejects flag values containing secret markers (`sk-…`, `ghp_…`, bearer tokens). |
| `verify` | Validate a deployed `managed-settings.json` at `--path <file>` against the 14 enterprise invariants (see [managed-settings-deploy.md § 6](managed-settings-deploy.md#6-verification-reference)). `--json` emits `{ok, drifts[], source}`. |
| `rollback-template` | **Synthetic test fixture only — not a production rollback.** Atomically regenerates the template at `--path <file>` (mode `0600`) and re-runs `verify`. Production rollback is MDM-orchestrated; see [managed-settings-deploy.md § 8](managed-settings-deploy.md#8-rollback). |

### Common flags

| Flag | Used by | Purpose |
| --- | --- | --- |
| `--output <dir>` | `export` | Destination directory for both JSON files. Required. |
| `--path <file>` | `verify`, `rollback-template` | Deployed `managed-settings.json` to inspect or atomically rewrite. Required. |
| `--mcp-gateway-url <url>` | `export`, `rollback-template` | Cordum MCP gateway URL to embed. Required. |
| `--llm-proxy-base-url <url>` | `export`, `rollback-template` | Cordum LLM proxy base URL. Required. |
| `--api-key-helper-command <cmd>` | `export`, `rollback-template` | Path to `cordum-agentd claude api-key-helper`. Required. |
| `--hook-command <cmd>` | `export`, `rollback-template` | `cordum-hook` install path. Default `/opt/cordum/bin/cordum-hook`. |
| `--agentd-url <url>` | `export`, `rollback-template` | Local loopback hook URL. Default `http://127.0.0.1:8765/v1/edge/hooks/claude`. |
| `--platform <linux\|darwin\|windows>` | `export`, `rollback-template` | Target platform; default `runtime.GOOS`. Determines hook-path normalisation. |
| `--force` | `export` | Overwrite existing output files (refuse-to-overwrite is the default). |
| `--json` | `verify` | Emit machine-readable envelope. |

### Exit codes

| Code | Subcommand | Meaning |
| --- | --- | --- |
| `0` | all | Success. `export` wrote both files; `verify` reports `ok`; `rollback-template` regenerated and re-verified clean. |
| `1` | `verify`, `rollback-template` | Drift detected (one line per drift on stderr) or post-rollback verification failed. |
| `2` | all | Validation error: missing/sensitive flag, missing or unparseable file, unknown subcommand. |

### Quick reference

```bash
# Generate the payload (operator workstation)
cordumctl edge managed-settings export \
  --output ./payload/ \
  --mcp-gateway-url https://mcp.cordum.example/mcp \
  --llm-proxy-base-url https://llm-proxy.cordum.example \
  --api-key-helper-command "/opt/cordum/bin/cordum-agentd claude api-key-helper"

# Verify a deployed file (workstation, no gateway needed)
cordumctl edge managed-settings verify \
  --path /etc/claude-code/managed-settings.json
```

## Related docs

- [Root Claude Code guide](../edge-claude-code.md)
- [Manual demo](demo.md)
- [Configuration](configuration.md)
- [cordumctl edge doctor](cordumctl-edge-doctor.md)
- [cordum-hook](cordum-hook.md)
- [cordum-agentd](cordum-agentd.md)
- [Managed settings template (synthetic excerpt)](managed-settings-template.md)
- [Managed settings deployment automation](managed-settings-deploy.md)

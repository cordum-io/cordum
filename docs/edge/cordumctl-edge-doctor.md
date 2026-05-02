# cordumctl edge doctor — local diagnostics

`cordumctl edge doctor` verifies the local Cordum Edge Claude Code path without
auto-fixing anything. It is safe for CI and support scripts: checks are bounded,
secrets are not printed, and real Claude Code is not required when you pass a
mock or explicit `--claude-path`.

Use it before demos, after updating generated settings, or while debugging a
local governed Claude session.

## Usage

```bash
CORDUM_GATEWAY=http://localhost:8081 \
CORDUM_API_KEY=<cordum-api-key> \
CORDUM_TENANT_ID=default \
cordumctl edge doctor
```

Machine-readable output:

```bash
cordumctl edge doctor --json
```

Common local/demo invocation with explicit paths:

```bash
cordumctl edge doctor \
  --claude-path /usr/local/bin/claude \
  --hook-command /usr/local/bin/cordum-hook \
  --agentd-path /usr/local/bin/cordum-agentd \
  --settings-path ~/.claude/settings.json \
  --agentd-url http://127.0.0.1:8765/v1/edge/hooks/claude
```

## Flags and environment

| Flag | Env/default | Purpose |
| --- | --- | --- |
| `--gateway` | `CORDUM_GATEWAY` or `http://localhost:8081` | Gateway base URL. |
| `--api-key` | `CORDUM_API_KEY` | Gateway API key. The value is never printed. |
| `--tenant` | `CORDUM_TENANT_ID` or `default` | Tenant sent as `X-Tenant-ID`. |
| `--policy-mode` | `CORDUM_EDGE_POLICY_MODE` or `enforce` | Reports degraded/fail-closed implications. |
| `--claude-path` | `CLAUDE_PATH` or PATH lookup | Claude Code executable to verify. |
| `--hook-command` | `CORDUM_HOOK_COMMAND` or `cordum-hook` | Hook command embedded in generated settings. |
| `--agentd-path` | `CORDUM_AGENTD_PATH` or PATH lookup | `cordum-agentd` executable to verify. |
| `--agentd-url` | `CORDUM_AGENTD_URL`, `CORDUM_AGENTD_SOCKET`, or local default | Local loopback hook URL to dial. |
| `--settings-path` | `CORDUM_EDGE_SETTINGS_PATH`, `CLAUDE_SETTINGS_PATH`, or `~/.claude/settings.json` | Claude settings JSON to validate. |
| `--dashboard-url` | `CORDUM_EDGE_DASHBOARD_URL`, `CORDUM_DASHBOARD_URL` | Optional dashboard reachability probe. |
| `--timeout` | `30` | Overall deadline in seconds. Each check is still individually bounded. |
| `--json` | false | Emit JSON envelope instead of the human table. |

## Checks

The doctor reports `ok`, `warn`, `fail`, or `skip` for each check:

- Gateway `/readyz` reachability.
- Gateway auth and tenant validation via `GET /api/v1/status`.
- Safety Kernel reachability through Gateway via `GET /api/v1/policy/snapshots`.
- Edge sessions API ping via `GET /api/v1/edge/sessions?limit=1`.
- `claude`, `cordum-hook`, and `cordum-agentd` executable discovery.
- Generated Claude settings shape: required Cordum command hooks, session and
  execution metadata, bare `CORDUM_AGENTD_URL`/socket, and no persisted API key
  or nonce fields.
- Local `cordum-agentd` loopback status by bounded TCP connect. P0 agentd does
  not expose a separate health route; the hook endpoint is nonce-protected.
- Edge demo policy fixture rules loaded from `examples/cordum-edge-pack`.
- Optional dashboard reachability.
- Policy-mode implications.

## Exit codes

| Code | Meaning |
| --- | --- |
| `0` | All checks passed or were intentionally skipped. |
| `1` | At least one check failed. |
| `2` | No failures, but at least one warning needs operator attention. |

## JSON schema

`--json` emits this envelope:

```json
{
  "checks": [
    {
      "id": "gateway_auth_tenant",
      "label": "Gateway auth + tenant",
      "state": "ok",
      "detail": "authenticated tenant default; gateway build test",
      "fix": ""
    }
  ],
  "summary": { "ok": 1, "warn": 0, "fail": 0, "skip": 0 },
  "exitCode": 0,
  "policyMode": "enforce"
}
```

The `fix` field is present only when a concise remediation exists. Do not parse
`detail` for secrets or policy decisions; it is operator-facing diagnostic copy.

## Policy-mode implications

| Mode | Doctor copy means |
| --- | --- |
| `observe` | Cordum may degrade open: actions can continue while evidence is marked degraded. Fix warnings before relying on the audit trail. |
| `enforce` | Risky or unknown degraded actions should fail closed while known-safe actions may proceed. Fix failures before demos. |
| `enterprise-strict` | Warnings are expected until managed settings and supervised agentd bootstrap are deployed. Any missing Gateway, Safety Kernel, agentd, hook, or settings path can block governed Claude actions. |

`enterprise-strict` in the developer wrapper is still not a fleet enforcement
boundary by itself. Use managed Claude settings, endpoint controls, and service
bootstrap/keychain controls for enterprise rollout.

## Troubleshooting

| Symptom | Fix |
| --- | --- |
| `no API key configured` | `export CORDUM_API_KEY=<key>` or pass `--api-key`. |
| `GET /readyz failed` | Start Gateway or correct `--gateway`/TLS flags. |
| `/api/v1/policy/snapshots returned 502/503` | Restart Safety Kernel and verify `SAFETY_KERNEL_ADDR` from Gateway. |
| `claude not found` | Install Claude Code or pass `--claude-path` to a test/mock executable in CI. |
| `cordum-hook not found` | Build/install `cordum-hook` or pass `--hook-command`. |
| `cordum-agentd not found` | Build/install `cordum-agentd` or pass `--agentd-path`. |
| `settings.json not found` | Generate settings with `cordumctl edge claude --settings-output <path>` or pass the active temp settings path. |
| `CORDUM_AGENTD_URL must not persist nonce` | Regenerate settings. The nonce belongs only in process environment. |
| `local agentd not reachable` | Start `cordumctl edge claude` or `cordum-agentd` with a loopback `CORDUM_AGENTD_URL`. |
| `Edge demo policy missing` | `cordumctl pack install ./examples/cordum-edge-pack`. |

Doctor never runs fixes automatically. Re-run it after applying a remediation.

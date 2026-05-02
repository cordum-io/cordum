# Demo: Cordum Edge with Claude Code

This root demo guide mirrors the detailed walkthrough in [edge/demo.md](edge/demo.md).
It uses the real P0 path, not the Week 0 spike:

```text
cordumctl edge claude -> cordum-agentd -> cordum-hook -> Gateway /api/v1/edge/*
```

## Setup

```bash
make build SERVICE=cordumctl
make build SERVICE=cordum-hook
make build SERVICE=cordum-agentd

export CORDUM_GATEWAY=http://localhost:8081
export CORDUM_API_KEY=<cordum-api-key>
export CORDUM_TENANT_ID=default
export CORDUM_PRINCIPAL_ID=<your-user-id>
```

Use placeholders only in docs and screenshots. Do not paste real secrets,
production commands, raw prompts, transcripts, or provider tokens.

## Inspect settings

```bash
./bin/cordumctl edge claude \
  --agentd-path ./bin/cordum-agentd \
  --hook-command ./bin/cordum-hook \
  --settings-output -
```

Expected: command hooks, bare loopback `CORDUM_AGENTD_URL`, hook timeout below
`5s`, and no API key or nonce in the JSON.

## Dry run

```bash
./bin/cordumctl edge claude \
  --agentd-path ./bin/cordum-agentd \
  --hook-command ./bin/cordum-hook \
  --dry-run
```

Expected: redacted JSON summary with `api_key_configured: true`, session and
execution IDs, policy mode, agentd URL, settings path, and dashboard URL.

## Launch

```bash
./bin/cordumctl edge claude \
  --agentd-path ./bin/cordum-agentd \
  --hook-command ./bin/cordum-hook \
  -- --print "Summarize the repository status, then stop."
```

Then use Claude Code `/hooks` and `/status` to confirm the Cordum command hooks
and settings source.

## Expected dashboard evidence

- Edge session for the Claude Code run with tenant/principal/policy metadata.
- Agent execution for the launched process.
- Ordered action events for safe, denied, approval, degraded, and artifact cases.
- Approval drawer shows requester, redacted action summary, hashes, policy
  snapshot, expiry, and self/stale/terminal warnings.
- Artifacts panel shows pointer metadata only and never raw bytes.
- Evidence export returns a metadata-only bundle or a redacted error.

## Troubleshooting

| Symptom | Fix |
| --- | --- |
| Missing metadata/API key error | Set `CORDUM_GATEWAY`, `CORDUM_API_KEY`, `CORDUM_TENANT_ID`, and principal env/flags. |
| Nonce URL error | Regenerate settings; use `CORDUM_AGENTD_HOOK_NONCE` process env, never `?nonce=`. |
| Hook timeout | Keep `CORDUM_AGENTD_HOOK_TIMEOUT` at generated `4.5s` and check Gateway health. |
| No dashboard session | Check tenant/API key, agentd stderr, and Gateway reachability. |
| Approval not visible | Use the requester principal or operator/admin role and refresh after expiry. |
| Raw `claude` bypasses Edge | Deploy enterprise managed settings and endpoint controls; the wrapper is not fleet enforcement. |

For headless CI, use [LOCAL_E2E.md](LOCAL_E2E.md#edge-fake-hook-e2e).
